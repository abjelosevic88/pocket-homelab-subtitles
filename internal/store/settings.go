package store

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

// DefaultTitle is the app title until someone changes it.
const DefaultTitle = "Pocket Subtitles"

// Settings are edited at runtime under Settings. API keys never leave the
// server: the JSON carries only whether one is set.
type Settings struct {
	Title         string `json:"title"`
	DateFormat    string `json:"date_format"` // auto | dmy-dot | dmy-slash | mdy-slash | ymd | dmmmy | mmmdy
	TimeFormat    string `json:"time_format"` // auto | 24h | 12h
	SetupComplete bool   `json:"setup_complete"`

	// Sync behaviour.
	LeadMS        int    `json:"lead_ms"`         // cues land this long AFTER the voice
	CentreRef     bool   `json:"centre_ref"`      // try the 5.1 centre channel as speech reference
	Polish        bool   `json:"polish"`          // apply the short-block polish pass
	RefResidual   bool   `json:"ref_residual"`    // re-measure alass output against the audio
	ContentGate   bool   `json:"content_gate"`    // reject subtitles whose text is for another video
	MTGateLangs   string `json:"mt_gate_langs"`   // reject machine translations of fresh downloads in these languages
	LatinizeLangs string `json:"latinize_langs"`  // convert Serbian Cyrillic to Latin for these languages
	OnReject      string `json:"on_reject"`       // blacklist | quarantine | keep
	VerifyTolMS   int    `json:"verify_tol_ms"`   // independent check: allowed distance from the target
	Languages     string `json:"languages"`       // only manage these languages (empty = all)
	AutoUnpinNew  bool   `json:"auto_unpin_new"`  // a fresh download replaces a pinned file: unpin it
	SweepEnabled  bool   `json:"sweep_enabled"`   // nightly sweep over the library
	SweepTime     string `json:"sweep_time"`      // HH:MM, server time zone
	SweepBudget   int    `json:"sweep_budget"`    // minutes; leftovers wait for the next night
	ReverifyFrom  string `json:"reverify_before"` // verdicts older than this date are re-checked
	LastSweepAt   string `json:"last_sweep_at"`

	// Integrations. *_mode: core (through Pocket Core) | direct (the URL and
	// key here) | off; empty = direct with a saved key, else core when the box
	// has a Pocket Core (see internal/services).
	BazarrMode       string `json:"bazarr_mode"`
	JellyfinMode     string `json:"jellyfin_mode"`
	BazarrURL        string `json:"bazarr_url"`
	BazarrAPIKey     string `json:"-"`
	BazarrKeySet     bool   `json:"bazarr_api_key_set"`
	BazarrPathMap    string `json:"bazarr_path_map"` // bazarr-prefix=our-prefix, comma separated
	JellyfinURL      string `json:"jellyfin_url"`
	JellyfinAPIKey   string `json:"-"`
	JellyfinKeySet   bool   `json:"jellyfin_api_key_set"`
	JellyfinPathMap  string `json:"jellyfin_path_map"` // our-prefix=jellyfin-prefix
	NotifyKind       string `json:"notify_kind"`       // "" | apprise | ntfy | webhook
	NotifyURL        string `json:"notify_url"`
	NotifyFailures   bool   `json:"notify_failures"`
	NotifySynced     bool   `json:"notify_synced"`
	NotifySweep      bool   `json:"notify_sweep"`
	SeasonLangs      string `json:"season_langs"`        // languages a season reset downloads
	SeasonManualLang string `json:"season_manual_langs"` // fetched by manual provider search (finds low-scoring human subs)
	PreferProvider   string `json:"prefer_provider"`     // ranked first in manual searches
}

var hhmm = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// ValidTime reports whether v is HH:MM.
func ValidTime(v string) bool { return hhmm.MatchString(v) }

// Date and time formats the UI can use ("auto" = the browser's locale).
var DateFormats = []string{"auto", "dmy-dot", "dmy-slash", "mdy-slash", "ymd", "dmmmy", "mmmdy"}
var TimeFormats = []string{"auto", "24h", "12h"}

func oneOf(v string, list []string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// ValidDateFormat / ValidTimeFormat report whether v is a known format id.
func ValidDateFormat(v string) bool { return oneOf(v, DateFormats) }
func ValidTimeFormat(v string) bool { return oneOf(v, TimeFormats) }

// DefaultSettings are what a new install starts with.
func DefaultSettings() Settings {
	return Settings{
		Title: DefaultTitle, DateFormat: "auto", TimeFormat: "auto",
		CentreRef: true, Polish: true, ContentGate: true,
		MTGateLangs: "sr,hr,bs", OnReject: "blacklist", VerifyTolMS: 600, AutoUnpinNew: true,
		SweepEnabled: true, SweepTime: "03:30", SweepBudget: 300,
		NotifyFailures: true, NotifySweep: false, SeasonLangs: "en",
	}
}

// GetSettings loads all settings with defaults applied.
func (s *Store) GetSettings(ctx context.Context) (Settings, error) {
	st := DefaultSettings()
	rows, err := s.DB.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return st, err
		}
		b := v == "1"
		n, _ := strconv.Atoi(v)
		switch k {
		case "title":
			if v != "" {
				st.Title = v
			}
		case "date_format":
			if ValidDateFormat(v) {
				st.DateFormat = v
			}
		case "time_format":
			if ValidTimeFormat(v) {
				st.TimeFormat = v
			}
		case "setup_complete":
			st.SetupComplete = b
		case "lead_ms":
			st.LeadMS = n
		case "centre_ref":
			st.CentreRef = b
		case "polish":
			st.Polish = b
		case "ref_residual":
			st.RefResidual = b
		case "content_gate":
			st.ContentGate = b
		case "mt_gate_langs":
			st.MTGateLangs = v
		case "latinize_langs":
			st.LatinizeLangs = v
		case "on_reject":
			if oneOf(v, []string{"blacklist", "quarantine", "keep"}) {
				st.OnReject = v
			}
		case "verify_tol_ms":
			if n > 0 {
				st.VerifyTolMS = n
			}
		case "languages":
			st.Languages = v
		case "auto_unpin_new":
			st.AutoUnpinNew = b
		case "sweep_enabled":
			st.SweepEnabled = b
		case "sweep_time":
			if ValidTime(v) {
				st.SweepTime = v
			}
		case "sweep_budget":
			if n > 0 {
				st.SweepBudget = n
			}
		case "reverify_before":
			st.ReverifyFrom = v
		case "last_sweep_at":
			st.LastSweepAt = v
		case "bazarr_mode":
			st.BazarrMode = v
		case "jellyfin_mode":
			st.JellyfinMode = v
		case "bazarr_url":
			st.BazarrURL = v
		case "bazarr_api_key":
			st.BazarrAPIKey = v
		case "bazarr_path_map":
			st.BazarrPathMap = v
		case "jellyfin_url":
			st.JellyfinURL = v
		case "jellyfin_api_key":
			st.JellyfinAPIKey = v
		case "jellyfin_path_map":
			st.JellyfinPathMap = v
		case "notify_kind":
			st.NotifyKind = v
		case "notify_url":
			st.NotifyURL = v
		case "notify_failures":
			st.NotifyFailures = b
		case "notify_synced":
			st.NotifySynced = b
		case "notify_sweep":
			st.NotifySweep = b
		case "season_langs":
			st.SeasonLangs = v
		case "season_manual_langs":
			st.SeasonManualLang = v
		case "prefer_provider":
			st.PreferProvider = v
		}
	}
	st.BazarrKeySet = st.BazarrAPIKey != ""
	st.JellyfinKeySet = st.JellyfinAPIKey != ""
	return st, rows.Err()
}

// SaveSettings persists all settings.
func (s *Store) SaveSettings(ctx context.Context, st Settings) error {
	b := map[bool]string{true: "1", false: "0"}
	kv := map[string]string{
		"title": st.Title, "date_format": st.DateFormat, "time_format": st.TimeFormat,
		"setup_complete": b[st.SetupComplete],
		"lead_ms":        strconv.Itoa(st.LeadMS), "centre_ref": b[st.CentreRef], "polish": b[st.Polish],
		"ref_residual": b[st.RefResidual], "content_gate": b[st.ContentGate],
		"mt_gate_langs": NormLangs(st.MTGateLangs), "latinize_langs": NormLangs(st.LatinizeLangs),
		"on_reject": st.OnReject, "verify_tol_ms": strconv.Itoa(st.VerifyTolMS),
		"languages": NormLangs(st.Languages), "auto_unpin_new": b[st.AutoUnpinNew],
		"sweep_enabled": b[st.SweepEnabled], "sweep_time": st.SweepTime, "sweep_budget": strconv.Itoa(st.SweepBudget),
		"reverify_before": st.ReverifyFrom, "last_sweep_at": st.LastSweepAt,
		"bazarr_mode": st.BazarrMode, "jellyfin_mode": st.JellyfinMode,
		"bazarr_url": strings.TrimRight(st.BazarrURL, "/"), "bazarr_api_key": st.BazarrAPIKey, "bazarr_path_map": st.BazarrPathMap,
		"jellyfin_url": strings.TrimRight(st.JellyfinURL, "/"), "jellyfin_api_key": st.JellyfinAPIKey, "jellyfin_path_map": st.JellyfinPathMap,
		"notify_kind": st.NotifyKind, "notify_url": st.NotifyURL,
		"notify_failures": b[st.NotifyFailures], "notify_synced": b[st.NotifySynced], "notify_sweep": b[st.NotifySweep],
		"season_langs": NormLangs(st.SeasonLangs), "season_manual_langs": NormLangs(st.SeasonManualLang),
		"prefer_provider": strings.TrimSpace(st.PreferProvider),
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// GetSetting reads one key ("" when unset).
func (s *Store) GetSetting(ctx context.Context, key string) string {
	var v string
	_ = s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	return v
}

// SetSetting writes one key (used for bookkeeping such as last_sweep_at).
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// NormLangs turns "SR, en ,hr" into "sr,en,hr".
func NormLangs(v string) string {
	var out []string
	for _, p := range strings.FieldsFunc(strings.ToLower(v), func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ",")
}

// LangIn reports whether lang is in a comma list ("" never matches).
func LangIn(lang, list string) bool {
	if lang == "" {
		return false
	}
	for _, x := range strings.Split(list, ",") {
		if x == lang {
			return true
		}
	}
	return false
}
