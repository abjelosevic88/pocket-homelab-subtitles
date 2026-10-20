package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/bazarr"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/library"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/store"
)

func videoFor(p string) string { return library.FindVideo(p) }

// SeasonRequest asks for one of the season tools.
type SeasonRequest struct {
	Title  string `json:"title"`
	Season int    `json:"season"`
	Mode   string `json:"mode"`  // resync | verify | reset
	Fresh  bool   `json:"fresh"` // resync: rebuild the audio references first
}

// StartSeason runs a season tool in the background.
func (w *Worker) StartSeason(ctx context.Context, r SeasonRequest, userID *int64) (*store.Task, error) {
	if r.Title == "" {
		return nil, errors.New("title is required")
	}
	kind := map[string]string{"resync": "season_resync", "verify": "season_verify", "reset": "season_reset"}[r.Mode]
	if kind == "" {
		return nil, errors.New("mode must be resync, verify or reset")
	}
	if r.Mode == "reset" {
		st, _ := w.st.GetSettings(ctx)
		if _, err := w.bazarrClient(ctx, st); err != nil {
			return nil, err
		}
	}
	if !w.seasonMu.TryLock() {
		return nil, fmt.Errorf("a season tool is %w", ErrBusy)
	}
	verb := map[string]string{"resync": "Re-sync", "verify": "Verify", "reset": "Reset"}[r.Mode]
	title := fmt.Sprintf("%s %s season %d", verb, r.Title, r.Season)
	if r.Season == 0 && r.Mode != "reset" {
		title = fmt.Sprintf("%s %s", verb, r.Title)
	}
	task, err := w.st.CreateTask(ctx, kind, title, r, userID)
	if err != nil {
		w.seasonMu.Unlock()
		return nil, err
	}
	tctx, cancel := context.WithCancel(w.ctx)
	w.mu.Lock()
	w.tasks[task.ID] = cancel
	w.mu.Unlock()
	go func() {
		defer w.seasonMu.Unlock()
		defer w.endTask(task.ID)
		var err error
		var summary map[string]any
		switch r.Mode {
		case "resync":
			summary, err = w.seasonResync(tctx, task, r)
		case "verify":
			summary, err = w.seasonVerify(tctx, task, r, nil)
		case "reset":
			summary, err = w.seasonReset(tctx, task, r)
		}
		state := "done"
		if err != nil {
			state = "failed"
			if errors.Is(err, context.Canceled) {
				state = "cancelled"
			}
			w.st.TaskLog(context.Background(), task.ID, "stopped: %v", err)
			if summary == nil {
				summary = map[string]any{}
			}
			summary["error"] = err.Error()
		}
		_ = w.st.FinishTask(context.Background(), task.ID, state, summary)
		st, _ := w.st.GetSettings(context.Background())
		if r.Mode == "reset" && st.NotifyKind != "" {
			level, head := notify.Success, "done"
			if state != "done" || toInt(summary["failed"]) > 0 {
				level, head = notify.Warning, "needs attention"
			}
			w.notify(context.Background(), st, title+": "+head, fmt.Sprint(summary["verdict"]), level)
		}
	}()
	return task, nil
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}

func (w *Worker) seasonFiles(ctx context.Context, r SeasonRequest) ([]store.Subtitle, error) {
	f := store.SubtitleFilter{Title: r.Title}
	if r.Season > 0 {
		f.Season = &r.Season
	}
	subs, _, err := w.st.ListSubtitles(ctx, f)
	return subs, err
}

// waitJobs waits until none of the given jobs is queued or running.
func (w *Worker) waitJobs(ctx context.Context, task *store.Task, ids []int64) error {
	total := len(ids)
	for {
		left := 0
		for _, id := range ids {
			j, err := w.st.GetJob(ctx, id)
			if err == nil && (j.State == "queued" || j.State == "running") {
				left++
			}
		}
		w.st.TaskProgress(context.Background(), task.ID, total-left, total)
		if left == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			for _, id := range ids {
				_ = w.Cancel(context.Background(), id)
			}
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// purgeCaches moves a video's measurements aside so the next run rebuilds
// them from the real audio. Act boundaries and extracted tracks are kept:
// they are facts about the file, not measurements.
func (w *Worker) purgeCaches(videos []string) int {
	dir := filepath.Join(w.cfg.CacheDir, "..", "cache-quarantine", time.Now().Format("20060102"))
	n := 0
	for _, v := range videos {
		fi, err := os.Stat(v)
		if err != nil {
			continue
		}
		prefix := fmt.Sprintf("%s.%d", filepath.Base(v), fi.Size())
		matches, _ := filepath.Glob(filepath.Join(w.cfg.CacheDir, globEscape(prefix)+"*"))
		for _, m := range matches {
			if strings.HasSuffix(m, ".bounds.json") || strings.HasSuffix(m, ".ref.srt") {
				continue
			}
			_ = os.MkdirAll(dir, 0o755)
			if os.Rename(m, filepath.Join(dir, filepath.Base(m))) == nil {
				n++
			}
		}
	}
	return n
}

func globEscape(s string) string {
	return regexp.MustCompile(`([*?\[\\])`).ReplaceAllString(s, `\$1`)
}

func (w *Worker) seasonResync(ctx context.Context, task *store.Task, r SeasonRequest) (map[string]any, error) {
	logf := func(f string, a ...any) { w.st.TaskLog(context.Background(), task.ID, f, a...) }
	subs, err := w.seasonFiles(ctx, r)
	if err != nil {
		return nil, err
	}
	if len(subs) == 0 {
		return nil, errors.New("no subtitles found for that title/season")
	}
	logf("%d subtitle files", len(subs))
	unpinned := 0
	videos := map[string]bool{}
	for _, s := range subs {
		if s.Pinned {
			_ = w.st.SetPinned(ctx, s.Path, false, "")
			w.st.AddEvent(ctx, s.Path, "unpin", "season re-sync", task.UserID)
			unpinned++
		}
		if s.VideoPath != "" {
			videos[s.VideoPath] = true
		}
	}
	if unpinned > 0 {
		logf("unpinned %d (a pin would block the re-sync)", unpinned)
	}
	if r.Fresh {
		vs := make([]string, 0, len(videos))
		for v := range videos {
			vs = append(vs, v)
		}
		logf("moved %d cached measurements aside — fresh audio references", w.purgeCaches(vs))
	}
	var ids []int64
	for _, s := range subs {
		res, err := w.Enqueue(ctx, Request{Path: s.Path, Video: s.VideoPath, Source: "season", Priority: PrioritySeason, Force: true, TaskID: &task.ID})
		if err != nil {
			logf("could not queue %s: %v", s.Rel, err)
			continue
		}
		ids = append(ids, res.Job.ID)
	}
	logf("queued %d syncs", len(ids))
	if err := w.waitJobs(ctx, task, ids); err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, id := range ids {
		if j, err := w.st.GetJob(ctx, id); err == nil {
			d := j.Decision
			if d == "" {
				d = j.State
			}
			counts[d]++
			logf("%-28s %s", DecisionLabel(d), j.Label+" "+strings.ToUpper(j.Lang))
		}
	}
	return w.seasonVerify(ctx, task, r, counts)
}

var langRE = regexp.MustCompile(`(?i)\.([a-z]{2,3}(?:-[a-z]{2,4})?)(\.hi|\.sdh|\.forced)?\.srt$`)

// seasonVerify checks every file independently against the real audio: the
// whole-file offset must sit within the tolerance of the reading lead, Serbian
// must be Latin when that is the house style, and no machine-translation smell.
func (w *Worker) seasonVerify(ctx context.Context, task *store.Task, r SeasonRequest, synced map[string]int) (map[string]any, error) {
	logf := func(f string, a ...any) { w.st.TaskLog(context.Background(), task.ID, f, a...) }
	st, _ := w.st.GetSettings(ctx)
	subs, err := w.seasonFiles(ctx, r)
	if err != nil {
		return nil, err
	}
	logf("— independent verification (tolerance ±%d ms around the %d ms lead) —", st.VerifyTolMS, st.LeadMS)
	var failed []string
	ok := 0
	// duplicates: two files of one language next to one video
	perVideo := map[string]map[string]int{}
	for _, s := range subs {
		if perVideo[s.VideoPath] == nil {
			perVideo[s.VideoPath] = map[string]int{}
		}
		perVideo[s.VideoPath][s.Lang]++
	}
	for i, s := range subs {
		w.st.TaskProgress(context.Background(), task.ID, i, len(subs))
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		res, err := w.VerifyFile(ctx, s.Path, nil)
		tag := strings.TrimSpace(s.Label + " " + strings.ToUpper(s.Lang))
		if err != nil {
			failed = append(failed, tag+": "+err.Error())
			logf("BAD  %-12s %s", tag, err.Error())
			continue
		}
		var problems []string
		if p, ok := res["problems"].([]any); ok {
			for _, x := range p {
				problems = append(problems, fmt.Sprint(x))
			}
		}
		if perVideo[s.VideoPath][s.Lang] > 1 {
			problems = append(problems, "two files in this language")
		}
		off := "  n/a"
		if v, ok := res["offset"].(float64); ok {
			off = fmt.Sprintf("%+.2f", v)
		}
		if len(problems) == 0 {
			ok++
			logf("OK   %-12s %s", tag, off)
		} else {
			failed = append(failed, tag+": "+strings.Join(problems, "; "))
			logf("BAD  %-12s %s  %s", tag, off, strings.Join(problems, "; "))
		}
	}
	w.st.TaskProgress(context.Background(), task.ID, len(subs), len(subs))
	verdict := fmt.Sprintf("%d/%d files verified", ok, len(subs))
	if len(failed) > 0 {
		verdict += fmt.Sprintf(", %d failed:\n%s", len(failed), strings.Join(firstN(failed, 12), "\n"))
	} else {
		verdict += " — all good"
	}
	logf("%s", verdict)
	out := map[string]any{"files": len(subs), "ok": ok, "failed": len(failed), "problems": failed, "verdict": verdict}
	if synced != nil {
		out["decisions"] = synced
	}
	return out, nil
}

func firstN(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(s[:n:n], fmt.Sprintf("… and %d more", len(s)-n))
}

// seasonReset deletes a season's subtitles through Bazarr, downloads them
// again (manual provider search for the languages that need it, preferring a
// provider and Latin-script packs), lets every download sync, removes HI
// duplicates and verifies the result. Anything no provider serves any more is
// put back from the backup, so a reset never leaves fewer subtitles behind.
func (w *Worker) seasonReset(ctx context.Context, task *store.Task, r SeasonRequest) (map[string]any, error) {
	logf := func(f string, a ...any) { w.st.TaskLog(context.Background(), task.ID, f, a...) }
	st, _ := w.st.GetSettings(ctx)
	bz, err := w.bazarrClient(ctx, st)
	if err != nil {
		return nil, err
	}
	if r.Season <= 0 {
		return nil, errors.New("pick a season")
	}
	series, err := bz.FindSeries(ctx, r.Title)
	if err != nil {
		return nil, err
	}
	all, err := bz.Episodes(ctx, series.ID)
	if err != nil {
		return nil, err
	}
	var eps []bazarr.Episode
	for _, e := range all {
		if e.Season == r.Season && e.Path != "" {
			eps = append(eps, e)
		}
	}
	sort.Slice(eps, func(i, j int) bool { return eps[i].Episode < eps[j].Episode })
	if len(eps) == 0 {
		return nil, errors.New("Bazarr knows no episodes with files for that season")
	}
	langs := strings.Split(store.NormLangs(st.SeasonLangs), ",")
	if st.SeasonLangs == "" {
		langs = []string{}
		seen := map[string]bool{}
		for _, e := range eps {
			for _, s := range e.Subtitles {
				if s.Path != "" && !seen[s.Code2] {
					seen[s.Code2] = true
					langs = append(langs, s.Code2)
				}
			}
		}
	}
	slug := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(series.Title), "-"), "-")
	backup := filepath.Join(w.cfg.BackupDir, "manual", fmt.Sprintf("%s-s%02d-%s", slug, r.Season, time.Now().Format("20060102-1504")))
	_ = os.MkdirAll(backup, 0o755)
	logf("%s season %d: %d episodes × %s → backups in %s", series.Title, r.Season, len(eps), strings.Join(langs, ", "), backup)

	// 1. unpin, back up, move caches aside
	var current []string
	var videos []string
	for _, e := range eps {
		videos = append(videos, e.Path)
		current = append(current, seasonSubs(e.Path)...)
	}
	for _, p := range current {
		if s, err := w.st.GetSubtitleByPath(ctx, p); err == nil && s.Pinned {
			_ = w.st.SetPinned(ctx, p, false, "")
			w.st.AddEvent(ctx, p, "unpin", "season reset", task.UserID)
		}
		_ = copyFile(p, filepath.Join(backup, filepath.Base(p)))
	}
	logf("backed up %d subtitle files", len(current))
	logf("moved %d cached measurements aside — fresh audio references", w.purgeCaches(videos))

	// 2. delete through Bazarr
	deleted := 0
	for _, e := range eps {
		for _, s := range e.Subtitles {
			if s.Path == "" {
				continue
			}
			if err := bz.DeleteEpisodeSubtitle(ctx, series.ID, e.ID, s); err != nil {
				logf("delete %s: %v", filepath.Base(s.Path), err)
				continue
			}
			deleted++
		}
	}
	logf("deleted %d subtitles through Bazarr", deleted)
	time.Sleep(3 * time.Second)
	left := 0
	for _, e := range eps {
		left += len(seasonSubs(e.Path))
	}
	if left > 0 {
		return nil, fmt.Errorf("%d files survived the delete — stopping before anything is downloaded", left)
	}

	// 3. download again
	if t := bz.Throttled(ctx); len(t) > 0 {
		logf("throttled providers: %s", strings.Join(t, ", "))
	}
	type miss struct {
		ep   bazarr.Episode
		lang string
	}
	var misses []miss
	fetch := func(e bazarr.Episode, lang string, manual bool) (bool, string) {
		since := time.Now()
		if manual {
			return w.manualFetch(ctx, bz, st, series.ID, e, lang, since)
		}
		return w.autoFetch(ctx, bz, series.ID, e, lang, since)
	}
	for i, e := range eps {
		for _, lang := range langs {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			tag := fmt.Sprintf("S%02dE%02d %s", e.Season, e.Episode, lang)
			ok, detail := fetch(e, lang, store.LangIn(lang, st.SeasonManualLang))
			logf("[%s] %s %s", tag, map[bool]string{true: "OK  ", false: "MISS"}[ok], detail)
			if !ok {
				misses = append(misses, miss{e, lang})
			}
			time.Sleep(4 * time.Second) // stay below provider rate limits
		}
		w.st.TaskProgress(context.Background(), task.ID, i+1, len(eps))
	}
	restored := []string{}
	if len(misses) > 0 {
		logf("%d not found on the first pass; trying the other search", len(misses))
		for _, m := range misses {
			tag := fmt.Sprintf("S%02dE%02d %s", m.ep.Season, m.ep.Episode, m.lang)
			ok, detail := fetch(m.ep, m.lang, !store.LangIn(m.lang, st.SeasonManualLang))
			logf("[%s] retry %s %s", tag, map[bool]string{true: "OK  ", false: "MISS"}[ok], detail)
			if ok {
				continue
			}
			stem := strings.TrimSuffix(filepath.Base(m.ep.Path), filepath.Ext(m.ep.Path))
			put := false
			for _, suffix := range []string{"." + m.lang + ".srt", "." + m.lang + ".hi.srt"} {
				src := filepath.Join(backup, stem+suffix)
				if exists(src) {
					if copyFile(src, filepath.Join(filepath.Dir(m.ep.Path), stem+suffix)) == nil {
						logf("[%s] no provider has it — the OLD file was put back from the backup; check its quality", tag)
						restored = append(restored, tag)
						put = true
					}
					break
				}
			}
			if !put {
				logf("[%s] no provider has it and there was no old file", tag)
			}
		}
	}
	logf("download phase complete")

	// 4. make sure every file got its sync (downloads sync on arrival via the hook)
	var ids []int64
	for _, e := range eps {
		for _, p := range seasonSubs(e.Path) {
			last, _ := w.st.LastRunFor(ctx, p)
			if last != nil && last.CreatedAt >= store.TimeString(time.Now().Add(-6*time.Hour)) && last.Source != "import" {
				continue
			}
			if res, err := w.Enqueue(ctx, Request{Path: p, Video: e.Path, Source: "season", Priority: PrioritySeason, Force: true, TaskID: &task.ID}); err == nil {
				ids = append(ids, res.Job.ID)
			}
		}
	}
	if len(ids) > 0 {
		logf("syncing %d files that did not come through the hook", len(ids))
		if err := w.waitJobs(ctx, task, ids); err != nil {
			return nil, err
		}
	}

	// 5. one file per language: drop HI twins of a plain file through Bazarr
	dupDir := filepath.Join(backup, "duplicates")
	all, _ = bz.Episodes(ctx, series.ID)
	for _, e := range all {
		if e.Season != r.Season || e.Path == "" {
			continue
		}
		stem := strings.TrimSuffix(e.Path, filepath.Ext(e.Path))
		for _, lang := range langs {
			plain, hi := stem+"."+lang+".srt", stem+"."+lang+".hi.srt"
			if exists(plain) && exists(hi) {
				_ = copyFile(hi, filepath.Join(dupDir, filepath.Base(hi)))
				_ = bz.DeleteEpisodeSubtitle(ctx, series.ID, e.ID, bazarr.EpisodeSub{Code2: lang, Path: hi, HI: true})
				if exists(hi) {
					_ = os.Remove(hi)
				}
				_ = w.st.MarkMissing(ctx, hi)
				logf("removed duplicate %s", filepath.Base(hi))
			}
		}
	}
	if _, err := w.Scan(ctx); err != nil {
		logf("scan: %v", err)
	}
	out, err := w.seasonVerify(ctx, task, r, nil)
	if out != nil {
		out["restored_from_backup"] = restored
	}
	return out, err
}

// seasonSubs lists the external subtitles next to one video.
func seasonSubs(video string) []string {
	stem := strings.TrimSuffix(video, filepath.Ext(video))
	m, _ := filepath.Glob(globEscape(stem) + ".*.srt")
	sort.Strings(m)
	return m
}

func findSubFor(video, lang string) string {
	for _, p := range seasonSubs(video) {
		if mm := langRE.FindStringSubmatch(filepath.Base(p)); mm != nil && strings.EqualFold(mm[1], lang) {
			return p
		}
	}
	return ""
}

// arrival waits until a subtitle in lang exists next to the video and its
// sync (triggered by Bazarr's post-processing hook) has finished.
func (w *Worker) arrival(ctx context.Context, video, lang string, since time.Time, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	sinceS := store.TimeString(since)
	found := ""
	for time.Now().Before(deadline) {
		if found == "" {
			found = findSubFor(video, lang)
		}
		if found != "" {
			if last, _ := w.st.LastRunFor(ctx, found); last != nil && last.CreatedAt >= sinceS {
				return found
			}
			// arrived but not synced yet: give the hook up to 15 more minutes
			if time.Until(deadline) < 15*time.Minute {
				deadline = time.Now().Add(15 * time.Minute)
			}
		}
		select {
		case <-ctx.Done():
			return found
		case <-time.After(5 * time.Second):
		}
	}
	return found
}

func (w *Worker) autoFetch(ctx context.Context, bz *bazarr.Client, seriesID int64, e bazarr.Episode, lang string, since time.Time) (bool, string) {
	if err := bz.SearchEpisode(ctx, seriesID, e.ID, lang); err != nil {
		return false, err.Error()
	}
	p := w.arrival(ctx, e.Path, lang, since, 5*time.Minute)
	if p == "" {
		return false, "no provider had it (auto search)"
	}
	return true, filepath.Base(p) + fmt.Sprintf(" (%.0fs)", time.Since(since).Seconds())
}

// manualFetch uses Bazarr's manual provider search, which also offers
// low-scoring human subtitles the automatic search refuses. Ranking: the
// preferred provider first; packs whose release note says Cyrillic last when
// the language is kept in Latin; non-HI before HI; then score.
func (w *Worker) manualFetch(ctx context.Context, bz *bazarr.Client, st store.Settings, seriesID int64, e bazarr.Episode, lang string, since time.Time) (bool, string) {
	cands, err := bz.ManualSearch(ctx, e.ID)
	if err != nil {
		return false, "manual search: " + err.Error()
	}
	var pick []bazarr.Candidate
	for _, c := range cands {
		if strings.EqualFold(c.Language, lang) {
			pick = append(pick, c)
		}
	}
	if len(pick) == 0 {
		return false, "manual search: nothing in this language"
	}
	latin := store.LangIn(lang, st.LatinizeLangs)
	cyr := func(c bazarr.Candidate) bool {
		s := strings.ToLower(strings.Join(c.ReleaseInfo, " "))
		return strings.Contains(s, "ćirilica") || strings.Contains(s, "cirilica") || strings.Contains(s, "cyrillic")
	}
	sort.SliceStable(pick, func(i, j int) bool {
		a, b := pick[i], pick[j]
		pa, pb := st.PreferProvider != "" && a.Provider == st.PreferProvider, st.PreferProvider != "" && b.Provider == st.PreferProvider
		if pa != pb {
			return pa
		}
		if latin && cyr(a) != cyr(b) {
			return !cyr(a)
		}
		if a.HI != b.HI {
			return !a.HI
		}
		return a.Score > b.Score
	})
	best := pick[0]
	if err := bz.ManualDownload(ctx, seriesID, e.ID, best, lang); err != nil {
		return false, "manual download: " + err.Error()
	}
	p := w.arrival(ctx, e.Path, lang, since, 5*time.Minute)
	if p == "" {
		return false, "manual download: the file never arrived"
	}
	note := ""
	if strings.HasSuffix(p, "."+lang+".hi.srt") {
		plain := strings.TrimSuffix(p, "."+lang+".hi.srt") + "." + lang + ".srt"
		if !exists(plain) && os.Rename(p, plain) == nil {
			_ = w.st.RenamePath(ctx, p, plain)
			p, note = plain, ", renamed hi → plain"
		}
	}
	hi := ""
	if best.HI {
		hi = " HI"
	}
	return true, fmt.Sprintf("manual: %s%s score %.0f → %s%s", best.Provider, hi, best.Score, filepath.Base(p), note)
}
