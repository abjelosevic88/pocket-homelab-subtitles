// Package server wires the HTTP API, auth and the embedded SPA.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/crypto/bcrypt"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/config"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/store"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/worker"
)

// Version is injected at build time.
var Version = "dev"

// Server holds dependencies.
type Server struct {
	cfg      *config.Config
	store    *store.Store
	w        *worker.Worker
	log      *slog.Logger
	webFS    fs.FS
	started  time.Time
	requests atomic.Int64
	errors   atomic.Int64

	wantedMu sync.Mutex
	wanted   wantedCache

	core     *core.Client
	coreMu   sync.Mutex
	coreOK   bool
	coreErr  string
	resolved map[string]resolvedURL
}

type resolvedURL struct {
	url string
	at  time.Time
}

type wantedCache struct {
	at       time.Time
	episodes int
	movies   int
	ok       bool
}

// New creates a server.
func New(cfg *config.Config, st *store.Store, w *worker.Worker, log *slog.Logger, webFS fs.FS) *Server {
	s := &Server{cfg: cfg, store: st, w: w, log: log, webFS: webFS, started: time.Now(),
		core:     core.New(cfg.CoreURL, core.Options{TokenFile: cfg.DataDir + "/core.token"}),
		resolved: map[string]resolvedURL{}}
	if s.core.Configured() && w != nil {
		// every finished sync (success or failure) becomes a Core event
		w.OnRun = s.publishRun
	}
	return s
}

// Bootstrap seeds the first admin, integration settings from the environment,
// and demo data.
func (s *Server) Bootstrap(ctx context.Context) error {
	n, err := s.store.CountUsers(ctx)
	if err != nil {
		return err
	}
	st, _ := s.store.GetSettings(ctx)
	if n == 0 && s.cfg.AuthMode == "local" && s.cfg.AdminEmail != "" && s.cfg.AdminPassword != "" {
		if len(s.cfg.AdminPassword) < 8 {
			return errors.New("ADMIN_PASSWORD must be at least 8 characters")
		}
		h, _ := bcrypt.GenerateFromPassword([]byte(s.cfg.AdminPassword), bcrypt.DefaultCost)
		u := &store.User{Email: s.cfg.AdminEmail, Name: s.cfg.AdminName, PasswordHash: string(h), Role: "admin"}
		if err := s.store.CreateUser(ctx, u); err != nil {
			return err
		}
		st.SetupComplete = true
		s.log.Info("admin user created from environment", "email", u.Email)
	}
	// Integration settings from the environment fill empty fields only, so
	// what is saved under Settings always wins.
	seed := func(dst *string, v string) {
		if *dst == "" && v != "" {
			*dst = v
		}
	}
	seed(&st.BazarrURL, s.cfg.BazarrURL)
	seed(&st.BazarrAPIKey, s.cfg.BazarrAPIKey)
	seed(&st.BazarrPathMap, s.cfg.BazarrPathMap)
	seed(&st.JellyfinURL, s.cfg.JellyfinURL)
	seed(&st.JellyfinAPIKey, s.cfg.JellyfinAPIKey)
	seed(&st.JellyfinPathMap, s.cfg.JellyfinPathMap)
	if err := s.store.SaveSettings(ctx, st); err != nil {
		return err
	}
	if s.cfg.DemoData {
		if err := s.seedDemo(ctx); err != nil {
			return err
		}
	}
	_ = s.store.PurgeSessions(ctx)
	_ = s.store.PruneJobs(ctx, store.TimeString(time.Now().AddDate(0, 0, -14)))
	if s.core.Configured() {
		go s.coreLoop(ctx)
	}
	return nil
}

// Router builds the chi router.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	if s.cfg.TrustProxy {
		r.Use(middleware.RealIP)
	}
	r.Use(middleware.RequestID)
	r.Use(s.logging)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))

	r.Get("/healthz", s.handleHealth)
	r.Get("/readyz", s.handleReady)
	r.Get("/api/health", s.handleHealth)
	if s.cfg.MetricsEnabled {
		r.Get("/metrics", s.handleMetrics)
	}
	s.legacyRoutes(r)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(middleware.Timeout(120 * time.Second))
		r.Post("/auth/login", s.handleLogin)
		r.Post("/auth/setup", s.handleSetup)
		r.Get("/auth/status", s.handleAuthStatus)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Post("/auth/logout", s.handleLogout)
			r.Get("/auth/me", s.handleMe)
			r.Put("/auth/me", s.handleUpdateMe)

			r.Get("/overview", s.handleOverview)
			r.Get("/widget", s.handleWidget)

			r.Get("/queue", s.handleQueue)
			r.Post("/queue/clear", s.handleQueueClear)
			r.Post("/queue/{id}/cancel", s.handleQueueCancel)
			r.Post("/queue/{id}/bump", s.handleQueueBump)

			r.Get("/runs", s.handleRuns)
			r.Get("/runs/{id}", s.handleRun)
			r.Post("/runs/{id}/restore", s.handleRestoreRejected)

			r.Get("/library/titles", s.handleTitles)
			r.Get("/library/files", s.handleFiles)
			r.Get("/library/langs", s.handleLangs)
			r.Post("/library/scan", s.handleScan)

			r.Get("/files/by-path", s.handleFileByPath)
			r.Route("/files/{id}", func(r chi.Router) {
				r.Get("/", s.handleFile)
				r.Get("/text", s.handleFileText)
				r.Post("/sync", s.handleFileSync)
				r.Post("/nudge", s.handleFileNudge)
				r.Post("/undo", s.handleFileUndo)
				r.Post("/pin", s.handleFilePin)
				r.Post("/unpin", s.handleFileUnpin)
				r.Post("/restore-original", s.handleFileRestore)
				r.Post("/verify", s.handleFileVerify)
			})
			r.Get("/nudges", s.handleRecentNudges)

			r.Post("/sync", s.handleSync)
			r.Post("/hooks/bazarr", s.handleBazarrHook)

			r.Get("/tasks", s.handleTasks)
			r.Get("/tasks/{id}", s.handleTask)
			r.Post("/tasks/{id}/cancel", s.handleTaskCancel)
			r.Get("/sweep/plan", s.handleSweepPlan)
			r.Post("/sweep", s.handleSweepStart)
			r.Post("/seasons", s.handleSeason)

			r.Get("/issues/missing", s.handleMissing)
			r.Post("/issues/missing/search", s.handleMissingSearch)

			r.Get("/users", s.handleListUsers)
			r.Post("/users", s.handleCreateUser)
			r.Put("/users/{id}", s.handleUpdateUser)
			r.Delete("/users/{id}", s.handleDeleteUser)

			r.Get("/settings", s.handleGetSettings)
			r.Put("/settings", s.handlePutSettings)
			r.Get("/settings/system", s.handleSystemInfo)
			r.Post("/settings/test/{what}", s.handleTestIntegration)
			r.Get("/doctor", s.handleDoctor)

			r.Get("/tokens", s.handleListTokens)
			r.Post("/tokens", s.handleCreateToken)
			r.Delete("/tokens/{id}", s.handleDeleteToken)

			r.Get("/backup", s.handleBackup)
		})
	})

	r.NotFound(s.handleSPA)
	return r
}

// ---------- helpers ----------

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		s.requests.Add(1)
		if ww.Status() >= 500 {
			s.errors.Add(1)
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			return
		}
		s.log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", ww.Status(), "dur", time.Since(start).String(), "ip", r.RemoteAddr)
	})
}

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiError{Error: msg})
}

func (s *Server) fail(w http.ResponseWriter, err error, context string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
		return
	case errors.Is(err, store.ErrConflict):
		writeErr(w, http.StatusConflict, "already exists")
		return
	case errors.Is(err, worker.ErrOutsideLibrary):
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, worker.ErrBusy):
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.log.Error(context, "err", err)
	writeErr(w, http.StatusInternalServerError, context+": "+err.Error())
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8<<20))
	return dec.Decode(v)
}

func idParam(r *http.Request, name string) int64 {
	n, _ := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	return n
}

func queryInt(r *http.Request, name string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(name))
	return n
}

func queryBool(r *http.Request, name string) bool {
	v := strings.ToLower(r.URL.Query().Get(name))
	return v == "1" || v == "true" || v == "yes"
}

func userIDPtr(r *http.Request) *int64 {
	if u := userFrom(r.Context()); u != nil {
		id := u.ID
		return &id
	}
	return nil
}

// ---- Pocket Core ----

// coreLoop registers with Core on start (retrying until it answers) and
// re-registers every 30 minutes so version and last-seen stay fresh.
func (s *Server) coreLoop(ctx context.Context) {
	wait := 5 * time.Second
	for {
		err := s.registerWithCore(ctx)
		s.coreMu.Lock()
		s.coreOK, s.coreErr = err == nil, ""
		if err != nil {
			s.coreErr = err.Error()
		}
		s.coreMu.Unlock()
		if err != nil {
			s.log.Warn("pocket core: registration failed, retrying", "err", err, "in", wait.String())
		} else {
			wait = 30 * time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if err != nil && wait < 5*time.Minute {
			wait *= 2
		}
	}
}

func (s *Server) registerWithCore(ctx context.Context) error {
	reg, err := s.core.Register(ctx, s.cfg.CoreRegistrationKey, core.Manifest{
		Slug: "subtitles", Name: "Pocket Subtitles",
		Description: "Keeps your library's subtitles in sync with the audio: every download from Bazarr re-timed automatically, the library swept every night, any file nudged by hand.",
		Icon:        "/favicon.svg", Version: Version,
		URL: s.cfg.CoreAppURL, PublicURL: s.cfg.BaseURL, HealthPath: "/healthz", EnvPrefix: "SUBSYNC",
		Publishes: []string{"subtitles.synced", "subtitles.failed", "subtitles.nudged"},
	})
	if err != nil {
		return err
	}
	if reg.Token != "" {
		s.log.Info("pocket core: registered, app token stored", "core", s.cfg.CoreURL)
	} else {
		s.log.Debug("pocket core: re-registered", "core", s.cfg.CoreURL)
	}
	return nil
}

// publish sends an event to Core without blocking the request.
func (s *Server) publish(topic, subject string, payload map[string]any) {
	if !s.core.Configured() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.core.Publish(ctx, topic, subject, payload); err != nil {
			s.log.Warn("pocket core: publish failed", "topic", topic, "err", err)
		}
	}()
}

// publishRun is the worker's OnRun hook: a sync that re-timed a file becomes
// subtitles.synced, one that needs a person becomes subtitles.failed. Verdicts
// that changed nothing (in sync, pinned, skipped) are not events.
func (s *Server) publishRun(sub *store.Subtitle, run *store.Run) {
	if sub == nil || run == nil {
		return
	}
	payload := map[string]any{
		"file_id": sub.ID, "run_id": run.ID, "title": sub.Title, "label": sub.Label, "lang": sub.Lang, "name": run.Name,
		"source": run.Source, "decision": run.Decision, "reason": run.Reason, "method": run.Method, "shift": run.MaxShift, "at": run.CreatedAt,
	}
	switch {
	case run.Decision == "synced":
		s.publish("subtitles.synced", fmt.Sprintf("file:%d", sub.ID), payload)
	case store.IsAttention(run.Decision):
		s.publish("subtitles.failed", fmt.Sprintf("file:%d", sub.ID), payload)
	}
}

// publishNudge announces a hand nudge (the file is pinned by it).
func (s *Server) publishNudge(sub *store.Subtitle, res *worker.NudgeResult) {
	if sub == nil || res == nil {
		return
	}
	s.publish("subtitles.nudged", fmt.Sprintf("file:%d", sub.ID), map[string]any{
		"file_id": sub.ID, "title": sub.Title, "label": sub.Label, "lang": sub.Lang, "name": filepath.Base(sub.Path),
		"ms": res.MS, "total_ms": res.TotalMS, "cues": res.Cues, "pinned": res.Pinned,
	})
}

// resolveSibling asks Core where a sibling app lives (cached a minute) so the
// *_URL settings may stay empty on a box with Core.
func (s *Server) resolveSibling(ctx context.Context, slug string) string {
	if !s.core.Configured() {
		return ""
	}
	s.coreMu.Lock()
	if r, ok := s.resolved[slug]; ok && time.Since(r.at) < time.Minute {
		s.coreMu.Unlock()
		return r.url
	}
	s.coreMu.Unlock()
	url := ""
	if res, err := s.core.Resolve(ctx, slug); err == nil {
		url = res.URL
	}
	s.coreMu.Lock()
	s.resolved[slug] = resolvedURL{url: url, at: time.Now()}
	s.coreMu.Unlock()
	return url
}

// coreStatus summarises the Pocket Core link for the About page.
func (s *Server) coreStatus() map[string]any {
	s.coreMu.Lock()
	defer s.coreMu.Unlock()
	return map[string]any{"configured": s.core.Configured(), "url": s.cfg.CoreURL, "registered": s.coreOK, "error": s.coreErr}
}

// ---------- health / metrics ----------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": Version, "uptime": time.Since(s.started).Round(time.Second).String()})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DB.PingContext(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	runs, _ := s.store.GetRunStats(ctx)
	lib, _ := s.store.GetLibraryStats(ctx)
	queued, running, _ := s.store.QueueCounts(ctx)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	p := func(name, help, typ string, v any, labels string) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n%s%s %v\n", name, help, name, typ, name, labels, v)
	}
	p("pocket_subtitles_info", "Build info.", "gauge", 1, fmt.Sprintf("{version=%q}", Version))
	p("pocket_subtitles_uptime_seconds", "Process uptime.", "gauge", int(time.Since(s.started).Seconds()), "")
	p("pocket_subtitles_http_requests_total", "HTTP requests served.", "counter", s.requests.Load(), "")
	p("pocket_subtitles_runs_total", "Engine runs recorded.", "counter", runs.Processed, "")
	p("pocket_subtitles_runs_adjusted_total", "Runs that re-timed a file.", "counter", runs.Adjusted, "")
	p("pocket_subtitles_queue_length", "Jobs waiting.", "gauge", queued, "")
	p("pocket_subtitles_running", "Jobs running.", "gauge", running, "")
	p("pocket_subtitles_files", "Subtitle files in the library.", "gauge", lib.Files, "")
	p("pocket_subtitles_attention", "Files whose last verdict needs a person.", "gauge", lib.Attention, "")
	p("pocket_subtitles_pinned", "Pinned files.", "gauge", lib.Pinned, "")
}

// ---------- static / SPA ----------

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if s.webFS == nil {
		http.Error(w, "web UI not built (run `make web`)", http.StatusNotFound)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if f, err := s.webFS.Open(path); err == nil {
		f.Close()
		if strings.HasPrefix(path, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.FileServerFS(s.webFS).ServeHTTP(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	r.URL.Path = "/"
	http.FileServerFS(s.webFS).ServeHTTP(w, r)
}
