package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

const runCols = `id, subtitle_id, path, name, title, label, lang, source, decision, method, max_shift, content_fit, ref_fit, reason, duration_ms, job_id, created_at`

func scanRun(row interface{ Scan(...any) error }, details *string) (*Run, error) {
	var r Run
	dest := []any{&r.ID, &r.SubtitleID, &r.Path, &r.Name, &r.Title, &r.Label, &r.Lang, &r.Source, &r.Decision, &r.Method, &r.MaxShift, &r.ContentFit, &r.RefFit, &r.Reason, &r.DurationMS, &r.JobID, &r.CreatedAt}
	if details != nil {
		dest = append(dest, details)
	}
	if err := row.Scan(dest...); err != nil {
		return nil, notFound(err)
	}
	if details != nil && *details != "" {
		r.Details = []byte(*details)
	}
	return &r, nil
}

// CreateRun records one finished engine run.
func (s *Store) CreateRun(ctx context.Context, r *Run) error {
	if r.CreatedAt == "" {
		r.CreatedAt = Now()
	}
	details := "{}"
	if len(r.Details) > 0 {
		details = string(r.Details)
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO runs (subtitle_id, path, name, title, label, lang, source, decision, method, max_shift, content_fit, ref_fit, reason, duration_ms, details, job_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.SubtitleID, r.Path, r.Name, r.Title, r.Label, r.Lang, r.Source, r.Decision, r.Method, r.MaxShift, r.ContentFit, r.RefFit, r.Reason, r.DurationMS, details, r.JobID, r.CreatedAt)
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	return nil
}

// GetRun returns one run with its full engine details.
func (s *Store) GetRun(ctx context.Context, id int64) (*Run, error) {
	var d string
	return scanRun(s.DB.QueryRowContext(ctx, `SELECT `+runCols+`, details FROM runs WHERE id = ?`, id), &d)
}

// RunFilter narrows ListRuns.
type RunFilter struct {
	Q          string
	Outcome    string // adjusted | in_sync | attention | rejected | pinned
	Decision   string
	Source     string
	Lang       string
	SubtitleID int64
	Path       string
	Since      string
	Limit      int
	Offset     int
}

// ListRuns returns runs newest first and the total count.
func (s *Store) ListRuns(ctx context.Context, f RunFilter) ([]Run, int, error) {
	var w []string
	var args []any
	for _, word := range strings.Fields(strings.ToLower(f.Q)) {
		w = append(w, `(LOWER(name) LIKE ? OR LOWER(title) LIKE ? OR LOWER(label) LIKE ?)`)
		like := "%" + word + "%"
		args = append(args, like, like, like)
	}
	switch f.Outcome {
	case "adjusted":
		w = append(w, `decision = 'synced'`)
	case "in_sync":
		w = append(w, `decision = 'already_in_sync'`)
	case "attention":
		w = append(w, `decision IN ('left_unchanged','error','timeout')`)
	case "rejected":
		w = append(w, `decision LIKE 'rejected_%'`)
	case "pinned":
		w = append(w, `decision = 'pinned'`)
	}
	if f.Decision != "" {
		w = append(w, `decision = ?`)
		args = append(args, f.Decision)
	}
	if f.Source != "" {
		w = append(w, `source = ?`)
		args = append(args, f.Source)
	}
	if f.Lang != "" {
		w = append(w, `lang = ?`)
		args = append(args, f.Lang)
	}
	if f.SubtitleID != 0 {
		w = append(w, `subtitle_id = ?`)
		args = append(args, f.SubtitleID)
	}
	if f.Path != "" {
		w = append(w, `path = ?`)
		args = append(args, f.Path)
	}
	if f.Since != "" {
		w = append(w, `created_at >= ?`)
		args = append(args, f.Since)
	}
	where := ""
	if len(w) > 0 {
		where = " WHERE " + strings.Join(w, " AND ")
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM runs`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+runCols+` FROM runs`+where+` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r, err := scanRun(rows, nil)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *r)
	}
	return out, total, rows.Err()
}

// RunStats are the history counters on the overview.
type RunStats struct {
	Processed int `json:"processed"`
	Adjusted  int `json:"adjusted"`
	InSync    int `json:"in_sync"`
	Rejected  int `json:"rejected"`
	Failed    int `json:"failed"`
	Today     int `json:"today"`
}

// GetRunStats counts the history.
func (s *Store) GetRunStats(ctx context.Context) (RunStats, error) {
	var st RunStats
	since := TimeString(startOfDay(time.Now()))
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1),
		COALESCE(SUM(CASE WHEN decision = 'synced' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN decision = 'already_in_sync' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN decision LIKE 'rejected_%' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN decision IN ('left_unchanged','error','timeout') THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN created_at >= ? THEN 1 ELSE 0 END), 0)
		FROM runs WHERE decision != 'pinned'`, since).Scan(&st.Processed, &st.Adjusted, &st.InSync, &st.Rejected, &st.Failed, &st.Today)
	return st, err
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// DayCount is one day of the activity chart.
type DayCount struct {
	Day      string `json:"day"` // YYYY-MM-DD, server time zone
	Adjusted int    `json:"adjusted"`
	InSync   int    `json:"in_sync"`
	Problems int    `json:"problems"`
}

// RunsPerDay counts runs per local day for the last n days (oldest first).
func (s *Store) RunsPerDay(ctx context.Context, n int) ([]DayCount, error) {
	now := time.Now()
	first := startOfDay(now).AddDate(0, 0, -(n - 1))
	rows, err := s.DB.QueryContext(ctx, `SELECT created_at, decision FROM runs WHERE created_at >= ? AND decision != 'pinned'`, TimeString(first))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := map[string]int{}
	out := make([]DayCount, n)
	for i := 0; i < n; i++ {
		d := first.AddDate(0, 0, i).Format("2006-01-02")
		out[i].Day = d
		idx[d] = i
	}
	for rows.Next() {
		var at, dec string
		if err := rows.Scan(&at, &dec); err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339Nano, at)
		if err != nil {
			continue
		}
		i, ok := idx[t.Local().Format("2006-01-02")]
		if !ok {
			continue
		}
		switch {
		case dec == "synced":
			out[i].Adjusted++
		case dec == "already_in_sync":
			out[i].InSync++
		default:
			out[i].Problems++
		}
	}
	return out, rows.Err()
}

// LastRunFor returns the newest run of a path, if any.
func (s *Store) LastRunFor(ctx context.Context, path string) (*Run, error) {
	return scanRun(s.DB.QueryRowContext(ctx, `SELECT `+runCols+` FROM runs WHERE path = ? ORDER BY created_at DESC, id DESC LIMIT 1`, path), nil)
}

// LinkRuns attaches imported runs (which only know a file name) to library rows.
func (s *Store) LinkRuns(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE runs SET
		subtitle_id = (SELECT id FROM subtitles s WHERE s.path = runs.path),
		title = COALESCE((SELECT title FROM subtitles s WHERE s.path = runs.path), title),
		label = COALESCE((SELECT label FROM subtitles s WHERE s.path = runs.path), label),
		lang = COALESCE((SELECT lang FROM subtitles s WHERE s.path = runs.path), lang)
		WHERE subtitle_id IS NULL AND path != '' AND EXISTS (SELECT 1 FROM subtitles s WHERE s.path = runs.path)`)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return n, err
	}
	// History keeps the names a file had when it ran; when the library learns a
	// better name (a parser fix, a renamed folder), carry it over.
	_, err = s.DB.ExecContext(ctx, `UPDATE runs SET
		title = (SELECT title FROM subtitles s WHERE s.id = runs.subtitle_id),
		label = (SELECT label FROM subtitles s WHERE s.id = runs.subtitle_id)
		WHERE subtitle_id IS NOT NULL AND EXISTS (SELECT 1 FROM subtitles s WHERE s.id = runs.subtitle_id
		  AND (s.title != runs.title OR s.label != runs.label))`)
	return n, err
}

var _ = sql.ErrNoRows
