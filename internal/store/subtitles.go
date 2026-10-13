package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

const subCols = `id, path, video_path, rel, library, title, kind, season, episode, label, lang, flags,
	sub_size, sub_mtime, vid_size, vid_mtime, last_decision, last_reason, last_method, last_shift, last_run_id,
	last_checked_at, pinned, pin_note, pinned_at, nudge_ms, verify, verified_at, missing, seen_at, created_at`

func scanSubtitle(row interface{ Scan(...any) error }) (*Subtitle, error) {
	var x Subtitle
	var verify string
	var season, episode sql.NullInt64
	if err := row.Scan(&x.ID, &x.Path, &x.VideoPath, &x.Rel, &x.Library, &x.Title, &x.Kind, &season, &episode, &x.Label, &x.Lang, &x.Flags,
		&x.SubSize, &x.SubMtime, &x.VidSize, &x.VidMtime, &x.LastDecision, &x.LastReason, &x.LastMethod, &x.LastShift, &x.LastRunID,
		&x.LastCheckedAt, &x.Pinned, &x.PinNote, &x.PinnedAt, &x.NudgeMS, &verify, &x.VerifiedAt, &x.Missing, &x.SeenAt, &x.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	if season.Valid {
		n := int(season.Int64)
		x.Season = &n
	}
	if episode.Valid {
		n := int(episode.Int64)
		x.Episode = &n
	}
	if verify != "" {
		x.Verify = json.RawMessage(verify)
	}
	return &x, nil
}

// LibraryEntry is what a library scan knows about one subtitle file.
type LibraryEntry struct {
	Path, VideoPath, Rel, Library, Title, Kind, Label, Lang, Flags string
	Season, Episode                                                *int
}

// SyncLibrary upserts every scanned file and marks the rest missing. Returns
// how many files are new.
func (s *Store) SyncLibrary(ctx context.Context, entries []LibraryEntry) (added int, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	var before int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM subtitles`).Scan(&before); err != nil {
		return 0, err
	}
	now := Now()
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Path] = true
		if _, err = tx.ExecContext(ctx, `INSERT INTO subtitles (path, video_path, rel, library, title, kind, season, episode, label, lang, flags, seen_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(path) DO UPDATE SET video_path = excluded.video_path, rel = excluded.rel, library = excluded.library,
			  title = excluded.title, kind = excluded.kind, season = excluded.season, episode = excluded.episode,
			  label = excluded.label, lang = excluded.lang, flags = excluded.flags, missing = 0, seen_at = excluded.seen_at`,
			e.Path, e.VideoPath, e.Rel, e.Library, e.Title, e.Kind, e.Season, e.Episode, e.Label, e.Lang, e.Flags, now); err != nil {
			return 0, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT path FROM subtitles WHERE missing = 0`)
	if err != nil {
		return 0, err
	}
	var gone []string
	for rows.Next() {
		var p string
		if err = rows.Scan(&p); err != nil {
			rows.Close()
			return 0, err
		}
		if !seen[p] {
			gone = append(gone, p)
		}
	}
	rows.Close()
	for _, p := range gone {
		if _, err = tx.ExecContext(ctx, `UPDATE subtitles SET missing = 1 WHERE path = ?`, p); err != nil {
			return 0, err
		}
	}
	var after int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM subtitles`).Scan(&after); err != nil {
		return 0, err
	}
	return after - before, tx.Commit()
}

// UpsertSubtitle makes sure a row exists for one file (outside a full scan).
func (s *Store) UpsertSubtitle(ctx context.Context, e LibraryEntry) (*Subtitle, error) {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO subtitles (path, video_path, rel, library, title, kind, season, episode, label, lang, flags, seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET video_path = CASE WHEN excluded.video_path != '' THEN excluded.video_path ELSE subtitles.video_path END,
		  missing = 0, seen_at = excluded.seen_at`,
		e.Path, e.VideoPath, e.Rel, e.Library, e.Title, e.Kind, e.Season, e.Episode, e.Label, e.Lang, e.Flags, Now())
	if err != nil {
		return nil, err
	}
	return s.GetSubtitleByPath(ctx, e.Path)
}

// GetSubtitle returns one row by id.
func (s *Store) GetSubtitle(ctx context.Context, id int64) (*Subtitle, error) {
	return scanSubtitle(s.DB.QueryRowContext(ctx, `SELECT `+subCols+` FROM subtitles WHERE id = ?`, id))
}

// GetSubtitleByPath returns one row by path.
func (s *Store) GetSubtitleByPath(ctx context.Context, path string) (*Subtitle, error) {
	return scanSubtitle(s.DB.QueryRowContext(ctx, `SELECT `+subCols+` FROM subtitles WHERE path = ?`, path))
}

// SubtitleFilter narrows ListSubtitles.
type SubtitleFilter struct {
	Q        string // every word must appear in the relative path or title
	Title    string // exact show/movie
	Lang     string
	Status   string // attention | pinned | nudged | unchecked | synced | in_sync | missing
	Library  string
	Season   *int
	Limit    int
	Offset   int
	Missing  bool // include missing files
	OrderRel bool
}

func (f SubtitleFilter) where() (string, []any) {
	var w []string
	var args []any
	for _, word := range strings.Fields(strings.ToLower(f.Q)) {
		w = append(w, `(LOWER(rel) LIKE ? OR LOWER(title) LIKE ? OR LOWER(label) LIKE ?)`)
		like := "%" + word + "%"
		args = append(args, like, like, like)
	}
	if f.Title != "" {
		w = append(w, `title = ?`)
		args = append(args, f.Title)
	}
	if f.Library != "" {
		w = append(w, `library = ?`)
		args = append(args, f.Library)
	}
	if f.Lang != "" {
		w = append(w, `lang = ?`)
		args = append(args, f.Lang)
	}
	if f.Season != nil {
		w = append(w, `season = ?`)
		args = append(args, *f.Season)
	}
	switch f.Status {
	case "attention":
		w = append(w, `last_decision IN ('`+strings.Join(AttentionDecisions, `','`)+`') AND pinned = 0`)
	case "pinned":
		w = append(w, `pinned = 1`)
	case "nudged":
		w = append(w, `nudge_ms != 0`)
	case "unchecked":
		w = append(w, `last_decision = ''`)
	case "synced":
		w = append(w, `last_decision = 'synced'`)
	case "in_sync":
		w = append(w, `last_decision = 'already_in_sync'`)
	case "missing":
		w = append(w, `missing = 1`)
	}
	if !f.Missing && f.Status != "missing" {
		w = append(w, `missing = 0`)
	}
	if len(w) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(w, " AND "), args
}

// ListSubtitles returns matching files and the total count.
func (s *Store) ListSubtitles(ctx context.Context, f SubtitleFilter) ([]Subtitle, int, error) {
	where, args := f.where()
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM subtitles`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT ` + subCols + ` FROM subtitles` + where + ` ORDER BY LOWER(title), season, episode, LOWER(rel)`
	if f.Limit > 0 {
		q += ` LIMIT ? OFFSET ?`
		args = append(args, f.Limit, f.Offset)
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Subtitle{}
	for rows.Next() {
		x, err := scanSubtitle(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *x)
	}
	return out, total, rows.Err()
}

// TitleSummary is one show or movie in the library overview.
type TitleSummary struct {
	Title     string   `json:"title"`
	Library   string   `json:"library"`
	Kind      string   `json:"kind"`
	Files     int      `json:"files"`
	Langs     []string `json:"langs"`
	Seasons   []int    `json:"seasons"`
	Attention int      `json:"attention"`
	Pinned    int      `json:"pinned"`
	Synced    int      `json:"synced"`
	Unchecked int      `json:"unchecked"`
	LastAt    *string  `json:"last_at"`
}

// ListTitles groups the library by show/movie.
func (s *Store) ListTitles(ctx context.Context, f SubtitleFilter) ([]TitleSummary, error) {
	f.Limit = 0
	where, args := f.where()
	rows, err := s.DB.QueryContext(ctx, `SELECT title, library, MAX(kind), COUNT(1),
		GROUP_CONCAT(DISTINCT lang), GROUP_CONCAT(DISTINCT season),
		SUM(CASE WHEN last_decision IN ('`+strings.Join(AttentionDecisions, `','`)+`') AND pinned = 0 THEN 1 ELSE 0 END),
		SUM(pinned), SUM(CASE WHEN last_decision = 'synced' THEN 1 ELSE 0 END),
		SUM(CASE WHEN last_decision = '' THEN 1 ELSE 0 END), MAX(last_checked_at)
		FROM subtitles`+where+` GROUP BY title, library ORDER BY LOWER(title)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TitleSummary{}
	for rows.Next() {
		var t TitleSummary
		var langs, seasons sql.NullString
		if err := rows.Scan(&t.Title, &t.Library, &t.Kind, &t.Files, &langs, &seasons, &t.Attention, &t.Pinned, &t.Synced, &t.Unchecked, &t.LastAt); err != nil {
			return nil, err
		}
		t.Langs = splitNonEmpty(langs.String)
		for _, x := range splitNonEmpty(seasons.String) {
			var n int
			for _, c := range x {
				n = n*10 + int(c-'0')
			}
			t.Seasons = append(t.Seasons, n)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func splitNonEmpty(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Verdict is what a finished run writes back onto the file's row.
type Verdict struct {
	Decision, Reason, Method string
	Shift                    *float64
	RunID                    int64
	SubSize                  *int64
	SubMtime                 *float64
	VidSize                  *int64
	VidMtime                 *float64
	CheckedAt                string
}

// SetVerdict records the outcome of a run on the file's row.
func (s *Store) SetVerdict(ctx context.Context, path string, v Verdict) error {
	var runID *int64
	if v.RunID != 0 {
		runID = &v.RunID
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE subtitles SET last_decision = ?, last_reason = ?, last_method = ?, last_shift = ?, last_run_id = ?,
		sub_size = ?, sub_mtime = ?, vid_size = ?, vid_mtime = ?, last_checked_at = ? WHERE path = ?`,
		v.Decision, v.Reason, v.Method, v.Shift, runID, v.SubSize, v.SubMtime, v.VidSize, v.VidMtime, v.CheckedAt, path)
	return err
}

// SetPinned pins or unpins a file.
func (s *Store) SetPinned(ctx context.Context, path string, pinned bool, note string) error {
	if pinned {
		_, err := s.DB.ExecContext(ctx, `UPDATE subtitles SET pinned = 1, pin_note = ?, pinned_at = COALESCE(pinned_at, ?) WHERE path = ?`, note, Now(), path)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE subtitles SET pinned = 0, pin_note = '', pinned_at = NULL WHERE path = ?`, path)
	return err
}

// AddNudgeMS adds to the file's net manual shift.
func (s *Store) AddNudgeMS(ctx context.Context, path string, ms int) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE subtitles SET nudge_ms = nudge_ms + ? WHERE path = ?`, ms, path)
	return err
}

// SetNudgeMS sets the file's net manual shift (imports).
func (s *Store) SetNudgeMS(ctx context.Context, path string, ms int) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE subtitles SET nudge_ms = ? WHERE path = ?`, ms, path)
	return err
}

// SetVerify stores the JSON of an independent check.
func (s *Store) SetVerify(ctx context.Context, path string, js []byte) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE subtitles SET verify = ?, verified_at = ? WHERE path = ?`, string(js), Now(), path)
	return err
}

// MarkMissing flags a file that was deleted (rejected, removed).
func (s *Store) MarkMissing(ctx context.Context, path string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE subtitles SET missing = 1 WHERE path = ?`, path)
	return err
}

// RenamePath moves a row (and its history links) to a new file name. If a
// row already exists at the target, that row is replaced.
func (s *Store) RenamePath(ctx context.Context, from, to string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM subtitles WHERE path = ? AND ? != ''`,
	} {
		if _, err := tx.ExecContext(ctx, q, to, from); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	for _, q := range []string{`UPDATE subtitles SET path = ? WHERE path = ?`, `UPDATE runs SET path = ? WHERE path = ?`,
		`UPDATE nudges SET path = ? WHERE path = ?`, `UPDATE events SET path = ? WHERE path = ?`} {
		if _, err := tx.ExecContext(ctx, q, to, from); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// LibraryStats are the counters on the overview.
type LibraryStats struct {
	Files     int `json:"files"`
	Titles    int `json:"titles"`
	Attention int `json:"attention"`
	Pinned    int `json:"pinned"`
	Nudged    int `json:"nudged"`
	Unchecked int `json:"unchecked"`
	Missing   int `json:"missing"`
}

// GetLibraryStats counts the library.
func (s *Store) GetLibraryStats(ctx context.Context) (LibraryStats, error) {
	var st LibraryStats
	err := s.DB.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN missing = 0 THEN 1 ELSE 0 END), 0),
		COUNT(DISTINCT CASE WHEN missing = 0 THEN title END),
		COALESCE(SUM(CASE WHEN missing = 0 AND pinned = 0 AND last_decision IN ('`+strings.Join(AttentionDecisions, `','`)+`') THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN missing = 0 AND pinned = 1 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN missing = 0 AND nudge_ms != 0 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN missing = 0 AND last_decision = '' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(missing), 0)
		FROM subtitles`).Scan(&st.Files, &st.Titles, &st.Attention, &st.Pinned, &st.Nudged, &st.Unchecked, &st.Missing)
	return st, err
}

// Languages lists the languages present in the library.
func (s *Store) Languages(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT lang FROM subtitles WHERE missing = 0 AND lang != '' GROUP BY lang ORDER BY COUNT(1) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
