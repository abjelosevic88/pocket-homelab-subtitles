package store

import "encoding/json"

// User is an account that can sign in. The first user is the admin; admins
// change settings and manage other users.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // admin | member
	CreatedAt    string `json:"created_at"`
}

// APIToken is a personal access token (hash stored, plaintext shown once).
type APIToken struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"user_id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// Subtitle is one external subtitle file in the library and what the app last
// concluded about it.
type Subtitle struct {
	ID            int64           `json:"id"`
	Path          string          `json:"path"`
	VideoPath     string          `json:"video_path"`
	Rel           string          `json:"rel"`
	Library       string          `json:"library"`
	Title         string          `json:"title"`
	Kind          string          `json:"kind"`
	Season        *int            `json:"season"`
	Episode       *int            `json:"episode"`
	Label         string          `json:"label"`
	Lang          string          `json:"lang"`
	Flags         string          `json:"flags"`
	SubSize       *int64          `json:"-"`
	SubMtime      *float64        `json:"-"`
	VidSize       *int64          `json:"-"`
	VidMtime      *float64        `json:"-"`
	LastDecision  string          `json:"last_decision"`
	LastReason    string          `json:"last_reason"`
	LastMethod    string          `json:"last_method"`
	LastShift     *float64        `json:"last_shift"`
	LastRunID     *int64          `json:"last_run_id"`
	LastCheckedAt *string         `json:"last_checked_at"`
	Pinned        bool            `json:"pinned"`
	PinNote       string          `json:"pin_note"`
	PinnedAt      *string         `json:"pinned_at"`
	NudgeMS       int             `json:"nudge_ms"`
	Verify        json.RawMessage `json:"verify"`
	VerifiedAt    *string         `json:"verified_at"`
	Missing       bool            `json:"missing"`
	SeenAt        *string         `json:"seen_at"`
	CreatedAt     string          `json:"created_at"`
}

// Run is one completed engine run on a subtitle.
type Run struct {
	ID         int64           `json:"id"`
	SubtitleID *int64          `json:"subtitle_id"`
	Path       string          `json:"path"`
	Name       string          `json:"name"`
	Title      string          `json:"title"`
	Label      string          `json:"label"`
	Lang       string          `json:"lang"`
	Source     string          `json:"source"`
	Decision   string          `json:"decision"`
	Method     string          `json:"method"`
	MaxShift   *float64        `json:"max_shift"`
	ContentFit *float64        `json:"content_fit"`
	RefFit     *float64        `json:"ref_fit"`
	Reason     string          `json:"reason"`
	DurationMS int64           `json:"duration_ms"`
	Details    json.RawMessage `json:"details,omitempty"`
	JobID      *int64          `json:"job_id"`
	CreatedAt  string          `json:"created_at"`
}

// Job is one queued, running or finished sync of one subtitle.
type Job struct {
	ID         int64             `json:"id"`
	Path       string            `json:"path"`
	VideoPath  string            `json:"video_path"`
	Title      string            `json:"title"`
	Label      string            `json:"label"`
	Lang       string            `json:"lang"`
	Source     string            `json:"source"`
	Priority   int               `json:"priority"`
	Extras     map[string]string `json:"extras"`
	Force      bool              `json:"force"`
	State      string            `json:"state"`
	Stage      string            `json:"stage"`
	RunID      *int64            `json:"run_id"`
	TaskID     *int64            `json:"task_id"`
	Error      string            `json:"error"`
	CreatedAt  string            `json:"created_at"`
	StartedAt  *string           `json:"started_at"`
	FinishedAt *string           `json:"finished_at"`
	Decision   string            `json:"decision,omitempty"` // of the run, when finished
}

// Task is a long-running piece of work with a log (sweep, season tools).
type Task struct {
	ID         int64           `json:"id"`
	Kind       string          `json:"kind"`
	Title      string          `json:"title"`
	State      string          `json:"state"`
	Params     json.RawMessage `json:"params"`
	Summary    json.RawMessage `json:"summary"`
	Log        string          `json:"log,omitempty"`
	Total      int             `json:"total"`
	Done       int             `json:"done"`
	UserID     *int64          `json:"user_id"`
	CreatedAt  string          `json:"created_at"`
	FinishedAt *string         `json:"finished_at"`
}

// Nudge is one manual shift of a subtitle by some milliseconds.
type Nudge struct {
	ID        int64  `json:"id"`
	Path      string `json:"path"`
	MS        int    `json:"ms"`
	Backup    string `json:"backup"`
	Cues      int    `json:"cues"`
	Undone    bool   `json:"undone"`
	UserID    *int64 `json:"user_id"`
	CreatedAt string `json:"created_at"`
}

// Event is something a person (or the app) did to a file besides syncing it.
type Event struct {
	ID        int64  `json:"id"`
	Path      string `json:"path"`
	Kind      string `json:"kind"` // nudge | undo | pin | unpin | restore | verify | rename | reject | quarantine
	Detail    string `json:"detail"`
	UserID    *int64 `json:"user_id"`
	CreatedAt string `json:"created_at"`
}

// Decisions that mean "a person should look at this file".
var AttentionDecisions = []string{"left_unchanged", "error", "timeout", "rejected_wrong_content", "rejected_machine_translation"}

// Decisions that mean the engine never got to answer; worth another go.
var RetryableDecisions = []string{"error", "timeout"}

// IsAttention reports whether a decision needs a person.
func IsAttention(d string) bool {
	for _, x := range AttentionDecisions {
		if x == d {
			return true
		}
	}
	return false
}
