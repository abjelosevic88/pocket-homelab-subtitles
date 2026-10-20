package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"

	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/store"
)

func TestModeDefaults(t *testing.T) {
	for _, c := range []struct {
		stored, key string
		coreOn      bool
		want        string
	}{
		{"", "k", true, ModeDirect}, // a saved key keeps working
		{"", "", true, ModeCore},    // nothing saved, Core is there
		{"", "", false, ModeDirect}, // no Core
		{"off", "k", true, ModeOff}, // the choice wins
		{"core", "k", true, ModeCore},
		{"bogus", "", true, ModeCore},
	} {
		if got := Mode(c.stored, c.key, c.coreOn); got != c.want {
			t.Errorf("Mode(%q, %q, %v) = %s, want %s", c.stored, c.key, c.coreOn, got, c.want)
		}
	}
}

// The worker's Jellyfin refresh after a sync goes through the resolver: with
// Core, Core's URL and key (X-Emby-Token); a nil resolver (no server) knows
// only the saved connection.
func TestJellyfinRefreshThroughResolver(t *testing.T) {
	var got []string
	jf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Path+" "+r.Header.Get("X-Emby-Token"))
	}))
	defer jf.Close()
	cr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/services/credential" || r.Header.Get("Authorization") != "Bearer pca_x" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"url": jf.URL, "key": "core-key", "source": "personal"})
	}))
	defer cr.Close()
	r := &Resolver{Core: core.New(cr.URL, core.Options{Token: "pca_x"}), Owner: func(context.Context) string { return "admin@example.com" }}
	ctx := context.Background()
	c, conn, err := r.Jellyfin(ctx, store.Settings{})
	if err != nil || conn.Via != ModeCore || conn.Source != "personal" {
		t.Fatalf("core: %v %+v", err, conn)
	}
	if err := c.Refresh(ctx, "/media/x/a.srt"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "/Library/Media/Updated core-key" {
		t.Fatalf("jellyfin saw %v", got)
	}
	if st := r.StatusOf(ctx, store.Settings{JellyfinMode: ModeOff}, Jellyfin); st.Mode != ModeOff || st.Via != "" {
		t.Fatalf("off status %+v", st)
	}

	var none *Resolver
	if c, _, err := none.Jellyfin(ctx, store.Settings{}); c != nil || err == nil {
		t.Fatal("nil resolver without a saved key must not connect")
	}
	c, conn, err = none.Jellyfin(ctx, store.Settings{JellyfinURL: jf.URL, JellyfinAPIKey: "saved"})
	if err != nil || conn.Via != ModeDirect {
		t.Fatalf("nil resolver direct: %v", err)
	}
	_ = c.Refresh(ctx, "/media/x/a.srt")
	if got[len(got)-1] != "/Library/Media/Updated saved" {
		t.Fatalf("direct used %v", got[len(got)-1])
	}
}
