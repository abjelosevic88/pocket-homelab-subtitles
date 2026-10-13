// Package worker owns the queue: it runs the engine on one subtitle at a time
// (or WORKERS at a time), records every verdict, acts on rejections, refreshes
// Jellyfin, runs the nightly sweep and the season tools.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/bazarr"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/config"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/engine"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/jellyfin"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/library"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/store"
)

// Priorities: a person asking beats a fresh download beats the nightly sweep.
const (
	PrioritySweep  = 0
	PrioritySeason = 15
	PriorityBazarr = 10
	PriorityManual = 20
)

// Worker runs jobs and tasks.
type Worker struct {
	cfg *config.Config
	st  *store.Store
	eng *engine.Engine
	lib *library.Library
	log *slog.Logger

	slots chan struct{} // engine slots shared by syncs and verifications
	wake  chan struct{}

	mu      sync.Mutex
	running map[int64]*active
	tasks   map[int64]context.CancelFunc

	scanMu   sync.Mutex
	lastScan time.Time
	sweepMu  sync.Mutex
	seasonMu sync.Mutex
	ctx      context.Context
}

type active struct {
	job        store.Job
	stage      string
	started    time.Time
	cancel     context.CancelFunc
	userCancel bool
}

// New creates a worker.
func New(cfg *config.Config, st *store.Store, eng *engine.Engine, lib *library.Library, log *slog.Logger) *Worker {
	return &Worker{cfg: cfg, st: st, eng: eng, lib: lib, log: log,
		slots: make(chan struct{}, cfg.Workers), wake: make(chan struct{}, cfg.Workers),
		running: map[int64]*active{}, tasks: map[int64]context.CancelFunc{}, ctx: context.Background()}
}

// Library exposes the library (paths, names).
func (w *Worker) Library() *library.Library { return w.lib }

// Engine exposes the engine (doctor).
func (w *Worker) Engine() *engine.Engine { return w.eng }

// Start launches the queue loops, the library scanner and the sweep scheduler.
func (w *Worker) Start(ctx context.Context) {
	w.ctx = ctx
	if w.cfg.DemoData {
		// demo: nothing on disk to sync — keep the seeded queue as it is
		return
	}
	if n, err := w.st.RequeueRunning(ctx); err == nil && n > 0 {
		w.log.Info("requeued jobs interrupted by a restart", "jobs", n)
	}
	_ = w.st.FailRunningTasks(ctx)
	for i := 0; i < w.cfg.Workers; i++ {
		go w.loop(ctx)
	}
	go w.scanner(ctx)
	go w.scheduler(ctx)
}

// Wake nudges idle loops after something was queued.
func (w *Worker) Wake() {
	for i := 0; i < cap(w.wake); i++ {
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
}

func (w *Worker) acquire(ctx context.Context) bool {
	select {
	case w.slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (w *Worker) release() { <-w.slots }

func (w *Worker) loop(ctx context.Context) {
	for {
		if !w.acquire(ctx) {
			return
		}
		job, err := w.st.ClaimJob(ctx)
		if err != nil || job == nil {
			w.release()
			if err != nil && ctx.Err() == nil {
				w.log.Error("claim job", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-w.wake:
			case <-time.After(20 * time.Second):
			}
			continue
		}
		w.process(ctx, job)
		w.release()
	}
}

// Running describes the jobs being synced right now.
type Running struct {
	store.Job
	Stage   string `json:"stage"`
	Elapsed int64  `json:"elapsed_s"`
}

// RunningJobs returns what the engine is working on.
func (w *Worker) RunningJobs() []Running {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := []Running{}
	for _, a := range w.running {
		out = append(out, Running{Job: a.job, Stage: a.stage, Elapsed: int64(time.Since(a.started).Seconds())})
	}
	if w.cfg.DemoData && len(out) == 0 {
		jobs, _, _ := w.st.ListJobs(context.Background(), store.JobFilter{States: []string{"running"}, Limit: 5})
		for _, j := range jobs {
			out = append(out, Running{Job: j, Stage: j.Stage, Elapsed: 95})
		}
	}
	return out
}

// Cancel stops a running job or drops a queued one.
func (w *Worker) Cancel(ctx context.Context, id int64) error {
	w.mu.Lock()
	a := w.running[id]
	if a != nil {
		a.userCancel = true
		a.cancel()
	}
	w.mu.Unlock()
	if a != nil {
		return nil
	}
	n, err := w.st.CancelQueued(ctx, id, 0, "")
	if err == nil && n == 0 {
		return errors.New("job is not queued or running")
	}
	return err
}

// ---- enqueue ----

// Request asks for one subtitle to be synced.
type Request struct {
	Path     string
	Video    string
	Source   string // bazarr | manual | sweep | season | api
	Priority int
	Extras   map[string]string
	Force    bool
	TaskID   *int64
}

// ErrOutsideLibrary is returned for paths outside MEDIA_DIRS.
var ErrOutsideLibrary = errors.New("not a subtitle inside the media library")

// Enqueue validates and queues one subtitle.
func (w *Worker) Enqueue(ctx context.Context, r Request) (store.EnqueueResult, error) {
	p := filepath.Clean(r.Path)
	if !strings.EqualFold(filepath.Ext(p), ".srt") || !w.lib.Contains(p) {
		return store.EnqueueResult{}, ErrOutsideLibrary
	}
	if _, err := os.Stat(p); err != nil {
		return store.EnqueueResult{}, fmt.Errorf("file not found: %s", p)
	}
	video := r.Video
	if video == "" || !exists(video) {
		video = library.FindVideo(p)
	}
	d := w.lib.Describe(p, video)
	if _, err := w.st.UpsertSubtitle(ctx, d); err != nil {
		return store.EnqueueResult{}, err
	}
	res, err := w.st.Enqueue(ctx, &store.Job{Path: p, VideoPath: video, Title: d.Title, Label: d.Label, Lang: d.Lang,
		Source: r.Source, Priority: r.Priority, Extras: r.Extras, Force: r.Force, TaskID: r.TaskID})
	if err == nil {
		w.Wake()
	}
	return res, err
}

// ---- processing ----

func exists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

func (w *Worker) setStage(id int64, stage string) {
	w.mu.Lock()
	if a := w.running[id]; a != nil {
		a.stage = stage
	}
	w.mu.Unlock()
	_ = w.st.SetJobStage(context.Background(), id, stage)
}

func (w *Worker) bazarrClient(st store.Settings) *bazarr.Client {
	return bazarr.New(st.BazarrURL, st.BazarrAPIKey, st.BazarrPathMap)
}

func (w *Worker) jellyfinClient(st store.Settings) *jellyfin.Client {
	return jellyfin.New(st.JellyfinURL, st.JellyfinAPIKey, st.JellyfinPathMap)
}

// BackupPath is where the first original of a library file is kept.
func (w *Worker) BackupPath(sub *store.Subtitle) string {
	if sub.Rel == "" {
		return filepath.Join(w.cfg.BackupDir, "loose", filepath.Base(sub.Path))
	}
	return filepath.Join(w.cfg.BackupDir, sub.Rel)
}

func (w *Worker) process(ctx context.Context, job *store.Job) {
	started := time.Now()
	jctx, cancel := context.WithTimeout(ctx, w.cfg.JobTimeout)
	a := &active{job: *job, stage: "starting", started: started, cancel: cancel}
	w.mu.Lock()
	w.running[job.ID] = a
	w.mu.Unlock()
	defer func() {
		cancel()
		w.mu.Lock()
		delete(w.running, job.ID)
		w.mu.Unlock()
	}()

	st, _ := w.st.GetSettings(ctx)
	finish := func(run *store.Run, state, errMsg string) {
		var runID *int64
		if run != nil {
			if err := w.st.CreateRun(ctx, run); err != nil {
				w.log.Error("record run", "err", err)
			} else {
				runID = &run.ID
			}
		}
		_ = w.st.FinishJob(ctx, job.ID, state, runID, errMsg)
	}
	newRun := func(sub *store.Subtitle, decision, reason string) *store.Run {
		r := &store.Run{Path: job.Path, Name: filepath.Base(job.Path), Title: job.Title, Label: job.Label, Lang: job.Lang,
			Source: job.Source, Decision: decision, Reason: reason, JobID: &job.ID, DurationMS: time.Since(started).Milliseconds()}
		if sub != nil {
			r.SubtitleID, r.Title, r.Label, r.Lang = &sub.ID, sub.Title, sub.Label, sub.Lang
		}
		return r
	}

	if !exists(job.Path) {
		_ = w.st.MarkMissing(ctx, job.Path)
		finish(newRun(nil, "skipped", "the file was gone before its turn (deleted or renamed)"), "done", "")
		return
	}
	video := job.VideoPath
	if !exists(video) {
		video = library.FindVideo(job.Path)
	}
	sub, err := w.st.UpsertSubtitle(ctx, w.lib.Describe(job.Path, video))
	if err != nil {
		finish(nil, "failed", err.Error())
		return
	}
	if video == "" {
		finish(newRun(sub, "skipped", "no video next to this subtitle (it must start with the video's file name)"), "done", "")
		return
	}

	extras := bazarr.Extras{Language: job.Extras["language"], Provider: job.Extras["provider"], SubtitleID: job.Extras["subtitle_id"],
		SeriesID: job.Extras["series_id"], EpisodeID: job.Extras["episode_id"]}
	fresh := job.Source == "bazarr"
	if fresh && sub.Pinned && st.AutoUnpinNew {
		// The pin protected the timing of the PREVIOUS file. A new download is
		// a different subtitle; keeping the pin would leave it unsynced forever.
		_ = w.st.SetPinned(ctx, sub.Path, false, "")
		w.st.AddEvent(ctx, sub.Path, "unpin", "replaced by a new download from "+orDash(extras.Provider), nil)
		sub.Pinned = false
	}
	lang := sub.Lang
	if lang == "" {
		lang = strings.ToLower(extras.Language)
	}
	opts := engine.SyncOptions{
		BackupPath: w.BackupPath(sub), LeadMS: st.LeadMS, CentreRef: st.CentreRef, Polish: st.Polish, RefResidual: st.RefResidual,
		Latinize: store.LangIn(lang, st.LatinizeLangs), MTGate: fresh && store.LangIn(lang, st.MTGateLangs),
		ContentGate: st.ContentGate, Pinned: sub.Pinned,
	}
	res, err := w.eng.Sync(jctx, video, job.Path, opts, func(s string) { w.setStage(job.ID, s) })
	if err != nil {
		w.mu.Lock()
		userCancel := a.userCancel
		w.mu.Unlock()
		switch {
		case userCancel:
			finish(nil, "cancelled", "cancelled")
		case ctx.Err() != nil:
			// shutting down: put it back for the next start
			_, _ = w.st.DB.ExecContext(context.Background(), `UPDATE jobs SET state = 'queued', stage = '', started_at = NULL WHERE id = ?`, job.ID)
		case errors.Is(err, engine.ErrTimeout):
			r := newRun(sub, "timeout", fmt.Sprintf("gave up after %s (JOB_TIMEOUT)", w.cfg.JobTimeout))
			finish(r, "failed", err.Error())
			w.afterRun(ctx, st, sub, r)
		default:
			r := newRun(sub, "error", err.Error())
			finish(r, "failed", err.Error())
			w.afterRun(ctx, st, sub, r)
		}
		return
	}

	details := map[string]any{}
	_ = json.Unmarshal(res.Raw, &details)
	run := newRun(sub, res.Decision, res.Reason)
	run.Method, run.MaxShift, run.ContentFit, run.RefFit = res.Method, res.MaxShift, res.ContentFit, res.RefFit
	if res.Decision == "pinned" {
		run.Reason = "pinned — timing left exactly as it is"
	}
	removed := false
	if strings.HasPrefix(res.Decision, "rejected_") {
		outcome, gone := w.reject(ctx, st, sub, extras)
		details["outcome"] = outcome.Text
		if outcome.Copy != "" {
			details["quarantine"] = outcome.Copy
		}
		removed = gone
	}
	if (res.Changed || res.Latinized) && !removed {
		if jf := w.jellyfinClient(st); jf != nil {
			if err := jf.Refresh(ctx, job.Path); err != nil {
				details["jellyfin"] = err.Error()
				w.log.Warn("jellyfin refresh", "path", job.Path, "err", err)
			} else {
				details["jellyfin"] = "refreshed"
			}
		}
	}
	run.Details, _ = json.Marshal(details)
	run.DurationMS = time.Since(started).Milliseconds()
	finish(run, "done", "")
	if removed {
		_ = w.st.MarkMissing(ctx, job.Path)
	}
	w.afterRun(ctx, st, sub, run)
}

// afterRun writes the verdict onto the file's row and sends notifications.
func (w *Worker) afterRun(ctx context.Context, st store.Settings, sub *store.Subtitle, run *store.Run) {
	if run.Decision != "pinned" {
		v := store.Verdict{Decision: run.Decision, Reason: run.Reason, Method: run.Method, Shift: run.MaxShift, RunID: run.ID, CheckedAt: run.CreatedAt}
		v.SubSize, v.SubMtime = sig(sub.Path)
		v.VidSize, v.VidMtime = sig(sub.VideoPath)
		_ = w.st.SetVerdict(ctx, sub.Path, v)
	}
	if run.Source == "sweep" {
		return // the sweep sends one summary
	}
	name := sub.Title
	if sub.Kind == "episode" {
		name += " " + sub.Label
	}
	if sub.Lang != "" {
		name += " [" + strings.ToUpper(sub.Lang) + "]"
	}
	switch {
	case store.IsAttention(run.Decision) && st.NotifyFailures:
		title := map[string]string{"left_unchanged": "Subtitle could not be synced", "error": "Subtitle sync error", "timeout": "Subtitle sync timed out",
			"rejected_wrong_content": "Subtitle rejected: wrong content", "rejected_machine_translation": "Subtitle rejected: machine translation"}[run.Decision]
		w.notify(ctx, st, title, name+"\n"+run.Reason, notify.Warning)
	case run.Decision == "synced" && st.NotifySynced:
		w.notify(ctx, st, "Subtitle synced", fmt.Sprintf("%s\n%s, moved up to %.2fs", name, methodLabel(run.Method), deref(run.MaxShift)), notify.Success)
	}
}

func (w *Worker) notify(ctx context.Context, st store.Settings, title, body string, level notify.Level) {
	if st.NotifyKind == "" || st.NotifyURL == "" {
		return
	}
	go func() {
		c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := notify.Send(c, st.NotifyKind, st.NotifyURL, title, body, level); err != nil {
			w.log.Warn("notification failed", "err", err)
		}
	}()
}

// Outcome describes what happened to a rejected file.
type Outcome struct {
	Text string
	Copy string
}

// reject acts on a rejected download according to Settings → on_reject.
func (w *Worker) reject(ctx context.Context, st store.Settings, sub *store.Subtitle, extras bazarr.Extras) (Outcome, bool) {
	var out Outcome
	if st.OnReject != "keep" {
		if err := os.MkdirAll(w.cfg.RejectDir, 0o755); err == nil {
			dst := filepath.Join(w.cfg.RejectDir, fmt.Sprintf("%s.%d", filepath.Base(sub.Path), time.Now().Unix()))
			if err := copyFile(sub.Path, dst); err == nil {
				out.Copy = dst
			} else {
				w.log.Error("quarantine copy", "err", err)
				return Outcome{Text: "could not keep a copy (" + err.Error() + "), so the file was left in place"}, false
			}
		}
	}
	switch st.OnReject {
	case "keep":
		out.Text = "left in place (Settings → on rejection: keep)"
		return out, false
	case "quarantine":
		if err := os.Remove(sub.Path); err != nil {
			out.Text = "copy kept, but removing the file failed: " + err.Error()
			return out, false
		}
		out.Text = "moved out of the library; search for another one in Bazarr"
		w.st.AddEvent(ctx, sub.Path, "quarantine", out.Copy, nil)
		return out, true
	}
	// blacklist: ban this upload so Bazarr fetches another one
	if bz := w.bazarrClient(st); bz != nil && extras.Valid() {
		if err := bz.Blacklist(ctx, extras, sub.Path); err != nil {
			out.Text = "blacklist request sent (" + err.Error() + "); the replacement search may still be running"
		} else {
			out.Text = "blacklisted in Bazarr; a replacement search was started"
		}
	}
	if exists(sub.Path) {
		if err := os.Remove(sub.Path); err != nil {
			out.Text = strings.TrimSpace(out.Text + " — removing the file failed: " + err.Error())
			return out, false
		}
		if out.Text == "" {
			out.Text = "removed from the library (a copy is kept); search for another one in Bazarr"
		}
	}
	w.st.AddEvent(ctx, sub.Path, "reject", out.Text, nil)
	return out, true
}

// sig returns a file's size and mtime (rounded like the ledger stores it).
func sig(p string) (*int64, *float64) {
	fi, err := os.Stat(p)
	if err != nil {
		return nil, nil
	}
	size := fi.Size()
	mt := math.Round(float64(fi.ModTime().UnixNano())/1e7) / 100
	return &size, &mt
}

// unchanged reports whether a file still has the signature of its last verdict.
func unchanged(p string, size *int64, mtime *float64) bool {
	s, m := sig(p)
	if s == nil || size == nil || mtime == nil {
		return false
	}
	return *s == *size && math.Abs(*m-*mtime) < 0.006
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	_ = os.Chtimes(tmp, fi.ModTime(), fi.ModTime())
	return os.Rename(tmp, dst)
}

func deref(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

func orDash(s string) string {
	if s == "" || s == "None" {
		return "a provider"
	}
	return s
}

func methodLabel(m string) string {
	switch m {
	case "alass_embedded_track":
		return "aligned to the embedded track"
	case "audio_piecewise":
		return "aligned to the audio"
	}
	return m
}
