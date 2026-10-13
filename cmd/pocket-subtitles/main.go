// Command pocket-subtitles keeps a media library's subtitles in sync with the
// audio: it re-times every subtitle Bazarr downloads, sweeps the library each
// night, and gives you a place to see what happened and to nudge a file by
// hand. One binary plus the bundled sync engine, one SQLite file, one volume.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/config"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/engine"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/library"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/server"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/store"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/worker"
	"github.com/abjelosevic88/pocket-homelab-subtitles/web"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Println("pocket-subtitles", version)
			return
		case "backup":
			runBackup(os.Args[2:])
			return
		case "reset-password":
			runResetPassword(os.Args[2:])
			return
		case "import-legacy":
			runImportLegacy(os.Args[2:])
			return
		case "scan":
			runScan()
			return
		case "help", "--help", "-h":
			fmt.Println(`pocket-subtitles — keep subtitles in sync with the audio

Usage:
  pocket-subtitles                     start the server (default)
  pocket-subtitles scan                walk MEDIA_DIRS once and print what was found
  pocket-subtitles backup [-o FILE]    write a consistent SQLite backup
  pocket-subtitles reset-password [-email USER] [-password NEW]
                                       set a user's password (default: first user, random password printed)
  pocket-subtitles import-legacy -dir DIR [-map OLD=NEW] [-backups OLD=NEW] [-reverify-before YYYY-MM-DD]
                                       import the history, ledger, pins and nudges of the
                                       sync_hook.py scripts this app grew out of
  pocket-subtitles version             print version

Configuration is read from environment variables, see docs/configuration.md.`)
			return
		}
	}
	serve()
}

func newLogger(cfg *config.Config) *slog.Logger {
	var level slog.Level
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	if cfg.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}

func mustLoad() (*config.Config, *slog.Logger, *store.Store) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	log := newLogger(cfg)
	st, err := store.Open(cfg.DBPath, log)
	if err != nil {
		log.Error("open database", "path", cfg.DBPath, "err", err)
		os.Exit(1)
	}
	return cfg, log, st
}

func newWorker(cfg *config.Config, st *store.Store, log *slog.Logger) *worker.Worker {
	eng := &engine.Engine{Python: cfg.EnginePython, Script: cfg.EngineScript, CacheDir: cfg.CacheDir, Alass: cfg.AlassBin, FFLibs: cfg.FFSubsyncLib, Nice: cfg.Nice}
	return worker.New(cfg, st, eng, library.New(cfg.MediaDirs), log)
}

func serve() {
	server.Version = version
	cfg, log, st := mustLoad()
	defer st.Close()
	for _, d := range []string{cfg.CacheDir, cfg.BackupDir, cfg.RejectDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			log.Error("create directory", "dir", d, "err", err)
			os.Exit(1)
		}
	}
	for _, d := range cfg.MediaDirs {
		if _, err := os.Stat(d); err != nil && !cfg.DemoData {
			log.Warn("media folder not found — mount your library and set MEDIA_DIRS", "dir", d)
		}
	}

	var webFS fs.FS
	if sub, err := fs.Sub(web.Dist, "dist"); err == nil {
		if f, err := sub.Open("index.html"); err == nil {
			f.Close()
			webFS = sub
		}
	}
	if webFS == nil {
		log.Warn("web UI not embedded; API only (run `make web` before building)")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	w := newWorker(cfg, st, log)
	srv := server.New(cfg, st, w, log, webFS)
	if err := srv.Bootstrap(ctx); err != nil {
		log.Error("bootstrap", "err", err)
		os.Exit(1)
	}
	w.Start(ctx)

	httpSrv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		log.Info("pocket-subtitles started", "version", version, "addr", cfg.Addr(), "media", cfg.MediaDirs, "db", cfg.DBPath,
			"auth_mode", cfg.AuthMode, "workers", cfg.Workers, "legacy_api", cfg.LegacyAPI)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, c2 := context.WithTimeout(context.Background(), 15*time.Second)
	defer c2()
	_ = httpSrv.Shutdown(shutdownCtx)
	time.Sleep(500 * time.Millisecond) // let a cancelled job put itself back in the queue
}

func runScan() {
	cfg, log, st := mustLoad()
	defer st.Close()
	w := newWorker(cfg, st, log)
	r, err := w.Scan(context.Background())
	if err != nil {
		log.Error("scan", "err", err)
		os.Exit(1)
	}
	fmt.Printf("%d subtitle files, %d new\n", r.Files, r.Added)
}

func runBackup(args []string) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	out := fs.String("o", "", "output file (default: DATA_DIR/backups/db/pocket-subtitles-<timestamp>.db)")
	_ = fs.Parse(args)
	cfg, log, st := mustLoad()
	defer st.Close()
	dst := *out
	if dst == "" {
		dst = fmt.Sprintf("%s/db/pocket-subtitles-%s.db", cfg.BackupDir, time.Now().Format("20060102-150405"))
	}
	if err := st.Backup(context.Background(), dst); err != nil {
		log.Error("backup failed", "err", err)
		os.Exit(1)
	}
	fmt.Println(dst)
}

func runResetPassword(args []string) {
	fs := flag.NewFlagSet("reset-password", flag.ExitOnError)
	email := fs.String("email", "", "user email (default: the first user)")
	password := fs.String("password", "", "new password (default: generate a random one)")
	_ = fs.Parse(args)
	_, log, st := mustLoad()
	defer st.Close()
	ctx := context.Background()
	var u *store.User
	var err error
	if *email != "" {
		u, err = st.GetUserByEmail(ctx, *email)
	} else {
		var users []store.User
		users, err = st.ListUsers(ctx)
		if err == nil && len(users) > 0 {
			u = &users[0]
		} else if err == nil {
			err = errors.New("no users found")
		}
	}
	if err != nil {
		log.Error("reset-password: user lookup failed", "err", err)
		os.Exit(1)
	}
	pw := *password
	if pw == "" {
		const alphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			log.Error("reset-password: random", "err", err)
			os.Exit(1)
		}
		for i := range b {
			b[i] = alphabet[int(b[i])%len(alphabet)]
		}
		pw = string(b)
	}
	if len(pw) < 8 {
		log.Error("reset-password: password must be at least 8 characters")
		os.Exit(1)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		log.Error("reset-password: hash", "err", err)
		os.Exit(1)
	}
	u.PasswordHash = string(h)
	if err := st.UpdateUser(ctx, u); err != nil {
		log.Error("reset-password: save", "err", err)
		os.Exit(1)
	}
	if *password == "" {
		fmt.Printf("Password for %s reset.\nTemporary password: %s\nChange it under Settings → Profile after logging in.\n", u.Email, pw)
	} else {
		fmt.Printf("Password for %s reset.\n", u.Email)
	}
}
