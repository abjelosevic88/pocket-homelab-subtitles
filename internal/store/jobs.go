package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

const jobCols = `j.id, j.path, j.video_path, j.title, j.label, j.lang, j.source, j.priority, j.extras, j.force, j.state, j.stage,
	j.run_id, j.task_id, j.error, j.created_at, j.started_at, j.finished_at, COALESCE(r.decision, '')`

const jobFrom = ` FROM jobs j LEFT JOIN runs r ON r.id = j.run_id`

func scanJob(row interface{ Scan(...any) error }) (*Job, error) {
	var j Job
	var extras string
	if err := row.Scan(&j.ID, &j.Path, &j.VideoPath, &j.Title, &j.Label, &j.Lang, &j.Source, &j.Priority, &extras, &j.Force, &j.State, &j.Stage,
		&j.RunID, &j.TaskID, &j.Error, &j.CreatedAt, &j.StartedAt, &j.FinishedAt, &j.Decision); err != nil {
		return nil, notFound(err)
	}
	_ = json.Unmarshal([]byte(extras), &j.Extras)
	return &j, nil
}

// EnqueueResult tells whether a new job was created or an existing queued one reused.
type EnqueueResult struct {
	Job     *Job
	Created bool
}

// Enqueue adds a job, or merges it into a queued job for the same file (the
// higher priority, fresh Bazarr ids and force win).
func (s *Store) Enqueue(ctx context.Context, j *Job) (EnqueueResult, error) {
	extras, _ := json.Marshal(j.Extras)
	if j.Extras == nil {
		extras = []byte("{}")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return EnqueueResult{}, err
	}
	var id int64
	var prio int
	var oldExtras string
	err = tx.QueryRowContext(ctx, `SELECT id, priority, extras FROM jobs WHERE path = ? AND state = 'queued' ORDER BY id LIMIT 1`, j.Path).Scan(&id, &prio, &oldExtras)
	switch {
	case err == nil:
		if prio < j.Priority {
			prio = j.Priority
		}
		ex := oldExtras
		if len(j.Extras) > 0 {
			ex = string(extras)
		}
		// a fresh Bazarr download or a person asking takes over a sweep item
		src := ""
		if j.Source != "sweep" {
			src = j.Source
		}
		if _, err = tx.ExecContext(ctx, `UPDATE jobs SET priority = ?, extras = ?, force = MAX(force, ?), video_path = CASE WHEN ? != '' THEN ? ELSE video_path END,
			source = CASE WHEN ? != '' THEN ? ELSE source END, task_id = CASE WHEN ? THEN task_id ELSE COALESCE(?, task_id) END WHERE id = ?`,
			prio, ex, boolInt(j.Force), j.VideoPath, j.VideoPath, src, src, j.TaskID == nil, j.TaskID, id); err != nil {
			_ = tx.Rollback()
			return EnqueueResult{}, err
		}
		if err = tx.Commit(); err != nil {
			return EnqueueResult{}, err
		}
		got, err := s.GetJob(ctx, id)
		return EnqueueResult{Job: got}, err
	case errors.Is(err, sql.ErrNoRows):
	default:
		_ = tx.Rollback()
		return EnqueueResult{}, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO jobs (path, video_path, title, label, lang, source, priority, extras, force, task_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.Path, j.VideoPath, j.Title, j.Label, j.Lang, j.Source, j.Priority, string(extras), boolInt(j.Force), j.TaskID)
	if err != nil {
		_ = tx.Rollback()
		return EnqueueResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return EnqueueResult{}, err
	}
	id, _ = res.LastInsertId()
	got, err := s.GetJob(ctx, id)
	return EnqueueResult{Job: got, Created: true}, err
}

// GetJob returns one job.
func (s *Store) GetJob(ctx context.Context, id int64) (*Job, error) {
	return scanJob(s.DB.QueryRowContext(ctx, `SELECT `+jobCols+jobFrom+` WHERE j.id = ?`, id))
}

// ClaimJob marks the next queued job running and returns it (nil when the queue is empty).
func (s *Store) ClaimJob(ctx context.Context) (*Job, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM jobs WHERE state = 'queued' AND path NOT IN (SELECT path FROM jobs WHERE state = 'running')
		ORDER BY priority DESC, id LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		return nil, nil
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state = 'running', stage = 'starting', started_at = ? WHERE id = ?`, Now(), id); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetJob(ctx, id)
}

// FinishJob closes a job.
func (s *Store) FinishJob(ctx context.Context, id int64, state string, runID *int64, errMsg string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET state = ?, run_id = ?, error = ?, stage = '', finished_at = ? WHERE id = ?`, state, runID, errMsg, Now(), id)
	return err
}

// SetJobStage stores the engine's progress text.
func (s *Store) SetJobStage(ctx context.Context, id int64, stage string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET stage = ? WHERE id = ?`, stage, id)
	return err
}

// RequeueRunning puts jobs that were running when the app stopped back in the queue.
func (s *Store) RequeueRunning(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE jobs SET state = 'queued', stage = '', started_at = NULL WHERE state = 'running'`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CancelQueued cancels queued jobs: one id, all of a task, or (both zero) everything queued from a source.
func (s *Store) CancelQueued(ctx context.Context, id, taskID int64, source string) (int64, error) {
	q, args := `UPDATE jobs SET state = 'cancelled', finished_at = ? WHERE state = 'queued'`, []any{Now()}
	switch {
	case id != 0:
		q += ` AND id = ?`
		args = append(args, id)
	case taskID != 0:
		q += ` AND task_id = ?`
		args = append(args, taskID)
	case source != "":
		q += ` AND source = ?`
		args = append(args, source)
	}
	res, err := s.DB.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// BumpJob moves a queued job to the front.
func (s *Store) BumpJob(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET priority = (SELECT COALESCE(MAX(priority), 0) + 1 FROM jobs WHERE state = 'queued') WHERE id = ? AND state = 'queued'`, id)
	return err
}

// JobFilter narrows ListJobs.
type JobFilter struct {
	States []string
	TaskID int64
	Limit  int
}

// ListJobs returns jobs in queue order (running first, then by priority).
func (s *Store) ListJobs(ctx context.Context, f JobFilter) ([]Job, int, error) {
	var w []string
	var args []any
	if len(f.States) > 0 {
		w = append(w, `j.state IN (?`+strings.Repeat(",?", len(f.States)-1)+`)`)
		for _, st := range f.States {
			args = append(args, st)
		}
	}
	if f.TaskID != 0 {
		w = append(w, `j.task_id = ?`)
		args = append(args, f.TaskID)
	}
	where := ""
	if len(w) > 0 {
		where = " WHERE " + strings.Join(w, " AND ")
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM jobs j`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Limit <= 0 {
		f.Limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+jobCols+jobFrom+where+`
		ORDER BY CASE j.state WHEN 'running' THEN 0 WHEN 'queued' THEN 1 ELSE 2 END,
		CASE WHEN j.state IN ('queued','running') THEN -j.priority ELSE 0 END,
		CASE WHEN j.state IN ('queued','running') THEN j.id ELSE -j.id END LIMIT ?`, append(args, f.Limit)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *j)
	}
	return out, total, rows.Err()
}

// QueueCounts returns how many jobs are queued and running.
func (s *Store) QueueCounts(ctx context.Context) (queued, running int, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(state = 'queued'), 0), COALESCE(SUM(state = 'running'), 0) FROM jobs WHERE state IN ('queued','running')`).Scan(&queued, &running)
	return
}

// TaskJobCounts returns state counts of one task's jobs.
func (s *Store) TaskJobCounts(ctx context.Context, taskID int64) (map[string]int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT state, COUNT(1) FROM jobs WHERE task_id = ? GROUP BY state`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// PruneJobs drops finished jobs older than the cutoff.
func (s *Store) PruneJobs(ctx context.Context, before string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM jobs WHERE state NOT IN ('queued','running') AND COALESCE(finished_at, created_at) < ?`, before)
	return err
}

// TaskDecisionCounts counts the run decisions of one task's jobs.
func (s *Store) TaskDecisionCounts(ctx context.Context, taskID int64) (map[string]int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT r.decision, COUNT(1) FROM jobs j JOIN runs r ON r.id = j.run_id WHERE j.task_id = ? GROUP BY r.decision`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var d string
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			return nil, err
		}
		out[d] = n
	}
	return out, rows.Err()
}
