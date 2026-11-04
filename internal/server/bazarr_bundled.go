package server

import (
	"context"
	"os"
	"regexp"
	"time"

	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/store"
)

// ---- The Bazarr that comes with Pocket Subtitles (BAZARR_CONFIG_FILE) ----
//
// The sample compose file can start Bazarr next to Pocket Subtitles (the
// "bazarr" profile) and mounts its config folder read-only here. Bazarr makes
// its API key on its first start, often after this app's, so the key is taken
// from its config.yaml when it appears — once: a Disconnect under Settings is
// not undone. Bazarr itself (Sonarr, Radarr, subtitle providers) is still set
// up in Bazarr.

var bazarrAPIKey = regexp.MustCompile(`(?m)^auth:[ \t]*\r?\n(?:[ \t]+.*\r?\n)*?[ \t]+apikey:[ \t]*['"]?([A-Za-z0-9]{16,})`)

func readBazarrKey(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if m := bazarrAPIKey.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return ""
}

func (s *Server) adoptBazarrKey(ctx context.Context) {
	const done = "bazarr_key_adopted"
	wait := 5 * time.Second
	for {
		if s.store.GetSetting(ctx, done) != "" {
			return
		}
		if key := readBazarrKey(s.cfg.BazarrConfigFile); key != "" {
			st, err := s.store.GetSettings(ctx)
			if err == nil {
				if st.BazarrAPIKey == "" {
					st.BazarrAPIKey = key
					if st.BazarrURL == "" {
						st.BazarrURL = s.cfg.BazarrURL
					}
					err = s.store.SaveSettings(ctx, st)
				}
				if err == nil {
					_ = s.store.SetSetting(ctx, done, store.TimeString(time.Now()))
					s.log.Info("bazarr api key taken from its config", "file", s.cfg.BazarrConfigFile)
					return
				}
			}
			s.log.Warn("bazarr api key", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if wait < time.Minute {
			wait *= 2
		}
	}
}
