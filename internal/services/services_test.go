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

// Normal work uses the SDK's credential cache; ForFresh asks Core every time.
// Modes are the modes in effect for the admin, defaults included.
func TestCacheFreshAndModes(t *testing.T) {
	n := 0
	cr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		_ = json.NewEncoder(w).Encode(map[string]string{"url": "http://bazarr:6767", "key": "k", "source": "box"})
	}))
	defer cr.Close()
	r := &Resolver{Core: core.New(cr.URL, core.Options{Token: "pca_x"}), Owner: func(context.Context) string { return "admin@example.com" }}
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := r.For(ctx, store.Settings{}, Bazarr); err != nil {
			t.Fatal(err)
		}
	}
	if n != 1 {
		t.Fatalf("Core asked %d times, want once (cached)", n)
	}
	if _, _, err := r.BazarrFresh(ctx, store.Settings{}); err != nil || n != 2 {
		t.Fatalf("fresh: %v, Core asked %d times", err, n)
	}
	modes := r.Modes(ctx, store.Settings{JellyfinURL: "http://jf", JellyfinAPIKey: "saved"})
	if len(modes) != 2 || modes[0] != (core.ConnMode{User: "admin@example.com", Target: "service:bazarr", Mode: ModeCore}) ||
		modes[1] != (core.ConnMode{User: "admin@example.com", Target: "service:jellyfin", Mode: ModeDirect}) {
		t.Fatalf("modes %+v", modes)
	}
	if (&Resolver{Core: r.Core}).Modes(ctx, store.Settings{}) != nil {
		t.Fatal("no admin: nothing to report")
	}
	var none *Resolver
	if err := none.Report(ctx, store.Settings{}); err != nil {
		t.Fatal("no Core: Report is a no-op")
	}
}
