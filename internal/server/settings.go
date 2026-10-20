package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/services"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/store"
)

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.GetSettings(r.Context())
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), st))
}

// publicSettings are the settings plus how Bazarr and Jellyfin are reached:
// *_mode (in effect), *_via (core | direct | "" = not connected), *_source
// (whose key Core handed out last: personal | box), *_detail (why not) and
// services_as (the admin the app acts as through Pocket Core).
func (s *Server) publicSettings(ctx context.Context, st store.Settings) map[string]any {
	b, _ := json.Marshal(st)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, typ := range []string{services.Bazarr, services.Jellyfin} {
		cs := s.svc.StatusOf(ctx, st, typ)
		m[typ+"_mode"], m[typ+"_via"], m[typ+"_source"], m[typ+"_detail"] = cs.Mode, cs.Via, cs.Source, cs.Detail
		if cs.As != "" {
			m["services_as"] = cs.As
		}
	}
	if _, ok := m["services_as"]; !ok {
		m["services_as"] = s.ownerEmail(ctx)
	}
	m["core_configured"] = s.core.Configured()
	return m
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	if u := userFrom(r.Context()); u == nil || u.Role != "admin" {
		writeErr(w, http.StatusForbidden, "only an admin can change settings")
		return
	}
	cur, err := s.store.GetSettings(r.Context())
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	in := cur
	var keys struct {
		BazarrAPIKey   *string `json:"bazarr_api_key"`
		JellyfinAPIKey *string `json:"jellyfin_api_key"`
	}
	body, err := readBody(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := unmarshal(body, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	_ = unmarshal(body, &keys)
	// API keys are write-only: absent/null keeps the stored one, "" clears it.
	in.BazarrAPIKey, in.JellyfinAPIKey = cur.BazarrAPIKey, cur.JellyfinAPIKey
	if keys.BazarrAPIKey != nil {
		in.BazarrAPIKey = strings.TrimSpace(*keys.BazarrAPIKey)
	}
	if keys.JellyfinAPIKey != nil {
		in.JellyfinAPIKey = strings.TrimSpace(*keys.JellyfinAPIKey)
	}
	in.SetupComplete, in.LastSweepAt = cur.SetupComplete, cur.LastSweepAt
	if !services.ValidMode(in.BazarrMode) {
		in.BazarrMode = cur.BazarrMode
	}
	if !services.ValidMode(in.JellyfinMode) {
		in.JellyfinMode = cur.JellyfinMode
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		in.Title = store.DefaultTitle
	}
	switch {
	case !store.ValidDateFormat(in.DateFormat) || !store.ValidTimeFormat(in.TimeFormat):
		writeErr(w, http.StatusBadRequest, "unknown date or time format")
		return
	case in.LeadMS < -2000 || in.LeadMS > 2000:
		writeErr(w, http.StatusBadRequest, "reading lead must be between -2000 and 2000 ms")
		return
	case !store.ValidTime(in.SweepTime):
		writeErr(w, http.StatusBadRequest, "sweep time must be HH:MM")
		return
	case in.SweepBudget < 5 || in.SweepBudget > 24*60:
		writeErr(w, http.StatusBadRequest, "sweep budget must be 5–1440 minutes")
		return
	case in.VerifyTolMS < 100 || in.VerifyTolMS > 5000:
		writeErr(w, http.StatusBadRequest, "verification tolerance must be 100–5000 ms")
		return
	case in.OnReject != "blacklist" && in.OnReject != "quarantine" && in.OnReject != "keep":
		writeErr(w, http.StatusBadRequest, "on_reject must be blacklist, quarantine or keep")
		return
	case in.NotifyKind != "" && in.NotifyKind != "apprise" && in.NotifyKind != "ntfy" && in.NotifyKind != "webhook":
		writeErr(w, http.StatusBadRequest, "notify_kind must be apprise, ntfy or webhook")
		return
	case in.ReverifyFrom != "" && !validDate(in.ReverifyFrom):
		writeErr(w, http.StatusBadRequest, "re-verify date must be YYYY-MM-DD")
		return
	}
	if err := s.store.SaveSettings(r.Context(), in); err != nil {
		s.fail(w, err, "save settings")
		return
	}
	// a switch or a Disconnect: Core's Links page follows right away
	changed := in.BazarrMode != cur.BazarrMode || in.JellyfinMode != cur.JellyfinMode
	for _, typ := range []string{services.Bazarr, services.Jellyfin} {
		changed = changed || s.svc.ModeOf(in, typ) != s.svc.ModeOf(cur, typ)
	}
	disconnect := (cur.BazarrAPIKey != "" && in.BazarrAPIKey == "") || (cur.JellyfinAPIKey != "" && in.JellyfinAPIKey == "") ||
		(cur.BazarrURL != "" && in.BazarrURL == "") || (cur.JellyfinURL != "" && in.JellyfinURL == "")
	if changed || disconnect {
		go s.reportModes(context.Background())
	}
	s.wantedMu.Lock()
	s.wanted.at = time.Time{}
	s.wantedMu.Unlock()
	st, _ := s.store.GetSettings(r.Context())
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), st))
}

// viaText says how a test reached the service.
func viaText(c *services.Conn) string {
	if c.Via != services.ModeCore {
		return ""
	}
	switch c.Source {
	case "personal":
		return " through Pocket Core (the admin's own key)"
	case "box":
		return " through Pocket Core (the box's key)"
	}
	return " through Pocket Core"
}

func validDate(v string) bool {
	_, err := time.Parse("2006-01-02", v)
	return err == nil
}

func (s *Server) handleTestIntegration(w http.ResponseWriter, r *http.Request) {
	st, _ := s.store.GetSettings(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	switch chi.URLParam(r, "what") {
	case "bazarr":
		// the saved mode decides: Pocket Core (asked afresh, not the cached
		// credential), the URL and key here, or off
		c, conn, err := s.svc.BazarrFresh(ctx, st)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		v, err := c.Status(ctx)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		_, e, m, err := c.Wanted(ctx, 1)
		msg := "Connected to Bazarr " + v + viaText(conn)
		if err == nil {
			msg += fmt.Sprintf(" — %d episodes and %d movies are missing subtitles", e, m)
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": msg, "via": conn.Via, "source": conn.Source})
	case "jellyfin":
		c, conn, err := s.svc.JellyfinFresh(ctx, st)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		v, err := c.Info(ctx)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": "Connected to " + v + viaText(conn), "via": conn.Via, "source": conn.Source})
	case "notify":
		if st.NotifyKind == "" || st.NotifyURL == "" {
			writeErr(w, http.StatusBadRequest, "pick a notification type and URL first")
			return
		}
		if err := notify.Send(ctx, st.NotifyKind, st.NotifyURL, "Pocket Subtitles", "Test notification — this is where failures will show up.", notify.Success); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": "Sent"})
	default:
		writeErr(w, http.StatusNotFound, "unknown integration")
	}
}

func (s *Server) handleDoctor(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	d, err := s.w.Engine().Doctor(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "errors": []string{err.Error()}})
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func dirSize(p string) (int64, int) {
	var size int64
	n := 0
	_ = filepath.WalkDir(p, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				size += fi.Size()
				n++
			}
		}
		return nil
	})
	return size, n
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	var dbBytes int64
	if fi, err := os.Stat(s.cfg.DBPath); err == nil {
		dbBytes = fi.Size()
	}
	cache, cacheN := dirSize(s.cfg.CacheDir)
	backups, backupsN := dirSize(s.cfg.BackupDir)
	tz, _ := time.Now().Zone()
	writeJSON(w, http.StatusOK, map[string]any{
		"version": Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"core":      s.coreStatus(),
		"auth_mode": s.cfg.AuthMode, "legacy_api": s.cfg.LegacyAPI, "db_path": s.cfg.DBPath, "db_bytes": dbBytes,
		"media_dirs": s.cfg.MediaDirs, "cache_dir": s.cfg.CacheDir, "cache_bytes": cache, "cache_files": cacheN,
		"backup_dir": s.cfg.BackupDir, "backup_bytes": backups, "backup_files": backupsN, "workers": s.cfg.Workers,
		"timezone": tz + " (" + time.Local.String() + ")", "uptime": time.Since(s.started).Round(time.Second).String(),
		"counts": s.store.Counts(r.Context()), "base_url": s.cfg.BaseURL, "port": s.cfg.Port,
	})
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if u := userFrom(r.Context()); u == nil || u.Role != "admin" {
		writeErr(w, http.StatusForbidden, "only an admin can download the database")
		return
	}
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("pocket-subtitles-%d.db", time.Now().UnixNano()))
	defer os.Remove(tmp)
	if err := s.store.Backup(r.Context(), tmp); err != nil {
		s.fail(w, err, "backup")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="pocket-subtitles-%s.db"`, time.Now().Format("20060102-150405")))
	http.ServeFile(w, r, tmp)
}
