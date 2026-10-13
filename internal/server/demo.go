package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"path/filepath"
	"time"

	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/store"
)

// seedDemo fills an empty database with a believable library and history so
// the UI can be explored (and screenshotted) without any media.
func (s *Server) seedDemo(ctx context.Context) error {
	var n int
	if err := s.store.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM subtitles`).Scan(&n); err != nil || n > 0 {
		return err
	}
	rng := rand.New(rand.NewSource(7))
	type show struct {
		title   string
		seasons int
		eps     int
		release string
	}
	shows := []show{
		{"Dark (2017)", 3, 8, "1080p.NF.WEB-DL.DDP5.1.x264"},
		{"Severance", 2, 9, "2160p.ATVP.WEB-DL.DDP5.1.HDR.H.265"},
		{"Slow Horses", 4, 6, "1080p.ATVP.WEB-DL.DDP5.1.H.264"},
		{"The Expanse", 2, 10, "1080p.BluRay.x264"},
		{"Shōgun (2024)", 1, 10, "1080p.DSNP.WEB-DL.DDP5.1.H.264"},
	}
	movies := []string{"Heat (1995)", "Arrival (2016)", "Blade Runner 2049 (2017)", "Dune Part Two (2024)", "Past Lives (2023)", "The Zone of Interest (2023)", "Perfect Days (2023)", "Anatomy of a Fall (2023)"}
	langs := []string{"en", "es", "de"}
	var entries []store.LibraryEntry
	for _, sh := range shows {
		for se := 1; se <= sh.seasons; se++ {
			for ep := 1; ep <= sh.eps; ep++ {
				stem := fmt.Sprintf("%s.S%02dE%02d.%s", dotted(sh.title), se, ep, sh.release)
				dir := fmt.Sprintf("/media/shows/%s/Season %02d/", sh.title, se)
				for i, l := range langs {
					if i > 0 && rng.Intn(3) == 0 {
						continue
					}
					s, e := se, ep
					entries = append(entries, store.LibraryEntry{Path: dir + stem + "." + l + ".srt", VideoPath: dir + stem + ".mkv",
						Rel: "shows/" + sh.title + fmt.Sprintf("/Season %02d/", se) + stem + "." + l + ".srt", Library: "shows", Title: sh.title,
						Kind: "episode", Season: &s, Episode: &e, Label: fmt.Sprintf("S%02dE%02d", se, ep), Lang: l})
				}
			}
		}
	}
	for _, m := range movies {
		stem := dotted(m) + ".1080p.BluRay.DDP5.1.x264"
		for i, l := range langs {
			if i > 1 && rng.Intn(2) == 0 {
				continue
			}
			entries = append(entries, store.LibraryEntry{Path: "/media/movies/" + m + "/" + stem + "." + l + ".srt", VideoPath: "/media/movies/" + m + "/" + stem + ".mkv",
				Rel: "movies/" + m + "/" + stem + "." + l + ".srt", Library: "movies", Title: m, Kind: "movie", Label: m, Lang: l})
		}
	}
	if _, err := s.store.SyncLibrary(ctx, entries); err != nil {
		return err
	}
	now := time.Now()
	methods := []string{"alass_embedded_track", "alass_embedded_track", "audio_piecewise"}
	sources := []string{"bazarr", "sweep", "sweep", "sweep", "bazarr", "manual"}
	for i, e := range entries {
		sub, err := s.store.GetSubtitleByPath(ctx, e.Path)
		if err != nil {
			return err
		}
		runsFor := 1 + rng.Intn(2)
		for k := 0; k < runsFor; k++ {
			at := now.Add(-time.Duration(rng.Intn(14*24*60)) * time.Minute)
			dec, reason := "already_in_sync", ""
			var shift *float64
			roll := rng.Intn(100)
			switch {
			case roll < 38:
				dec = "synced"
				v := float64(rng.Intn(1200)) / 100
				shift = &v
			case roll < 93:
				v := 0.3
				shift = &v
			case roll < 96:
				dec, reason = "left_unchanged", "whole-file alignment found no trustworthy offset"
			case roll < 98:
				dec, reason = "rejected_wrong_content", "cues cover only 31% of the detected speech (this video's embedded track covers 82%) - the text belongs to a different episode/movie"
			default:
				dec, reason = "rejected_machine_translation", "no translator credit, 27% fragment cues, avg line 22 chars, no dialogue dashes - reads like a machine translation"
			}
			fit := 0.5 + rng.Float64()*0.45
			ref := fit + rng.Float64()*0.05
			details, _ := json.Marshal(map[string]any{"decision": dec, "method": methods[rng.Intn(3)], "max_shift": shift, "content_fit": fit, "ref_fit": ref, "lead_out": 0.3,
				"segments": []map[string]any{{"cues": 120, "offset": 1.2}, {"cues": 210, "offset": 1.25}, {"cues": 160, "offset": 6.8}}})
			run := &store.Run{SubtitleID: &sub.ID, Path: sub.Path, Name: filepath.Base(sub.Path), Title: sub.Title, Label: sub.Label, Lang: sub.Lang,
				Source: sources[rng.Intn(len(sources))], Decision: dec, Method: methods[rng.Intn(3)], MaxShift: shift, ContentFit: &fit, RefFit: &ref,
				Reason: reason, DurationMS: int64(8000 + rng.Intn(240000)), Details: details, CreatedAt: store.TimeString(at)}
			if err := s.store.CreateRun(ctx, run); err != nil {
				return err
			}
			if k == runsFor-1 {
				_ = s.store.SetVerdict(ctx, sub.Path, store.Verdict{Decision: dec, Reason: reason, Method: run.Method, Shift: shift, RunID: run.ID, CheckedAt: run.CreatedAt})
			}
		}
		if i%37 == 5 {
			_ = s.store.SetPinned(ctx, sub.Path, true, "nudged +250 ms")
			_ = s.store.AddNudgeMS(ctx, sub.Path, 250)
			_ = s.store.CreateNudge(ctx, &store.Nudge{Path: sub.Path, MS: 250, Cues: 612, CreatedAt: store.TimeString(now.Add(-time.Duration(i) * time.Hour))})
		}
	}
	// a little queue so the overview has something to show
	for i, e := range entries[len(entries)-9:] {
		state := "queued"
		if i == 0 {
			state = "running"
		}
		_, _ = s.store.DB.ExecContext(ctx, `INSERT INTO jobs (path, video_path, title, label, lang, source, priority, state, stage, started_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.Path, e.VideoPath, e.Title, e.Label, e.Lang, []string{"bazarr", "sweep"}[min(i, 1)], 10-i, state, map[bool]string{true: "measuring act 3 of 5", false: ""}[i == 0], store.TimeString(now.Add(-95*time.Second)))
	}
	t, _ := s.store.CreateTask(ctx, "sweep", "Library sweep", map[string]string{"trigger": "schedule"}, nil)
	s.store.TaskLog(ctx, t.ID, "%d subtitle files (0 new)", len(entries))
	s.store.TaskLog(ctx, t.ID, "38 to check, %d unchanged since their last verdict, 6 pinned, 0 without a video", len(entries)-44)
	s.store.TaskLog(ctx, t.ID, "finished: 38 processed in 41 min — adjusted 12, in sync 25, gave up 1")
	_ = s.store.FinishTask(ctx, t.ID, "done", map[string]any{"processed": 38, "decisions": map[string]int{"synced": 12, "already_in_sync": 25, "left_unchanged": 1}, "minutes": 41})
	_ = s.store.SetSetting(ctx, "lead_ms", "300")
	s.log.Info("demo data seeded", "files", len(entries))
	return nil
}

func dotted(s string) string {
	out := []rune{}
	for _, r := range s {
		switch r {
		case ' ':
			out = append(out, '.')
		case '(', ')', ':':
		default:
			out = append(out, r)
		}
	}
	return string(out)
}
