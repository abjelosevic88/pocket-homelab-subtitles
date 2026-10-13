package worker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/store"
)

// ScanResult summarises a library scan.
type ScanResult struct {
	Files int `json:"files"`
	Added int `json:"added"`
}

// Scan walks the library and updates the file list.
func (w *Worker) Scan(ctx context.Context) (ScanResult, error) {
	w.scanMu.Lock()
	defer w.scanMu.Unlock()
	entries, err := w.lib.Scan()
	if err != nil {
		return ScanResult{}, err
	}
	added, err := w.st.SyncLibrary(ctx, entries)
	if err != nil {
		return ScanResult{}, err
	}
	_, _ = w.st.LinkRuns(ctx)
	w.lastScan = time.Now()
	return ScanResult{Files: len(entries), Added: added}, nil
}

// LastScan is when the library was last walked.
func (w *Worker) LastScan() time.Time {
	w.scanMu.Lock()
	defer w.scanMu.Unlock()
	return w.lastScan
}

func (w *Worker) scanner(ctx context.Context) {
	for {
		if r, err := w.Scan(ctx); err != nil {
			w.log.Warn("library scan failed", "err", err)
		} else {
			w.log.Debug("library scanned", "files", r.Files, "added", r.Added)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(w.cfg.ScanInterval):
		}
	}
}

// scheduler starts the nightly sweep at Settings → sweep_time. A sweep missed
// while the app was down runs when it comes back, up to six hours late.
func (w *Worker) scheduler(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		st, err := w.st.GetSettings(ctx)
		if err != nil || !st.SweepEnabled {
			continue
		}
		due, ok := lastDue(time.Now(), st.SweepTime)
		if !ok || time.Since(due) > 6*time.Hour {
			continue
		}
		last, _ := time.Parse(time.RFC3339Nano, st.LastSweepAt)
		if !last.Before(due) {
			continue
		}
		if _, err := w.StartSweep(ctx, "schedule", nil); err != nil && !errors.Is(err, ErrBusy) {
			w.log.Warn("scheduled sweep", "err", err)
		}
	}
}

// lastDue is the most recent HH:MM at or before now.
func lastDue(now time.Time, hhmm string) (time.Time, bool) {
	t, err := time.ParseInLocation("15:04", hhmm, now.Location())
	if err != nil {
		return time.Time{}, false
	}
	due := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
	if due.After(now) {
		due = due.AddDate(0, 0, -1)
	}
	return due, true
}

// NextSweep is when the scheduler will start the next sweep (zero if off).
func NextSweep(st store.Settings) time.Time {
	if !st.SweepEnabled {
		return time.Time{}
	}
	due, ok := lastDue(time.Now(), st.SweepTime)
	if !ok {
		return time.Time{}
	}
	return due.AddDate(0, 0, 1)
}

// ErrBusy is returned when the same kind of task is already running.
var ErrBusy = errors.New("already running")

// SweepRunning reports whether a sweep is in progress.
func (w *Worker) SweepRunning() bool {
	if w.sweepMu.TryLock() {
		w.sweepMu.Unlock()
		return false
	}
	return true
}

// StartSweep begins a library sweep in the background.
func (w *Worker) StartSweep(ctx context.Context, trigger string, userID *int64) (*store.Task, error) {
	if !w.sweepMu.TryLock() {
		return nil, fmt.Errorf("a sweep is %w", ErrBusy)
	}
	task, err := w.st.CreateTask(ctx, "sweep", "Library sweep", map[string]string{"trigger": trigger}, userID)
	if err != nil {
		w.sweepMu.Unlock()
		return nil, err
	}
	_ = w.st.SetSetting(ctx, "last_sweep_at", store.Now())
	tctx, cancel := context.WithCancel(w.ctx)
	w.mu.Lock()
	w.tasks[task.ID] = cancel
	w.mu.Unlock()
	go func() {
		defer w.sweepMu.Unlock()
		defer w.endTask(task.ID)
		w.runSweep(tctx, task)
	}()
	return task, nil
}

// CancelTask stops a running task (its queued jobs are dropped).
func (w *Worker) CancelTask(ctx context.Context, id int64) error {
	w.mu.Lock()
	cancel := w.tasks[id]
	w.mu.Unlock()
	if cancel == nil {
		return errors.New("task is not running")
	}
	cancel()
	_, _ = w.st.CancelQueued(ctx, 0, id, "")
	return nil
}

func (w *Worker) endTask(id int64) {
	w.mu.Lock()
	if c := w.tasks[id]; c != nil {
		c()
	}
	delete(w.tasks, id)
	w.mu.Unlock()
}

// SweepPlan is what a sweep would do right now.
type SweepPlan struct {
	Todo      []store.Subtitle
	Unchanged int
	Pinned    int
	NoVideo   int
	Stale     int // unchanged, but verified before reverify_before
	Skipped   int // other languages
}

// PlanSweep decides which files need (re)checking. A file whose subtitle AND
// video are byte-for-byte the files of its last verdict is skipped — files do
// not drift, so desync only enters with new or changed files. That includes
// earlier give-ups: same input, same answer.
func (w *Worker) PlanSweep(ctx context.Context, st store.Settings) (SweepPlan, error) {
	subs, _, err := w.st.ListSubtitles(ctx, store.SubtitleFilter{})
	if err != nil {
		return SweepPlan{}, err
	}
	var reverify string
	if st.ReverifyFrom != "" {
		if t, err := time.ParseInLocation("2006-01-02", st.ReverifyFrom, time.Local); err == nil {
			reverify = store.TimeString(t)
		}
	}
	var p SweepPlan
	for _, s := range subs {
		switch {
		case s.Pinned:
			p.Pinned++
			continue
		case s.VideoPath == "":
			p.NoVideo++
			continue
		case st.Languages != "" && !store.LangIn(s.Lang, st.Languages):
			p.Skipped++
			continue
		}
		same := s.LastDecision != "" && unchanged(s.Path, s.SubSize, s.SubMtime) && unchanged(s.VideoPath, s.VidSize, s.VidMtime)
		if same && (reverify == "" || (s.LastCheckedAt != nil && *s.LastCheckedAt >= reverify)) {
			p.Unchanged++
			continue
		}
		if same {
			p.Stale++
		}
		p.Todo = append(p.Todo, s)
	}
	return p, nil
}

func (w *Worker) runSweep(ctx context.Context, task *store.Task) {
	started := time.Now()
	st, _ := w.st.GetSettings(ctx)
	logf := func(f string, a ...any) { w.st.TaskLog(context.Background(), task.ID, f, a...) }
	logf("scanning the library")
	if r, err := w.Scan(ctx); err != nil {
		logf("scan failed: %v", err)
		_ = w.st.FinishTask(context.Background(), task.ID, "failed", map[string]any{"error": err.Error()})
		return
	} else {
		logf("%d subtitle files (%d new)", r.Files, r.Added)
	}
	plan, err := w.PlanSweep(ctx, st)
	if err != nil {
		logf("planning failed: %v", err)
		_ = w.st.FinishTask(context.Background(), task.ID, "failed", map[string]any{"error": err.Error()})
		return
	}
	logf("%d to check, %d unchanged since their last verdict, %d pinned, %d without a video%s",
		len(plan.Todo), plan.Unchanged, plan.Pinned, plan.NoVideo, ifs(plan.Stale > 0, fmt.Sprintf(", %d re-verified (verdict older than %s)", plan.Stale, st.ReverifyFrom)))
	for _, s := range plan.Todo {
		if _, err := w.Enqueue(ctx, Request{Path: s.Path, Video: s.VideoPath, Source: "sweep", Priority: PrioritySweep, TaskID: &task.ID}); err != nil {
			logf("could not queue %s: %v", s.Rel, err)
		}
	}
	total := len(plan.Todo)
	w.st.TaskProgress(context.Background(), task.ID, 0, total)
	deadline := started.Add(time.Duration(st.SweepBudget) * time.Minute)
	budgetHit := false
	for total > 0 {
		counts, _ := w.st.TaskJobCounts(context.Background(), task.ID)
		done := counts["done"] + counts["failed"] + counts["cancelled"]
		w.st.TaskProgress(context.Background(), task.ID, done, total)
		if counts["queued"]+counts["running"] == 0 {
			break
		}
		if !budgetHit && time.Now().After(deadline) {
			n, _ := w.st.CancelQueued(context.Background(), 0, task.ID, "")
			logf("time budget of %d min reached — %d left for the next sweep", st.SweepBudget, n)
			budgetHit = true
		}
		select {
		case <-ctx.Done():
			n, _ := w.st.CancelQueued(context.Background(), 0, task.ID, "")
			logf("cancelled — %d queued items dropped", n)
			w.finishSweep(task, st, started, "cancelled", plan, total)
			return
		case <-time.After(5 * time.Second):
		}
	}
	w.finishSweep(task, st, started, "done", plan, total)
}

func (w *Worker) finishSweep(task *store.Task, st store.Settings, started time.Time, state string, plan SweepPlan, total int) {
	ctx := context.Background()
	counts, _ := w.st.TaskDecisionCounts(ctx, task.ID)
	jobs, _ := w.st.TaskJobCounts(ctx, task.ID)
	processed := 0
	var parts []string
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		processed += counts[k]
		parts = append(parts, fmt.Sprintf("%s %d", DecisionLabel(k), counts[k]))
	}
	mins := time.Since(started).Minutes()
	w.st.TaskLog(ctx, task.ID, "finished: %d processed in %.0f min — %s", processed, mins, orNothing(strings.Join(parts, ", ")))
	left := jobs["cancelled"]
	summary := map[string]any{"processed": processed, "decisions": counts, "left": left, "todo": total,
		"unchanged": plan.Unchanged, "pinned": plan.Pinned, "minutes": int(mins + 0.5)}
	_ = w.st.FinishTask(ctx, task.ID, state, summary)
	if st.NotifySweep && processed > 0 {
		body := fmt.Sprintf("%d processed in %.0f min\n%s", processed, mins, strings.Join(parts, ", "))
		if left > 0 {
			body += fmt.Sprintf("\n%d left for the next sweep", left)
		}
		level := notify.Success
		for k := range counts {
			if store.IsAttention(k) {
				level = notify.Warning
			}
		}
		w.notify(ctx, st, "Subtitle sweep finished", body, level)
	}
}

// DecisionLabel is the human name of an engine decision.
func DecisionLabel(d string) string {
	switch d {
	case "synced":
		return "adjusted"
	case "already_in_sync":
		return "in sync"
	case "left_unchanged":
		return "gave up"
	case "rejected_wrong_content":
		return "rejected (wrong content)"
	case "rejected_machine_translation":
		return "rejected (machine translation)"
	}
	return strings.ReplaceAll(d, "_", " ")
}

func ifs(c bool, s string) string {
	if c {
		return s
	}
	return ""
}

func orNothing(s string) string {
	if s == "" {
		return "nothing to do"
	}
	return s
}
