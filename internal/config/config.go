// Package config loads runtime configuration from environment variables.
//
// Every option has a sensible default so that `docker run` with the media
// library and one data volume works out of the box. Settings that are changed
// at runtime (sync behaviour, integrations, schedule) live in the database and
// are edited under Settings; the matching environment variables only seed them
// on first start. See docs/configuration.md for the full list.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime settings.
type Config struct {
	Host           string
	Port           int
	BaseURL        string
	DataDir        string
	DBPath         string
	LogLevel       string
	LogFormat      string // text | json
	TrustProxy     bool
	MetricsEnabled bool

	// Library and storage.
	MediaDirs    []string // library folders, e.g. /media/shows,/media/movies
	CacheDir     string   // speech references and other per-video measurements
	BackupDir    string   // first originals of every changed subtitle, nudge backups
	RejectDir    string   // copies of rejected downloads
	ScanInterval time.Duration

	// Engine.
	EnginePython string
	EngineScript string
	AlassBin     string
	FFSubsyncLib string
	Workers      int
	Nice         int
	JobTimeout   time.Duration

	// LegacyAPI serves the endpoints of the old sync-status-server (GET / as
	// JSON, /nudge/files, POST /nudge/*) for existing clients: off | open | token.
	LegacyAPI string

	AuthMode             string // local | proxy | none
	AuthProxyHeader      string
	AuthProxyEmailHeader string
	SessionTTL           time.Duration
	SecureCookies        bool
	AdminEmail           string
	AdminPassword        string
	AdminName            string

	// Sign in with an OpenID Connect provider. On a box with Pocket Core none
	// of these are needed: the app uses the client Core issues on registration.
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCName         string // button label
	OIDCAutoCreate   bool   // unknown emails get a member account

	// Seeds for the integration settings (first start only).
	BazarrURL       string
	BazarrAPIKey    string
	BazarrPathMap   string
	JellyfinURL     string
	JellyfinAPIKey  string
	JellyfinPathMap string

	// Pocket Core: when CORE_URL is set the app registers itself on start,
	// publishes events and accepts the tokens Core hands to paired phones.
	CoreURL             string
	CoreRegistrationKey string
	CoreAppURL          string // how Core reaches this app; defaults to BASE_URL

	DemoData bool
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func envBool(key string, def bool) bool {
	switch strings.ToLower(env(key, "")) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func envInt(key string, def int) int {
	if v := env(key, ""); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := env(key, ""); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// envSecret reads KEY, or the file named by KEY_FILE (Docker secrets).
func envSecret(key string) string {
	if v := env(key, ""); v != "" {
		return v
	}
	if f := env(key+"_FILE", ""); f != "" {
		if b, err := os.ReadFile(f); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return ""
}

// Load reads configuration from the environment.
func Load() (*Config, error) {
	dataDir := env("DATA_DIR", "./data")
	c := &Config{
		Host:           env("HOST", "0.0.0.0"),
		Port:           envInt("PORT", 8080),
		BaseURL:        strings.TrimRight(env("BASE_URL", ""), "/"),
		DataDir:        dataDir,
		DBPath:         env("DB_PATH", dataDir+"/pocket-subtitles.db"),
		LogLevel:       strings.ToLower(env("LOG_LEVEL", "info")),
		LogFormat:      strings.ToLower(env("LOG_FORMAT", "text")),
		TrustProxy:     envBool("TRUST_PROXY", true),
		MetricsEnabled: envBool("METRICS_ENABLED", true),

		CacheDir:     env("CACHE_DIR", dataDir+"/cache"),
		BackupDir:    env("BACKUP_DIR", dataDir+"/backups"),
		RejectDir:    env("REJECT_DIR", dataDir+"/rejected"),
		ScanInterval: envDuration("SCAN_INTERVAL", 15*time.Minute),

		EnginePython: env("ENGINE_PYTHON", "python3"),
		EngineScript: env("ENGINE_SCRIPT", "/app/engine/pocket_sync.py"),
		AlassBin:     env("ALASS_BIN", "alass-cli"),
		FFSubsyncLib: env("FFSUBSYNC_LIBS", ""),
		Workers:      envInt("WORKERS", 1),
		Nice:         envInt("NICE", 19),
		JobTimeout:   envDuration("JOB_TIMEOUT", time.Hour),

		LegacyAPI: strings.ToLower(env("LEGACY_API", "off")),

		AuthMode:             strings.ToLower(env("AUTH_MODE", "local")),
		AuthProxyHeader:      env("AUTH_PROXY_HEADER", "Remote-User"),
		AuthProxyEmailHeader: env("AUTH_PROXY_EMAIL_HEADER", "Remote-Email"),
		SessionTTL:           envDuration("SESSION_TTL", 720*time.Hour),
		SecureCookies:        envBool("SECURE_COOKIES", false),
		AdminEmail:           strings.ToLower(env("ADMIN_EMAIL", "")),
		AdminPassword:        env("ADMIN_PASSWORD", ""),
		AdminName:            env("ADMIN_NAME", "Admin"),
		OIDCIssuer:           strings.TrimRight(env("OIDC_ISSUER", ""), "/"),
		OIDCClientID:         env("OIDC_CLIENT_ID", ""),
		OIDCClientSecret:     env("OIDC_CLIENT_SECRET", ""),
		OIDCName:             env("OIDC_NAME", "single sign-on"),
		OIDCAutoCreate:       envBool("OIDC_AUTO_CREATE", true),

		BazarrURL:       strings.TrimRight(env("BAZARR_URL", ""), "/"),
		BazarrAPIKey:    envSecret("BAZARR_API_KEY"),
		BazarrPathMap:   env("BAZARR_PATH_MAP", ""),
		JellyfinURL:     strings.TrimRight(env("JELLYFIN_URL", ""), "/"),
		JellyfinAPIKey:  envSecret("JELLYFIN_API_KEY"),
		JellyfinPathMap: env("JELLYFIN_PATH_MAP", ""),

		CoreURL:             strings.TrimRight(env("CORE_URL", ""), "/"),
		CoreRegistrationKey: env("CORE_REGISTRATION_KEY", ""),
		CoreAppURL:          strings.TrimRight(env("CORE_APP_URL", ""), "/"),

		DemoData: envBool("DEMO_DATA", false),
	}
	if c.CoreAppURL == "" {
		c.CoreAppURL = c.BaseURL
	}
	for _, d := range strings.Split(env("MEDIA_DIRS", "/media"), ",") {
		if d = strings.TrimSpace(d); d != "" {
			c.MediaDirs = append(c.MediaDirs, filepath.Clean(d))
		}
	}
	switch c.AuthMode {
	case "local", "proxy", "none":
	default:
		return nil, fmt.Errorf("AUTH_MODE must be one of local, proxy, none (got %q)", c.AuthMode)
	}
	switch c.LegacyAPI {
	case "off", "open", "token":
	default:
		return nil, fmt.Errorf("LEGACY_API must be one of off, open, token (got %q)", c.LegacyAPI)
	}
	if c.Port <= 0 || c.Port > 65535 {
		return nil, fmt.Errorf("PORT out of range: %d", c.Port)
	}
	if c.OIDCIssuer != "" && (c.OIDCClientID == "" || c.BaseURL == "") {
		return nil, fmt.Errorf("OIDC_ISSUER needs OIDC_CLIENT_ID and BASE_URL (the callback is BASE_URL/api/v1/auth/oidc/callback)")
	}
	if c.Workers < 1 {
		c.Workers = 1
	}
	if len(c.MediaDirs) == 0 {
		return nil, fmt.Errorf("MEDIA_DIRS is empty")
	}
	return c, nil
}

// Addr returns host:port.
func (c *Config) Addr() string { return fmt.Sprintf("%s:%d", c.Host, c.Port) }
