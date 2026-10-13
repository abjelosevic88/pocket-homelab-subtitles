package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/bazarr"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/store"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/worker"
)

// missingDownloads returns Bazarr's wanted totals, cached for a minute so a
// page refresh every few seconds does not hammer it. ok=false: not configured
// or unreachable.
func (s *Server) missingDownloads(ctx context.Context) (episodes, movies int, ok bool) {
	s.wantedMu.Lock()
	defer s.wantedMu.Unlock()
	if time.Since(s.wanted.at) < time.Minute {
		return s.wanted.episodes, s.wanted.movies, s.wanted.ok
	}
	st, _ := s.store.GetSettings(ctx)
	c := bazarr.New(st.BazarrURL, st.BazarrAPIKey, st.BazarrPathMap)
	s.wanted = wantedCache{at: time.Now()}
	if c == nil {
		return 0, 0, false
	}
	cctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	if _, e, m, err := c.Wanted(cctx, 1); err == nil {
		s.wanted.episodes, s.wanted.movies, s.wanted.ok = e, m, true
	}
	return s.wanted.episodes, s.wanted.movies, s.wanted.ok
}

type overviewStats struct {
	store.RunStats
	store.LibraryStats
	Queued   int  `json:"queued"`
	Running  int  `json:"running"`
	Missing  *int `json:"missing_downloads"`
	Episodes int  `json:"missing_episodes"`
	Movies   int  `json:"missing_movies"`
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var out struct {
		Stats    overviewStats    `json:"stats"`
		Running  []worker.Running `json:"running"`
		Next     []store.Job      `json:"next"`
		Queued   int              `json:"queued_total"`
		Recent   []store.Run      `json:"recent"`
		Days     []store.DayCount `json:"days"`
		Sweep    map[string]any   `json:"sweep"`
		LastScan *time.Time       `json:"last_scan"`
	}
	var err error
	if out.Stats.RunStats, err = s.store.GetRunStats(ctx); err != nil {
		s.fail(w, err, "stats")
		return
	}
	out.Stats.LibraryStats, _ = s.store.GetLibraryStats(ctx)
	out.Stats.Queued, out.Stats.Running, _ = s.store.QueueCounts(ctx)
	if e, m, ok := s.missingDownloads(ctx); ok {
		n := e + m
		out.Stats.Missing, out.Stats.Episodes, out.Stats.Movies = &n, e, m
	}
	out.Running = s.w.RunningJobs()
	out.Next, out.Queued, _ = s.store.ListJobs(ctx, store.JobFilter{States: []string{"queued"}, Limit: 25})
	out.Recent, _, _ = s.store.ListRuns(ctx, store.RunFilter{Limit: 8})
	out.Days, _ = s.store.RunsPerDay(ctx, 14)
	st, _ := s.store.GetSettings(ctx)
	sweep := map[string]any{"enabled": st.SweepEnabled, "time": st.SweepTime, "budget": st.SweepBudget, "running": s.w.SweepRunning()}
	if next := worker.NextSweep(st); !next.IsZero() {
		sweep["next_at"] = next
	}
	if ts, _ := s.store.ListTasks(ctx, "sweep", 1); len(ts) > 0 {
		sweep["last"] = ts[0]
	}
	out.Sweep = sweep
	if t := s.w.LastScan(); !t.IsZero() {
		out.LastScan = &t
	}
	writeJSON(w, http.StatusOK, out)
}

// handleWidget is a compact summary for Homepage / Glance / Raycast.
func (s *Server) handleWidget(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.widgetPayload(r.Context()))
}

func (s *Server) widgetPayload(ctx context.Context) map[string]any {
	runs, _ := s.store.GetRunStats(ctx)
	lib, _ := s.store.GetLibraryStats(ctx)
	queued, _, _ := s.store.QueueCounts(ctx)
	running := s.w.RunningJobs()
	items := []map[string]string{}
	now := "—"
	for _, j := range running {
		name := jobName(j.Job)
		if now == "—" {
			now = name
		}
		items = append(items, map[string]string{"name": name, "detail": "syncing · " + elapsed(j.Elapsed)})
	}
	next, _, _ := s.store.ListJobs(ctx, store.JobFilter{States: []string{"queued"}, Limit: 8})
	sweepN := 0
	for i, j := range next {
		if j.Source == "sweep" {
			sweepN++
			continue
		}
		items = append(items, map[string]string{"name": jobName(j), "detail": "waiting · #" + strconv.Itoa(i+1)})
	}
	if queued > 0 && sweepN > 0 {
		items = append(items, map[string]string{"name": "Library sweep backlog", "detail": strconv.Itoa(queued) + " left"})
	}
	if len(items) == 0 {
		items = append(items, map[string]string{"name": "Nothing in the queue", "detail": ""})
	}
	status := "idle"
	if len(running) > 0 || queued > 0 {
		status = "syncing"
		if now == "—" {
			now = "starting…"
		}
	}
	var missing any = "—"
	if e, m, ok := s.missingDownloads(ctx); ok {
		missing = e + m
	}
	return map[string]any{"status": status, "now": now, "queued": queued, "missing": missing, "items": items,
		"processed": runs.Processed, "adjusted": runs.Adjusted, "today": runs.Today, "attention": lib.Attention, "pinned": lib.Pinned, "files": lib.Files}
}

func jobName(j store.Job) string {
	n := j.Title
	if j.Label != "" && j.Label != j.Title {
		n += " " + j.Label
	}
	if n == "" {
		n = j.Path
		if i := strings.LastIndex(n, "/"); i >= 0 {
			n = n[i+1:]
		}
	}
	return n
}

func elapsed(sec int64) string {
	m := sec / 60
	switch {
	case m < 1:
		return "<1 min"
	case m < 60:
		return strconv.FormatInt(m, 10) + " min"
	}
	return strconv.FormatInt(m/60, 10) + "h " + strconv.FormatInt(m%60, 10) + "m"
}

// ---- queue ----

func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit")
	if limit <= 0 {
		limit = 200
	}
	jobs, total, err := s.store.ListJobs(r.Context(), store.JobFilter{States: []string{"queued"}, Limit: limit})
	if err != nil {
		s.fail(w, err, "queue")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"running": s.w.RunningJobs(), "queued": jobs, "total": total})
}

func (s *Server) handleQueueCancel(w http.ResponseWriter, r *http.Request) {
	if err := s.w.Cancel(r.Context(), idParam(r, "id")); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleQueueBump(w http.ResponseWriter, r *http.Request) {
	if err := s.store.BumpJob(r.Context(), idParam(r, "id")); err != nil {
		s.fail(w, err, "bump")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleQueueClear(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.CancelQueued(r.Context(), 0, 0, r.URL.Query().Get("source"))
	if err != nil {
		s.fail(w, err, "clear queue")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"cancelled": n})
}

// ---- history ----

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	runs, total, err := s.store.ListRuns(r.Context(), store.RunFilter{Q: q.Get("q"), Outcome: q.Get("outcome"), Decision: q.Get("decision"),
		Source: q.Get("source"), Lang: q.Get("lang"), SubtitleID: int64(queryInt(r, "file")), Limit: queryInt(r, "limit"), Offset: queryInt(r, "offset")})
	if err != nil {
		s.fail(w, err, "runs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": runs, "total": total})
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.store.GetRun(r.Context(), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "run")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleRestoreRejected(w http.ResponseWriter, r *http.Request) {
	p, err := s.w.RestoreRejected(r.Context(), idParam(r, "id"), userIDPtr(r))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	sub, _ := s.store.GetSubtitleByPath(r.Context(), p)
	writeJSON(w, http.StatusOK, sub)
}

// ---- library ----

func libFilter(r *http.Request) store.SubtitleFilter {
	q := r.URL.Query()
	f := store.SubtitleFilter{Q: q.Get("q"), Title: q.Get("title"), Lang: q.Get("lang"), Status: q.Get("status"), Library: q.Get("library"),
		Limit: queryInt(r, "limit"), Offset: queryInt(r, "offset")}
	if v := q.Get("season"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Season = &n
		}
	}
	return f
}

func (s *Server) handleTitles(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.ListTitles(r.Context(), libFilter(r))
	if err != nil {
		s.fail(w, err, "titles")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	f := libFilter(r)
	if f.Limit == 0 {
		f.Limit = 500
	}
	items, total, err := s.store.ListSubtitles(r.Context(), f)
	if err != nil {
		s.fail(w, err, "files")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) handleLangs(w http.ResponseWriter, r *http.Request) {
	l, err := s.store.Languages(r.Context())
	if err != nil {
		s.fail(w, err, "languages")
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	res, err := s.w.Scan(r.Context())
	if err != nil {
		s.fail(w, err, "scan")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ---- one file ----

type fileDetail struct {
	*store.Subtitle
	Runs        []store.Run   `json:"runs"`
	Events      []store.Event `json:"events"`
	HasOriginal bool          `json:"has_original"`
	CanUndo     bool          `json:"can_undo"`
	Queued      *store.Job    `json:"queued"`
	Exists      bool          `json:"exists"`
}

func (s *Server) fileDetail(ctx context.Context, sub *store.Subtitle) fileDetail {
	d := fileDetail{Subtitle: sub}
	d.Runs, _, _ = s.store.ListRuns(ctx, store.RunFilter{Path: sub.Path, Limit: 50})
	d.Events, _ = s.store.ListEvents(ctx, sub.Path, 100)
	d.HasOriginal = s.w.OriginalPath(sub) != ""
	if n, err := s.store.LastNudge(ctx, sub.Path); err == nil && n != nil {
		_, statErr := os.Stat(n.Backup)
		d.CanUndo = statErr == nil
	}
	if jobs, _, _ := s.store.ListJobs(ctx, store.JobFilter{States: []string{"queued", "running"}, Limit: 500}); len(jobs) > 0 {
		for i := range jobs {
			if jobs[i].Path == sub.Path {
				d.Queued = &jobs[i]
				break
			}
		}
	}
	_, err := os.Stat(sub.Path)
	d.Exists = err == nil || s.cfg.DemoData
	return d
}

func (s *Server) loadFile(w http.ResponseWriter, r *http.Request) *store.Subtitle {
	sub, err := s.store.GetSubtitle(r.Context(), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "file")
		return nil
	}
	return sub
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	if sub := s.loadFile(w, r); sub != nil {
		writeJSON(w, http.StatusOK, s.fileDetail(r.Context(), sub))
	}
}

func (s *Server) handleFileByPath(w http.ResponseWriter, r *http.Request) {
	sub, err := s.store.GetSubtitleByPath(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, err, "file")
		return
	}
	writeJSON(w, http.StatusOK, s.fileDetail(r.Context(), sub))
}

// handleFileText returns the subtitle text (first 200 KB) for the preview.
func (s *Server) handleFileText(w http.ResponseWriter, r *http.Request) {
	sub := s.loadFile(w, r)
	if sub == nil {
		return
	}
	f, err := os.Open(sub.Path)
	if err != nil {
		writeErr(w, http.StatusNotFound, "file is gone")
		return
	}
	defer f.Close()
	buf := make([]byte, 200<<10)
	n, _ := f.Read(buf)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(buf[:n])
}

func (s *Server) handleFileSync(w http.ResponseWriter, r *http.Request) {
	sub := s.loadFile(w, r)
	if sub == nil {
		return
	}
	var in struct {
		Unpin bool `json:"unpin"`
	}
	_ = decode(r, &in)
	if sub.Pinned {
		if !in.Unpin {
			writeErr(w, http.StatusConflict, "this file is pinned — unpin it to let the sync change its timing")
			return
		}
		if _, err := s.w.Unpin(r.Context(), sub.Path, userIDPtr(r)); err != nil {
			s.fail(w, err, "unpin")
			return
		}
	}
	res, err := s.w.Enqueue(r.Context(), worker.Request{Path: sub.Path, Video: sub.VideoPath, Source: "manual", Priority: worker.PriorityManual, Force: true})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, res.Job)
}

func (s *Server) handleFileNudge(w http.ResponseWriter, r *http.Request) {
	sub := s.loadFile(w, r)
	if sub == nil {
		return
	}
	var in struct {
		MS int `json:"ms"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	res, err := s.w.Nudge(r.Context(), sub.Path, in.MS, userIDPtr(r))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleFileUndo(w http.ResponseWriter, r *http.Request) {
	sub := s.loadFile(w, r)
	if sub == nil {
		return
	}
	res, err := s.w.UndoNudge(r.Context(), sub.Path, userIDPtr(r))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleFilePin(w http.ResponseWriter, r *http.Request) {
	sub := s.loadFile(w, r)
	if sub == nil {
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	_ = decode(r, &in)
	if err := s.w.Pin(r.Context(), sub.Path, in.Note, userIDPtr(r)); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleFileUnpin(w http.ResponseWriter, r *http.Request) {
	sub := s.loadFile(w, r)
	if sub == nil {
		return
	}
	was, err := s.w.Unpin(r.Context(), sub.Path, userIDPtr(r))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true, "was_pinned": was})
}

func (s *Server) handleFileRestore(w http.ResponseWriter, r *http.Request) {
	sub := s.loadFile(w, r)
	if sub == nil {
		return
	}
	if err := s.w.RestoreOriginal(r.Context(), sub.Path, userIDPtr(r)); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleFileVerify(w http.ResponseWriter, r *http.Request) {
	sub := s.loadFile(w, r)
	if sub == nil {
		return
	}
	t, err := s.w.StartVerify(context.WithoutCancel(r.Context()), sub.Path, userIDPtr(r))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, t)
}

func (s *Server) handleRecentNudges(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.RecentNudges(r.Context(), 30)
	if err != nil {
		s.fail(w, err, "nudges")
		return
	}
	type row struct {
		store.Nudge
		File *store.Subtitle `json:"file"`
	}
	out := []row{}
	for _, x := range n {
		f, _ := s.store.GetSubtitleByPath(r.Context(), x.Path)
		out = append(out, row{x, f})
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- queueing from outside ----

// handleSync queues one file by path (API, scripts, Shortcuts).
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path  string `json:"path"`
		Video string `json:"video"`
		Force bool   `json:"force"`
	}
	if err := decode(r, &in); err != nil || in.Path == "" {
		writeErr(w, http.StatusBadRequest, "path is required")
		return
	}
	res, err := s.w.Enqueue(r.Context(), worker.Request{Path: in.Path, Video: in.Video, Source: "api", Priority: worker.PriorityManual, Force: in.Force})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, res.Job)
}

// handleBazarrHook is what Bazarr's post-processing command calls after every
// download. Form or JSON fields: video, subtitle, language, provider,
// subtitle_id, series_id, episode_id (all as Bazarr passes them).
func (s *Server) handleBazarrHook(w http.ResponseWriter, r *http.Request) {
	in := map[string]string{}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		if err := decode(r, &in); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid form")
			return
		}
		for k := range r.Form {
			in[k] = strings.TrimSpace(r.Form.Get(k))
		}
	}
	st, _ := s.store.GetSettings(r.Context())
	paths := bazarr.ParsePathMap(st.BazarrPathMap)
	sub := paths.ToOurs(in["subtitle"])
	video := paths.ToOurs(in["video"])
	if sub == "" {
		writeErr(w, http.StatusBadRequest, "subtitle is required")
		return
	}
	if !strings.EqualFold(strings.TrimSpace(sub[max(0, len(sub)-4):]), ".srt") {
		writeJSON(w, http.StatusOK, map[string]string{"skipped": "not an srt file"})
		return
	}
	extras := map[string]string{}
	for _, k := range []string{"language", "provider", "subtitle_id", "series_id", "episode_id"} {
		if v := in[k]; v != "" && v != "None" {
			extras[k] = v
		}
	}
	if st.Languages != "" {
		lang := strings.ToLower(in["language"])
		if lang != "" && !store.LangIn(lang, st.Languages) {
			writeJSON(w, http.StatusOK, map[string]string{"skipped": "language " + lang + " is not managed (Settings → Sync)"})
			return
		}
	}
	res, err := s.w.Enqueue(r.Context(), worker.Request{Path: sub, Video: video, Source: "bazarr", Priority: worker.PriorityBazarr, Extras: extras})
	if err != nil {
		s.log.Warn("bazarr hook rejected", "subtitle", sub, "err", err)
		writeErr(w, http.StatusBadRequest, err.Error()+" (is the media mounted at the same path as in Bazarr? see Settings → Integrations → path mapping)")
		return
	}
	s.log.Info("queued from bazarr", "subtitle", sub, "provider", extras["provider"], "job", res.Job.ID)
	writeJSON(w, http.StatusAccepted, res.Job)
}

// ---- tasks ----

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.ListTasks(r.Context(), r.URL.Query().Get("kind"), queryInt(r, "limit"))
	if err != nil {
		s.fail(w, err, "tasks")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	t, err := s.store.GetTask(r.Context(), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "task")
		return
	}
	jobs, _, _ := s.store.ListJobs(r.Context(), store.JobFilter{TaskID: t.ID, Limit: 2000})
	writeJSON(w, http.StatusOK, map[string]any{"task": t, "jobs": jobs})
}

func (s *Server) handleTaskCancel(w http.ResponseWriter, r *http.Request) {
	if err := s.w.CancelTask(r.Context(), idParam(r, "id")); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleSweepPlan(w http.ResponseWriter, r *http.Request) {
	st, _ := s.store.GetSettings(r.Context())
	p, err := s.w.PlanSweep(r.Context(), st)
	if err != nil {
		s.fail(w, err, "plan")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"todo": len(p.Todo), "unchanged": p.Unchanged, "pinned": p.Pinned, "no_video": p.NoVideo, "stale": p.Stale, "skipped": p.Skipped})
}

func (s *Server) handleSweepStart(w http.ResponseWriter, r *http.Request) {
	t, err := s.w.StartSweep(context.WithoutCancel(r.Context()), "manual", userIDPtr(r))
	if err != nil {
		s.fail(w, err, "sweep")
		return
	}
	writeJSON(w, http.StatusAccepted, t)
}

func (s *Server) handleSeason(w http.ResponseWriter, r *http.Request) {
	var in worker.SeasonRequest
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Mode == "reset" {
		if u := userFrom(r.Context()); u == nil || u.Role != "admin" {
			writeErr(w, http.StatusForbidden, "only an admin can reset a season")
			return
		}
	}
	t, err := s.w.StartSeason(context.WithoutCancel(r.Context()), in, userIDPtr(r))
	if err != nil {
		if errors.Is(err, worker.ErrBusy) {
			s.fail(w, err, "season")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, t)
}

// ---- missing subtitles (Bazarr's wanted list) ----

func (s *Server) handleMissing(w http.ResponseWriter, r *http.Request) {
	st, _ := s.store.GetSettings(r.Context())
	c := bazarr.New(st.BazarrURL, st.BazarrAPIKey, st.BazarrPathMap)
	if c == nil {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false, "items": []any{}})
		return
	}
	n := queryInt(r, "limit")
	if n <= 0 || n > 500 {
		n = 200
	}
	items, e, m, err := c.Wanted(r.Context(), n)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if items == nil {
		items = []bazarr.WantedItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "items": items, "episodes": e, "movies": m, "bazarr_url": st.BazarrURL})
}

func (s *Server) handleMissingSearch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind      string `json:"kind"`
		SeriesID  int64  `json:"series_id"`
		EpisodeID int64  `json:"episode_id"`
		MovieID   int64  `json:"movie_id"`
		Language  string `json:"language"`
	}
	if err := decode(r, &in); err != nil || in.Language == "" {
		writeErr(w, http.StatusBadRequest, "language is required")
		return
	}
	st, _ := s.store.GetSettings(r.Context())
	c := bazarr.New(st.BazarrURL, st.BazarrAPIKey, st.BazarrPathMap)
	if c == nil {
		writeErr(w, http.StatusBadRequest, bazarr.ErrNotConfigured.Error())
		return
	}
	var err error
	if in.Kind == "movie" {
		err = c.SearchMovie(r.Context(), in.MovieID, in.Language)
	} else {
		err = c.SearchEpisode(r.Context(), in.SeriesID, in.EpisodeID, in.Language)
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.wantedMu.Lock()
	s.wanted.at = time.Time{}
	s.wantedMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
