package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/config"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/store"
)

// recorder is a fake Bazarr + Jellyfin: it answers the connection tests and
// records which key each request carried.
type recorder struct {
	srv  *httptest.Server
	mu   sync.Mutex
	seen []string // "<path> <key>"
}

func newRecorder(t *testing.T) *recorder {
	rc := &recorder{}
	rc.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-KEY") + r.Header.Get("X-Emby-Token")
		rc.mu.Lock()
		rc.seen = append(rc.seen, r.URL.Path+" "+key)
		rc.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/system/status":
			_, _ = io.WriteString(w, `{"data": {"bazarr_version": "1.5.2"}}`)
		case strings.HasPrefix(r.URL.Path, "/api/episodes/wanted"), strings.HasPrefix(r.URL.Path, "/api/movies/wanted"):
			_, _ = io.WriteString(w, `{"data": [], "total": 4}`)
		case r.URL.Path == "/System/Info":
			_, _ = io.WriteString(w, `{"ServerName": "media", "Version": "10.11.0"}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(rc.srv.Close)
	return rc
}

func (rc *recorder) calls() []string {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return append([]string{}, rc.seen...)
}

// fakeCore answers Pocket Core's service-connection API for the admin.
type fakeCore struct {
	srv    *httptest.Server
	mu     sync.Mutex
	refuse map[string]string // service type → reason
	source string
	asked  []string // "<auth> <user> <type>"
	status int
	modes  map[string]string // "<user> <target>" → last reported mode
	report int               // PUT /links/modes calls
}

func newFakeCore(t *testing.T, target string) *fakeCore {
	fc := &fakeCore{refuse: map[string]string{}, source: "box"}
	fc.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fc.mu.Lock()
		defer fc.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/services/credential":
			var in struct{ User, Type string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			fc.asked = append(fc.asked, r.Header.Get("Authorization")+" "+in.User+" "+in.Type)
			if reason := fc.refuse[in.Type]; reason != "" {
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "refused", "reason": reason})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"type": in.Type, "url": target, "public_url": "https://" + in.Type + ".example.org",
				"key": "core-" + in.Type + "-key", "source": fc.source})
		case "/api/v1/apps/register":
			_ = json.NewEncoder(w).Encode(map[string]any{"app": map[string]any{"slug": "subtitles"}})
		case "/api/v1/links/modes":
			if r.Method != http.MethodPut || r.Header.Get("Authorization") != "Bearer pca_subtitles" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			var in struct {
				Modes []struct{ User, Target, Mode string }
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			if fc.modes == nil {
				fc.modes = map[string]string{}
			}
			for _, m := range in.Modes {
				fc.modes[m.User+" "+m.Target] = m.Mode
			}
			fc.report++
			_ = json.NewEncoder(w).Encode(map[string]any{"recorded": len(in.Modes)})
		case "/api/v1/links/status":
			fc.status++
			typ := strings.TrimPrefix(r.URL.Query().Get("app"), "service:")
			if reason := fc.refuse[typ]; reason != "" {
				_ = json.NewEncoder(w).Encode([]map[string]any{{"app": "service:" + typ, "scope": "account", "state": reason, "on": false}})
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{"app": "service:" + typ, "scope": "account", "state": "on", "on": true}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fc.srv.Close)
	return fc
}

func (fc *fakeCore) credentialCalls() []string {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return append([]string{}, fc.asked...)
}

// reported is the last mode Core heard for the admin and a service, and how many reports came in.
func (fc *fakeCore) reported(typ string) (string, int) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.modes["admin@example.com service:"+typ], fc.report
}

// waitReported waits until Core heard want for the service (reports go out in the background).
func waitReported(t *testing.T, fc *fakeCore, typ, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, _ := fc.reported(typ)
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Core heard %q for %s, want %q", got, typ, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (fc *fakeCore) statusCalls() int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.status
}

type apiClient struct {
	t   *testing.T
	url string
	tok string
}

func (c apiClient) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, c.url+"/api/v1"+path, rd)
	req.Header.Set("Authorization", "Bearer "+c.tok)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func servicesSetup(t *testing.T, coreURL string, mod func(*config.Config)) (*Server, *store.Store, apiClient) {
	t.Helper()
	dir := t.TempDir()
	cfg := testConfig(dir)
	cfg.CoreURL = coreURL
	if mod != nil {
		mod(cfg)
	}
	if err := os.WriteFile(dir+"/core.token", []byte("pca_subtitles"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := openTestStore(t, cfg)
	ctx, cancel := context.WithCancel(context.Background()) // stops the Core loop Bootstrap starts
	t.Cleanup(cancel)
	member := &store.User{Email: "member@example.com", Name: "M", Role: "member", PasswordHash: "x"}
	admin := &store.User{Email: "admin@example.com", Name: "A", Role: "admin", PasswordHash: "x"}
	for _, u := range []*store.User{member, admin} {
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	s := New(cfg, st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	tok, _, err := st.CreateAPIToken(ctx, admin.ID, "t")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Router())
	t.Cleanup(ts.Close)
	return s, st, apiClient{t: t, url: ts.URL, tok: tok}
}

// Bazarr and Jellyfin through Pocket Core: the app acts as the first admin
// (the member created earlier is skipped), uses Core's URL with Core's key in
// each service's own header, and links open Core's public URL.
func TestServicesThroughCore(t *testing.T) {
	rc := newRecorder(t)
	fc := newFakeCore(t, rc.srv.URL)
	s, _, api := servicesSetup(t, fc.srv.URL, nil)

	code, st := api.do("GET", "/settings", nil)
	if code != 200 || st["bazarr_mode"] != "core" || st["bazarr_via"] != "core" || st["jellyfin_mode"] != "core" || st["jellyfin_via"] != "core" || st["services_as"] != "admin@example.com" {
		t.Fatalf("settings: %d %v", code, st)
	}
	if code, res := api.do("POST", "/settings/test/bazarr", nil); code != 200 || res["via"] != "core" || !strings.Contains(res["message"].(string), "the box's key") {
		t.Fatalf("bazarr test: %d %v", code, res)
	}
	if code, res := api.do("POST", "/settings/test/jellyfin", nil); code != 200 || res["via"] != "core" {
		t.Fatalf("jellyfin test: %d %v", code, res)
	}
	calls := rc.calls()
	if len(calls) < 2 || calls[0] != "/api/system/status core-bazarr-key" || calls[len(calls)-1] != "/System/Info core-jellyfin-key" {
		t.Fatalf("services saw %v", calls)
	}
	asked := fc.credentialCalls()
	if len(asked) != 2 || asked[0] != "Bearer pca_subtitles admin@example.com bazarr" || asked[1] != "Bearer pca_subtitles admin@example.com jellyfin" {
		t.Fatalf("core asked %v", asked)
	}
	// the status now knows whose key it was
	if _, st = api.do("GET", "/settings", nil); st["bazarr_source"] != "box" {
		t.Fatalf("bazarr source = %v", st["bazarr_source"])
	}
	// missing subtitles: Core again (tests ask afresh and fill no cache), link to Core's public URL
	code, miss := api.do("GET", "/issues/missing", nil)
	if code != 200 || miss["configured"] != true || miss["bazarr_url"] != "https://bazarr.example.org" {
		t.Fatalf("missing: %d %v", code, miss)
	}
	if n := len(fc.credentialCalls()); n != 3 {
		t.Fatalf("core asked %d times, want 3", n)
	}
	// normal work within the minute reuses the cached credential; a test asks again
	set, _ := s.store.GetSettings(context.Background())
	if _, err := s.svc.For(context.Background(), set, "bazarr"); err != nil {
		t.Fatal(err)
	}
	if n := len(fc.credentialCalls()); n != 3 {
		t.Fatalf("core asked %d times, want the cached credential", n)
	}
	if code, _ := api.do("POST", "/settings/test/bazarr", nil); code != 200 || len(fc.credentialCalls()) != 4 {
		t.Fatalf("test: %d, core asked %d times, want a fresh ask", code, len(fc.credentialCalls()))
	}
}

// A refusal reads as not connected, with Core's reason, and nothing reaches
// the service.
func TestServicesCoreRefusal(t *testing.T) {
	rc := newRecorder(t)
	fc := newFakeCore(t, rc.srv.URL)
	fc.refuse["bazarr"] = "off_admin"
	fc.refuse["jellyfin"] = "no_account"
	_, _, api := servicesSetup(t, fc.srv.URL, nil)
	_, st := api.do("GET", "/settings", nil)
	if st["bazarr_via"] != "" || !strings.Contains(st["bazarr_detail"].(string), "switched off in Pocket Core") {
		t.Fatalf("bazarr: via=%v detail=%v", st["bazarr_via"], st["bazarr_detail"])
	}
	if st["jellyfin_via"] != "" || !strings.Contains(st["jellyfin_detail"].(string), "no Jellyfin key") {
		t.Fatalf("jellyfin: via=%v detail=%v", st["jellyfin_via"], st["jellyfin_detail"])
	}
	if code, res := api.do("POST", "/settings/test/bazarr", nil); code != http.StatusBadRequest || !strings.Contains(res["error"].(string), "switched off") {
		t.Fatalf("bazarr test: %d %v", code, res)
	}
	if code, miss := api.do("GET", "/issues/missing", nil); code != 200 || miss["configured"] != false {
		t.Fatalf("missing: %d %v", code, miss)
	}
	if len(rc.calls()) != 0 {
		t.Fatalf("a refused service was called: %v", rc.calls())
	}
}

// Direct: a saved key keeps the old behaviour by default (Core is not asked);
// Off: no calls anywhere; Disconnect clears URL and key; modes are validated.
func TestServicesDirectAndOff(t *testing.T) {
	rc := newRecorder(t)
	fc := newFakeCore(t, rc.srv.URL)
	_, _, api := servicesSetup(t, fc.srv.URL, func(c *config.Config) {
		c.BazarrURL, c.BazarrAPIKey = "", ""
		c.JellyfinURL, c.JellyfinAPIKey = "", ""
	})
	_, st := api.do("GET", "/settings", nil)
	// save a direct Bazarr (the stored mode was core by default: choose direct)
	st["bazarr_mode"], st["bazarr_url"], st["bazarr_api_key"] = "direct", rc.srv.URL, "direct-key"
	code, st := api.do("PUT", "/settings", st)
	if code != 200 || st["bazarr_mode"] != "direct" || st["bazarr_via"] != "direct" || st["bazarr_api_key_set"] != true {
		t.Fatalf("PUT direct: %d %v", code, st)
	}
	if code, res := api.do("POST", "/settings/test/bazarr", nil); code != 200 || res["via"] != "direct" {
		t.Fatalf("direct test: %d %v", code, res)
	}
	if c := rc.calls(); c[0] != "/api/system/status direct-key" {
		t.Fatalf("direct used %v", c)
	}
	if len(fc.credentialCalls()) != 0 {
		t.Fatal("direct asked Core")
	}
	// Disconnect
	delete(st, "bazarr_api_key_set")
	st["bazarr_url"], st["bazarr_api_key"] = "", ""
	code, st = api.do("PUT", "/settings", st)
	if code != 200 || st["bazarr_url"] != "" || st["bazarr_api_key_set"] != false || st["bazarr_via"] != "" || st["bazarr_mode"] != "direct" {
		t.Fatalf("disconnect: %d %v", code, st)
	}
	// Off for both: no calls to Core or the services
	st["bazarr_mode"], st["jellyfin_mode"] = "off", "off"
	if code, st = api.do("PUT", "/settings", st); st["bazarr_mode"] != "off" || st["jellyfin_mode"] != "off" {
		t.Fatalf("PUT off: %d %v", code, st)
	}
	nSvc, nCore, nStatus := len(rc.calls()), len(fc.credentialCalls()), fc.statusCalls()
	api.do("GET", "/settings", nil)
	if code, _ := api.do("POST", "/settings/test/bazarr", nil); code != http.StatusBadRequest {
		t.Fatalf("test when off: %d", code)
	}
	if code, _ := api.do("POST", "/settings/test/jellyfin", nil); code != http.StatusBadRequest {
		t.Fatalf("test when off: %d", code)
	}
	if _, miss := api.do("GET", "/issues/missing", nil); miss["configured"] != false {
		t.Fatalf("missing when off: %v", miss)
	}
	if len(rc.calls()) != nSvc || len(fc.credentialCalls()) != nCore || fc.statusCalls() != nStatus {
		t.Fatal("off made calls")
	}
	// an unknown mode keeps the stored one
	st["bazarr_mode"] = "sideways"
	if _, st = api.do("PUT", "/settings", st); st["bazarr_mode"] != "off" {
		t.Fatalf("bad mode stored: %v", st["bazarr_mode"])
	}
}

// The environment seeds the integration settings once: a saved key wins by
// default (direct), and after a Disconnect a restart does not bring it back.
func TestIntegrationsSeededOnce(t *testing.T) {
	rc := newRecorder(t)
	fc := newFakeCore(t, rc.srv.URL)
	dir := t.TempDir()
	cfg := testConfig(dir)
	cfg.CoreURL = fc.srv.URL
	cfg.BazarrURL, cfg.BazarrAPIKey = rc.srv.URL, "env-key"
	st := openTestStore(t, cfg)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	s := New(cfg, st, nil, log, nil)
	if err := s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	set, _ := st.GetSettings(ctx)
	if set.BazarrAPIKey != "env-key" || s.svc.ModeOf(set, "bazarr") != "direct" {
		t.Fatalf("seeded: key=%q mode=%s", set.BazarrAPIKey, s.svc.ModeOf(set, "bazarr"))
	}
	if s.svc.ModeOf(set, "jellyfin") != "core" {
		t.Fatalf("jellyfin without a key on a Core box: %s", s.svc.ModeOf(set, "jellyfin"))
	}
	set.BazarrURL, set.BazarrAPIKey = "", ""
	if err := st.SaveSettings(ctx, set); err != nil {
		t.Fatal(err)
	}
	s2 := New(cfg, st, nil, log, nil)
	if err := s2.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	if set, _ = st.GetSettings(ctx); set.BazarrAPIKey != "" || set.BazarrURL != "" {
		t.Fatalf("re-seeded after a Disconnect: %q %q", set.BazarrURL, set.BazarrAPIKey)
	}
}

// Core hears the modes in effect for the admin the app acts as after
// registering (defaults included), and again right after a switch or a
// Disconnect; an unrelated save reports nothing.
func TestServicesModesReported(t *testing.T) {
	rc := newRecorder(t)
	fc := newFakeCore(t, rc.srv.URL)
	_, _, api := servicesSetup(t, fc.srv.URL, nil)
	waitReported(t, fc, "bazarr", "core")
	waitReported(t, fc, "jellyfin", "core")

	_, st := api.do("GET", "/settings", nil)
	st["bazarr_mode"] = "off"
	_, st = api.do("PUT", "/settings", st)
	waitReported(t, fc, "bazarr", "off")
	st["jellyfin_mode"], st["jellyfin_url"], st["jellyfin_api_key"] = "direct", rc.srv.URL, "jf-key"
	_, st = api.do("PUT", "/settings", st)
	waitReported(t, fc, "jellyfin", "direct")
	if m, _ := fc.reported("bazarr"); m != "off" {
		t.Fatalf("bazarr reported %q", m)
	}
	// Disconnect: the mode stays direct, and Core hears it again
	_, n := fc.reported("jellyfin")
	delete(st, "jellyfin_api_key_set")
	st["jellyfin_url"], st["jellyfin_api_key"] = "", ""
	_, st = api.do("PUT", "/settings", st)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, n2 := fc.reported("jellyfin"); n2 > n {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a Disconnect was not reported")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// an unrelated save does not report
	_, n = fc.reported("jellyfin")
	st["title"] = "Subs"
	if code, _ := api.do("PUT", "/settings", st); code != 200 {
		t.Fatalf("PUT: %d", code)
	}
	time.Sleep(100 * time.Millisecond)
	if _, n2 := fc.reported("jellyfin"); n2 != n {
		t.Fatalf("an unrelated save reported (%d → %d)", n, n2)
	}
}
