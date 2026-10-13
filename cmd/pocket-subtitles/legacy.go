package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/library"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/store"
)

// runImportLegacy reads the files written by the sync_hook.py / sync-sweep.py
// / nudge.py scripts this app replaces:
//
//	sync-history.jsonl  every finished hook run      → runs
//	sync-ledger.jsonl   per-file verdict + signature → subtitles (so the sweep
//	                                                  skips what is unchanged)
//	sync-pinned.txt     pinned files with notes      → subtitles.pinned
//	nudge-log.jsonl     manual shifts, undo, unpin   → nudges, events
func runImportLegacy(args []string) {
	fs := flag.NewFlagSet("import-legacy", flag.ExitOnError)
	dir := fs.String("dir", "", "directory holding sync-history.jsonl, sync-ledger.jsonl, sync-pinned.txt, nudge-log.jsonl")
	maps := fs.String("map", "", "media path mapping OLD=NEW[,OLD=NEW…] (e.g. /home/me/media=/media)")
	backups := fs.String("backups", "", "nudge backup mapping OLD=NEW (e.g. /home/me/bazarr/config/sync-backups=/data/backups)")
	reverify := fs.String("reverify-before", "", "re-check verdicts older than this date (YYYY-MM-DD) on the next sweeps")
	force := fs.Bool("force", false, "import even though the history is not empty")
	_ = fs.Parse(args)
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "import-legacy: -dir is required")
		os.Exit(2)
	}
	cfg, log, st := mustLoad()
	defer st.Close()
	ctx := context.Background()
	var n int
	_ = st.DB.QueryRow(`SELECT COUNT(1) FROM runs`).Scan(&n)
	if n > 0 && !*force {
		fmt.Fprintf(os.Stderr, "import-legacy: the history already has %d runs; use -force to import anyway\n", n)
		os.Exit(1)
	}
	mp := pairs(*maps)
	bp := pairs(*backups)
	mapPath := func(p string, m [][2]string) string {
		for _, x := range m {
			if p == x[0] || strings.HasPrefix(p, x[0]+"/") {
				return x[1] + p[len(x[0]):]
			}
		}
		return p
	}

	w := newWorker(cfg, st, log)
	scan, err := w.Scan(ctx)
	if err != nil {
		log.Error("scan", "err", err)
		os.Exit(1)
	}
	fmt.Printf("library: %d subtitle files\n", scan.Files)
	byName := map[string]string{}
	subs, _, _ := st.ListSubtitles(ctx, store.SubtitleFilter{Missing: true})
	for _, s := range subs {
		byName[filepath.Base(s.Path)] = s.Path
	}
	decision := func(d string) string {
		switch d {
		case "driver_timeout":
			return "timeout"
		case "driver_parse_error":
			return "error"
		}
		return d
	}
	at := func(ts float64) string {
		sec, frac := math.Modf(ts)
		return store.TimeString(time.Unix(int64(sec), int64(frac*1e9)))
	}

	// ledger: last entry per file wins
	type ledgerEntry struct {
		Subtitle string   `json:"subtitle"`
		Video    string   `json:"video"`
		Decision string   `json:"decision"`
		TS       float64  `json:"ts"`
		Method   string   `json:"method"`
		MaxShift *float64 `json:"max_shift"`
		Reason   string   `json:"reason"`
		SubSig   *struct {
			Size  int64   `json:"size"`
			Mtime float64 `json:"mtime"`
		} `json:"sub_sig"`
		VidSig *struct {
			Size  int64   `json:"size"`
			Mtime float64 `json:"mtime"`
		} `json:"vid_sig"`
	}
	ledger := map[string]ledgerEntry{}
	eachLine(filepath.Join(*dir, "sync-ledger.jsonl"), func(b []byte) {
		var e ledgerEntry
		if json.Unmarshal(b, &e) == nil && e.Subtitle != "" {
			e.Subtitle = mapPath(e.Subtitle, mp)
			ledger[e.Subtitle] = e
			if _, ok := byName[filepath.Base(e.Subtitle)]; !ok {
				byName[filepath.Base(e.Subtitle)] = e.Subtitle
			}
		}
	})
	verdicts := 0
	for p, e := range ledger {
		if _, err := st.GetSubtitleByPath(ctx, p); err != nil {
			continue // the file is gone
		}
		v := store.Verdict{Decision: decision(e.Decision), Reason: e.Reason, Method: e.Method, Shift: e.MaxShift, CheckedAt: at(e.TS)}
		if e.SubSig != nil {
			v.SubSize, v.SubMtime = &e.SubSig.Size, &e.SubSig.Mtime
		}
		if e.VidSig != nil {
			v.VidSize, v.VidMtime = &e.VidSig.Size, &e.VidSig.Mtime
		}
		if err := st.SetVerdict(ctx, p, v); err == nil {
			verdicts++
		}
	}
	fmt.Printf("ledger: %d files, %d verdicts applied\n", len(ledger), verdicts)

	// history
	tx, _ := st.DB.Begin()
	runs := 0
	eachLine(filepath.Join(*dir, "sync-history.jsonl"), func(b []byte) {
		var e map[string]any
		if json.Unmarshal(b, &e) != nil {
			return
		}
		name, _ := e["subtitle"].(string)
		ts, _ := e["ts"].(float64)
		if name == "" || ts == 0 {
			return
		}
		path := byName[name]
		pretty := library.Prettify(name)
		title, label := pretty, pretty
		if i := strings.LastIndex(pretty, " S"); i > 0 && len(pretty)-i == 7 {
			title, label = pretty[:i], pretty[i+1:]
		}
		lang, _ := library.ParseLang(name, "")
		f := func(k string) *float64 {
			if v, ok := e[k].(float64); ok {
				return &v
			}
			return nil
		}
		dec, _ := e["decision"].(string)
		method, _ := e["method"].(string)
		reason, _ := e["reason"].(string)
		_, err := tx.Exec(`INSERT INTO runs (path, name, title, label, lang, source, decision, method, max_shift, content_fit, ref_fit, reason, details, created_at)
			VALUES (?, ?, ?, ?, ?, 'import', ?, ?, ?, ?, ?, ?, ?, ?)`,
			path, name, title, label, lang, decision(dec), method, f("max_shift"), f("content_fit"), f("ref_fit"), reason, string(b), at(ts))
		if err == nil {
			runs++
		}
	})
	if err := tx.Commit(); err != nil {
		log.Error("import history", "err", err)
		os.Exit(1)
	}
	linked, _ := st.LinkRuns(ctx)
	_, _ = st.DB.Exec(`UPDATE subtitles SET last_run_id = (SELECT id FROM runs r WHERE r.path = subtitles.path ORDER BY created_at DESC LIMIT 1) WHERE last_run_id IS NULL`)
	fmt.Printf("history: %d runs imported, %d linked to library files\n", runs, linked)

	// pins
	pinned := 0
	if f, err := os.Open(filepath.Join(*dir, "sync-pinned.txt")); err == nil {
		var note []string
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				note = nil
				continue
			}
			if strings.HasPrefix(line, "#") {
				note = append(note, strings.TrimSpace(strings.TrimPrefix(line, "#")))
				continue
			}
			name := strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
			p := mapPath(name, mp)
			if !strings.HasPrefix(p, "/") {
				p = byName[name]
			}
			if p == "" {
				continue
			}
			text := strings.Join(note, " ")
			if len(text) > 400 {
				text = text[:400] + "…"
			}
			if text == "" {
				text = "pinned (imported)"
			}
			if _, err := st.GetSubtitleByPath(ctx, p); err == nil {
				_ = st.SetPinned(ctx, p, true, text)
				pinned++
			}
			note = nil
		}
		f.Close()
	}
	fmt.Printf("pins: %d files pinned\n", pinned)

	// nudges
	nudges, net := 0, map[string]int{}
	eachLine(filepath.Join(*dir, "nudge-log.jsonl"), func(b []byte) {
		var e struct {
			Action string  `json:"action"`
			Path   string  `json:"path"`
			MS     int     `json:"ms"`
			Backup string  `json:"backup"`
			Cues   int     `json:"cues"`
			Undone bool    `json:"undone"`
			TS     float64 `json:"ts"`
		}
		if json.Unmarshal(b, &e) != nil || e.Path == "" {
			return
		}
		p := mapPath(e.Path, mp)
		switch e.Action {
		case "apply":
			_ = st.CreateNudge(ctx, &store.Nudge{Path: p, MS: e.MS, Backup: mapPath(e.Backup, bp), Cues: e.Cues, Undone: e.Undone, CreatedAt: at(e.TS)})
			st.AddEventAt(ctx, p, "nudge", fmt.Sprintf("%+d ms, %d cues", e.MS, e.Cues), at(e.TS))
			net[p] += e.MS
			nudges++
		case "undo":
			st.AddEventAt(ctx, p, "undo", fmt.Sprintf("undid %+d ms", e.MS), at(e.TS))
			net[p] -= e.MS
		case "unpin":
			st.AddEventAt(ctx, p, "unpin", "handed back to the automatic sync", at(e.TS))
		}
	})
	for p, ms := range net {
		_ = st.SetNudgeMS(ctx, p, ms)
	}
	fmt.Printf("nudges: %d imported across %d files\n", nudges, len(net))

	if *reverify != "" {
		s, _ := st.GetSettings(ctx)
		s.ReverifyFrom = *reverify
		_ = st.SaveSettings(ctx, s)
		fmt.Printf("verdicts older than %s will be re-checked by the sweep\n", *reverify)
	}
}

func pairs(s string) [][2]string {
	var out [][2]string
	for _, p := range strings.Split(s, ",") {
		a, b, ok := strings.Cut(strings.TrimSpace(p), "=")
		if ok && a != "" && b != "" {
			out = append(out, [2]string{strings.TrimRight(a, "/"), strings.TrimRight(b, "/")})
		}
	}
	return out
}

func eachLine(path string, fn func([]byte)) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skip %s: %v\n", filepath.Base(path), err)
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		if b := sc.Bytes(); len(b) > 0 {
			fn(append([]byte(nil), b...))
		}
	}
}
