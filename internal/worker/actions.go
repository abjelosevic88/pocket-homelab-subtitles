package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/srt"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/store"
)

// MaxNudgeMS is the largest manual shift accepted in one step.
const MaxNudgeMS = 60_000

// Resolve checks that p is a subtitle inside the library and returns its row.
func (w *Worker) Resolve(ctx context.Context, p string) (*store.Subtitle, error) {
	p = filepath.Clean(p)
	if !strings.EqualFold(filepath.Ext(p), ".srt") || !w.lib.Contains(p) {
		return nil, ErrOutsideLibrary
	}
	if !exists(p) {
		return nil, errors.New("file does not exist")
	}
	sub, err := w.st.GetSubtitleByPath(ctx, p)
	if errors.Is(err, store.ErrNotFound) {
		return w.st.UpsertSubtitle(ctx, w.lib.Describe(p, findVideoOr(p, "")))
	}
	return sub, err
}

func findVideoOr(p, v string) string {
	if exists(v) {
		return v
	}
	if v2 := videoFor(p); v2 != "" {
		return v2
	}
	return ""
}

// NudgeResult reports a manual shift.
type NudgeResult struct {
	Cues     int  `json:"cues"`
	MS       int  `json:"ms"`
	TotalMS  int  `json:"total_ms"`
	Pinned   bool `json:"pinned"`
	Jellyfin bool `json:"jellyfin"`
}

func (w *Worker) refresh(ctx context.Context, path string) bool {
	st, _ := w.st.GetSettings(ctx)
	jf := w.jellyfinClient(st)
	if jf == nil {
		return false
	}
	if err := jf.Refresh(ctx, path); err != nil {
		w.log.Warn("jellyfin refresh", "path", path, "err", err)
		return false
	}
	return true
}

// writeAtomic replaces p with data, keeping its permissions.
func writeAtomic(p string, data []byte) error {
	fi, err := os.Stat(p)
	if err != nil {
		return err
	}
	tmp := p + ".pocket.tmp"
	if err := os.WriteFile(tmp, data, fi.Mode().Perm()); err != nil {
		return err
	}
	_ = os.Chmod(tmp, fi.Mode().Perm())
	return os.Rename(tmp, p)
}

// Nudge shifts every cue of a file by ms (negative = earlier), keeps a backup
// for undo, and pins the file: the nightly sweep would otherwise measure the
// hand-made timing and pull it straight back to its own target.
func (w *Worker) Nudge(ctx context.Context, path string, ms int, userID *int64) (*NudgeResult, error) {
	sub, err := w.Resolve(ctx, path)
	if err != nil {
		return nil, err
	}
	if ms == 0 || ms > MaxNudgeMS || ms < -MaxNudgeMS {
		return nil, fmt.Errorf("offset must be non-zero and within ±%d ms", MaxNudgeMS)
	}
	data, err := os.ReadFile(sub.Path)
	if err != nil {
		return nil, err
	}
	shifted, n := srt.Shift(data, int64(ms))
	if n == 0 {
		return nil, errors.New("no cues found in this file")
	}
	bak := filepath.Join(w.cfg.BackupDir, "manual", "nudge", fmt.Sprintf("%s.%d.bak", filepath.Base(sub.Path), time.Now().UnixNano()/1e6))
	if err := copyFile(sub.Path, bak); err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	if err := writeAtomic(sub.Path, shifted); err != nil {
		return nil, err
	}
	note := fmt.Sprintf("nudged %+d ms", ms)
	if !sub.Pinned {
		_ = w.st.SetPinned(ctx, sub.Path, true, note)
	}
	_ = w.st.CreateNudge(ctx, &store.Nudge{Path: sub.Path, MS: ms, Backup: bak, Cues: n, UserID: userID})
	_ = w.st.AddNudgeMS(ctx, sub.Path, ms)
	w.st.AddEvent(ctx, sub.Path, "nudge", fmt.Sprintf("%+d ms, %d cues", ms, n), userID)
	return &NudgeResult{Cues: n, MS: ms, TotalMS: sub.NudgeMS + ms, Pinned: true, Jellyfin: w.refresh(ctx, sub.Path)}, nil
}

// UndoResult reports an undone nudge.
type UndoResult struct {
	RestoredMS int  `json:"restored_ms"`
	TotalMS    int  `json:"total_ms"`
	Jellyfin   bool `json:"jellyfin"`
}

// UndoNudge puts back the file as it was before its last nudge (it stays pinned).
func (w *Worker) UndoNudge(ctx context.Context, path string, userID *int64) (*UndoResult, error) {
	sub, err := w.Resolve(ctx, path)
	if err != nil {
		return nil, err
	}
	last, err := w.st.LastNudge(ctx, sub.Path)
	if err != nil || !exists(last.Backup) {
		return nil, errors.New("nothing to undo for this file")
	}
	if err := copyFile(last.Backup, sub.Path); err != nil {
		return nil, err
	}
	_ = w.st.MarkNudgeUndone(ctx, last.ID)
	_ = w.st.AddNudgeMS(ctx, sub.Path, -last.MS)
	w.st.AddEvent(ctx, sub.Path, "undo", fmt.Sprintf("undid %+d ms", last.MS), userID)
	return &UndoResult{RestoredMS: -last.MS, TotalMS: sub.NudgeMS - last.MS, Jellyfin: w.refresh(ctx, sub.Path)}, nil
}

// Pin freezes a file's timing: no automatic run will touch it.
func (w *Worker) Pin(ctx context.Context, path, note string, userID *int64) error {
	sub, err := w.Resolve(ctx, path)
	if err != nil {
		return err
	}
	if note == "" {
		note = "pinned by hand"
	}
	if err := w.st.SetPinned(ctx, sub.Path, true, note); err != nil {
		return err
	}
	w.st.AddEvent(ctx, sub.Path, "pin", note, userID)
	return nil
}

// Unpin hands a file back to the automatic sync. Returns whether it was pinned.
func (w *Worker) Unpin(ctx context.Context, path string, userID *int64) (bool, error) {
	sub, err := w.Resolve(ctx, path)
	if err != nil {
		return false, err
	}
	if err := w.st.SetPinned(ctx, sub.Path, false, ""); err != nil {
		return false, err
	}
	if sub.Pinned {
		w.st.AddEvent(ctx, sub.Path, "unpin", "handed back to the automatic sync", userID)
	}
	return sub.Pinned, nil
}

// OriginalPath returns the kept first original of a file ("" if none).
func (w *Worker) OriginalPath(sub *store.Subtitle) string {
	p := w.BackupPath(sub)
	if exists(p) {
		return p
	}
	return ""
}

// RestoreOriginal puts back the file exactly as it was first downloaded and
// pins it (otherwise the next sweep would sync it again).
func (w *Worker) RestoreOriginal(ctx context.Context, path string, userID *int64) error {
	sub, err := w.Resolve(ctx, path)
	if err != nil {
		return err
	}
	orig := w.OriginalPath(sub)
	if orig == "" {
		return errors.New("no original is kept for this file (it was never changed by the app)")
	}
	keep := filepath.Join(w.cfg.BackupDir, "manual", "restore", fmt.Sprintf("%s.%d.bak", filepath.Base(sub.Path), time.Now().Unix()))
	if err := copyFile(sub.Path, keep); err != nil {
		return fmt.Errorf("backup of the current file: %w", err)
	}
	data, err := os.ReadFile(orig)
	if err != nil {
		return err
	}
	if err := writeAtomic(sub.Path, data); err != nil {
		return err
	}
	_ = w.st.SetPinned(ctx, sub.Path, true, "restored to the original download")
	w.st.AddEvent(ctx, sub.Path, "restore", "original download restored (previous version kept)", userID)
	w.refresh(ctx, sub.Path)
	return nil
}

// RestoreRejected puts a rejected download back into the library and pins it
// ("I know better than the gate").
func (w *Worker) RestoreRejected(ctx context.Context, runID int64, userID *int64) (string, error) {
	run, err := w.st.GetRun(ctx, runID)
	if err != nil {
		return "", err
	}
	var d struct {
		Quarantine string `json:"quarantine"`
	}
	_ = json.Unmarshal(run.Details, &d)
	if d.Quarantine == "" || !exists(d.Quarantine) {
		return "", errors.New("no copy of this file was kept")
	}
	if run.Path == "" || !w.lib.Contains(run.Path) {
		return "", ErrOutsideLibrary
	}
	if exists(run.Path) {
		return "", errors.New("a subtitle with that name is back in the library already")
	}
	if err := copyFile(d.Quarantine, run.Path); err != nil {
		return "", err
	}
	if _, err := w.st.UpsertSubtitle(ctx, w.lib.Describe(run.Path, videoFor(run.Path))); err != nil {
		return "", err
	}
	_ = w.st.SetPinned(ctx, run.Path, true, "kept after a rejection")
	w.st.AddEvent(ctx, run.Path, "restore", "rejected download put back by hand", userID)
	w.refresh(ctx, run.Path)
	return run.Path, nil
}

// VerifyFile runs the independent check on one file and stores the result.
func (w *Worker) VerifyFile(ctx context.Context, path string, onStage func(string)) (map[string]any, error) {
	sub, err := w.Resolve(ctx, path)
	if err != nil {
		return nil, err
	}
	video := findVideoOr(sub.Path, sub.VideoPath)
	if video == "" {
		return nil, errors.New("no video next to this subtitle")
	}
	st, _ := w.st.GetSettings(ctx)
	if !w.acquire(ctx) {
		return nil, ctx.Err()
	}
	defer w.release()
	lead := st.LeadMS
	if sub.Pinned {
		lead = 0 // a hand-tuned file is checked against the raw audio; the offset is shown as is
	}
	r, err := w.eng.Verify(ctx, video, sub.Path, lead, st.VerifyTolMS, store.LangIn(sub.Lang, st.LatinizeLangs), store.LangIn(sub.Lang, st.MTGateLangs), onStage)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	_ = json.Unmarshal(r.Raw, &out)
	_ = w.st.SetVerify(ctx, sub.Path, r.Raw)
	verdict := "in sync"
	if !r.OK {
		verdict = strings.Join(r.Problems, "; ")
	}
	w.st.AddEvent(ctx, sub.Path, "verify", verdict, nil)
	return out, nil
}

// StartVerify checks one file in the background as a small task.
func (w *Worker) StartVerify(ctx context.Context, path string, userID *int64) (*store.Task, error) {
	sub, err := w.Resolve(ctx, path)
	if err != nil {
		return nil, err
	}
	task, err := w.st.CreateTask(ctx, "verify", "Verify "+displayName(sub), map[string]string{"path": sub.Path}, userID)
	if err != nil {
		return nil, err
	}
	go func() {
		c := context.Background()
		res, err := w.VerifyFile(c, sub.Path, func(s string) { w.st.TaskLog(c, task.ID, "%s", s) })
		if err != nil {
			w.st.TaskLog(c, task.ID, "failed: %v", err)
			_ = w.st.FinishTask(c, task.ID, "failed", map[string]string{"error": err.Error()})
			return
		}
		_ = w.st.FinishTask(c, task.ID, "done", res)
	}()
	return task, nil
}

func displayName(s *store.Subtitle) string {
	n := s.Title
	if s.Kind == "episode" {
		n += " " + s.Label
	}
	if s.Lang != "" {
		n += " [" + strings.ToUpper(s.Lang) + "]"
	}
	return n
}
