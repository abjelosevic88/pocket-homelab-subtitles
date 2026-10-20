package store

import (
	"context"
	"encoding/json"
	"fmt"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"time"
)

const taskCols = `id, kind, title, state, params, summary, total, done, user_id, created_at, finished_at`

func scanTask(row interface{ Scan(...any) error }, withLog bool) (*Task, error) {
	var t Task
	var params, summary string
	dest := []any{&t.ID, &t.Kind, &t.Title, &t.State, &params, &summary, &t.Total, &t.Done, &t.UserID, &t.CreatedAt, &t.FinishedAt}
	if withLog {
		dest = append(dest, &t.Log)
	}
	if err := row.Scan(dest...); err != nil {
		return nil, notFound(err)
	}
	t.Params, t.Summary = json.RawMessage(params), json.RawMessage(summary)
	return &t, nil
}

// CreateTask starts a task.
func (s *Store) CreateTask(ctx context.Context, kind, title string, params any, userID *int64) (*Task, error) {
	p, _ := json.Marshal(params)
	if params == nil {
		p = []byte("{}")
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO tasks (kind, title, params, user_id) VALUES (?, ?, ?, ?)`, kind, title, string(p), userID)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetTask(ctx, id)
}

// GetTask returns a task with its log.
func (s *Store) GetTask(ctx context.Context, id int64) (*Task, error) {
	return scanTask(s.DB.QueryRowContext(ctx, `SELECT `+taskCols+`, log FROM tasks WHERE id = ?`, id), true)
}

// TaskLog appends a timestamped line to a task's log.
func (s *Store) TaskLog(ctx context.Context, id int64, format string, args ...any) {
	line := time.Now().Format("15:04:05") + "  " + fmt.Sprintf(format, args...) + "\n"
	_, _ = s.DB.ExecContext(ctx, `UPDATE tasks SET log = log || ? WHERE id = ?`, line, id)
}

// TaskProgress sets done/total.
func (s *Store) TaskProgress(ctx context.Context, id int64, done, total int) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE tasks SET done = ?, total = ? WHERE id = ?`, done, total, id)
}

// FinishTask closes a task with a summary.
func (s *Store) FinishTask(ctx context.Context, id int64, state string, summary any) error {
	b, _ := json.Marshal(summary)
	if summary == nil {
		b = []byte("{}")
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE tasks SET state = ?, summary = ?, finished_at = ? WHERE id = ?`, state, string(b), Now(), id)
	return err
}

// ListTasks returns recent tasks (no logs).
func (s *Store) ListTasks(ctx context.Context, kind string, limit int) ([]Task, error) {
	if limit <= 0 {
		limit = 50
	}
	q, args := `SELECT `+taskCols+` FROM tasks`, []any{}
	if kind != "" {
		q += ` WHERE kind = ?`
		args = append(args, kind)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// FailRunningTasks closes tasks left running by a previous process.
func (s *Store) FailRunningTasks(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE tasks SET state = 'failed', log = log || 'interrupted: the app was restarted\n', finished_at = ? WHERE state = 'running'`, Now())
	return err
}

// ---- nudges ----

// CreateNudge records a manual shift.
func (s *Store) CreateNudge(ctx context.Context, n *Nudge, after ...core.CommitHook) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if n.CreatedAt == "" {
		n.CreatedAt = Now()
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO nudges (path, ms, backup, cues, undone, user_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		n.Path, n.MS, n.Backup, n.Cues, boolInt(n.Undone), n.UserID, n.CreatedAt)
	if err != nil {
		return err
	}
	n.ID, _ = res.LastInsertId()
	return core.Commit(tx, after...)
}

// LastNudge returns the newest not-undone nudge of a file.
func (s *Store) LastNudge(ctx context.Context, path string) (*Nudge, error) {
	var n Nudge
	err := s.DB.QueryRowContext(ctx, `SELECT id, path, ms, backup, cues, undone, user_id, created_at FROM nudges WHERE path = ? AND undone = 0 ORDER BY id DESC LIMIT 1`, path).
		Scan(&n.ID, &n.Path, &n.MS, &n.Backup, &n.Cues, &n.Undone, &n.UserID, &n.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &n, nil
}

// MarkNudgeUndone flags a nudge as undone.
func (s *Store) MarkNudgeUndone(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE nudges SET undone = 1 WHERE id = ?`, id)
	return err
}

// RecentNudges lists the newest nudges across the library.
func (s *Store) RecentNudges(ctx context.Context, limit int) ([]Nudge, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, path, ms, backup, cues, undone, user_id, created_at FROM nudges ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Nudge{}
	for rows.Next() {
		var n Nudge
		if err := rows.Scan(&n.ID, &n.Path, &n.MS, &n.Backup, &n.Cues, &n.Undone, &n.UserID, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ---- events ----

// AddEvent records something done to a file.
func (s *Store) AddEvent(ctx context.Context, path, kind, detail string, userID *int64) {
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO events (path, kind, detail, user_id, created_at) VALUES (?, ?, ?, ?, ?)`, path, kind, detail, userID, Now())
}

// AddEventAt records an event with an explicit time (imports).
func (s *Store) AddEventAt(ctx context.Context, path, kind, detail, at string) {
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO events (path, kind, detail, created_at) VALUES (?, ?, ?, ?)`, path, kind, detail, at)
}

// ListEvents returns a file's events, newest first.
func (s *Store) ListEvents(ctx context.Context, path string, limit int) ([]Event, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, path, kind, detail, user_id, created_at FROM events WHERE path = ? ORDER BY created_at DESC, id DESC LIMIT ?`, path, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Path, &e.Kind, &e.Detail, &e.UserID, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
