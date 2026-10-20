// Package services decides how the app reaches Bazarr and Jellyfin: through
// Pocket Core's service connections, with the URL and key saved under
// Settings → Integrations, or not at all.
//
// Both services are box-wide (the app syncs subtitles for the whole library),
// so through Core the app acts as the admin who owns it: ADMIN_EMAIL when set,
// else the first admin account. Core hands that admin their own key there, or
// the box's key (Core gives the box-wide key to admins). Every use asks Core
// again, so a switch-off in Core applies at once; the key is never kept.
package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"

	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/bazarr"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/jellyfin"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/store"
)

// Connection modes, chosen per service under Settings → Integrations.
const (
	ModeCore   = "core"
	ModeDirect = "direct"
	ModeOff    = "off"
)

// Service types in Pocket Core's catalog.
const (
	Bazarr   = "bazarr"
	Jellyfin = "jellyfin"
)

// Uses are the services listed in the Core manifest.
var Uses = []core.ServiceUse{
	{Type: Bazarr, Reason: "Subtitles to sync: what is missing, blacklisting rejected downloads, season resets"},
	{Type: Jellyfin, Reason: "Find the media and refresh it after a sync"},
}

// Names are the services' display names.
var Names = map[string]string{Bazarr: "Bazarr", Jellyfin: "Jellyfin"}

// ValidMode reports whether m is a mode one can choose.
func ValidMode(m string) bool { return m == ModeCore || m == ModeDirect || m == ModeOff }

// Mode is the mode in effect: the chosen one, else direct when a key is
// saved (existing setups keep working), else Pocket Core when the box has
// one, else direct.
func Mode(stored, key string, coreOn bool) string {
	if ValidMode(stored) {
		return stored
	}
	if key == "" && coreOn {
		return ModeCore
	}
	return ModeDirect
}

// Conn is where a service is reached right now.
type Conn struct {
	URL       string
	Key       string
	PublicURL string // what a browser opens
	Via       string // core | direct
	Source    string // through Core: personal | box
}

// Resolver resolves connections; a nil Resolver knows only direct ones.
type Resolver struct {
	Core *core.Client
	// Owner returns the email of the admin the app acts as through Core.
	Owner func(ctx context.Context) string

	mu      sync.Mutex
	sources map[string]string // type → whose key Core handed out last (never the key)
}

func (r *Resolver) coreOn() bool { return r != nil && r.Core.Configured() }

func (r *Resolver) owner(ctx context.Context) string {
	if r == nil || r.Owner == nil {
		return ""
	}
	return r.Owner(ctx)
}

func saved(st store.Settings, typ string) (mode, url, key string) {
	if typ == Bazarr {
		return st.BazarrMode, st.BazarrURL, st.BazarrAPIKey
	}
	return st.JellyfinMode, st.JellyfinURL, st.JellyfinAPIKey
}

// ModeOf is the mode in effect for a service.
func (r *Resolver) ModeOf(st store.Settings, typ string) string {
	mode, _, key := saved(st, typ)
	return Mode(mode, key, r.coreOn())
}

// Reason turns Core's refusal into a sentence.
func Reason(name, reason string) string {
	switch reason {
	case "off_person":
		return "the admin switched Pocket Subtitles' use of " + name + " off in Pocket Core (Settings → Apps acting for me)"
	case "off_admin":
		return "Pocket Subtitles' use of " + name + " is switched off in Pocket Core (Links)"
	case "no_account":
		return "the admin has no " + name + " key in Pocket Core"
	case "not_in_audience":
		return "the admin does not have access to " + name + " in Pocket Core"
	case "target_missing":
		return "there is no " + name + " service in Pocket Core"
	case "not_declared":
		return "Pocket Core has not seen this Pocket Subtitles version yet; it re-registers within a minute of starting"
	case "unknown_user":
		return "the admin's email is not a person in Pocket Core"
	}
	return reason
}

func coreErr(name string, err error) error {
	var le *core.LinkError
	if errors.As(err, &le) {
		return errors.New(Reason(name, le.Reason))
	}
	return fmt.Errorf("%s through Pocket Core: %w", name, err)
}

// For returns where the service is reached now, following its mode.
func (r *Resolver) For(ctx context.Context, st store.Settings, typ string) (*Conn, error) {
	name := Names[typ]
	mode, url, key := saved(st, typ)
	switch Mode(mode, key, r.coreOn()) {
	case ModeOff:
		return nil, fmt.Errorf("%s is switched off (Settings → Integrations)", name)
	case ModeDirect:
		if url == "" || key == "" {
			return nil, fmt.Errorf("%s is not configured (Settings → Integrations)", name)
		}
		return &Conn{URL: url, Key: key, PublicURL: url, Via: ModeDirect}, nil
	}
	if !r.coreOn() {
		return nil, fmt.Errorf("%s through Pocket Core: there is no Pocket Core on this box; use a direct connection", name)
	}
	email := r.owner(ctx)
	if email == "" {
		return nil, fmt.Errorf("%s through Pocket Core: no admin account to act as", name)
	}
	cred, err := r.Core.ServiceFor(ctx, email, typ)
	if err != nil {
		return nil, coreErr(name, err)
	}
	if cred.URL == "" || cred.Key == "" {
		return nil, fmt.Errorf("the %s service in Pocket Core has no URL or key", name)
	}
	r.mu.Lock()
	if r.sources == nil {
		r.sources = map[string]string{}
	}
	r.sources[typ] = cred.Source
	r.mu.Unlock()
	base := strings.TrimRight(cred.URL, "/")
	pub := strings.TrimRight(cred.PublicURL, "/")
	if pub == "" {
		pub = base
	}
	return &Conn{URL: base, Key: cred.Key, PublicURL: pub, Via: ModeCore, Source: cred.Source}, nil
}

// Bazarr is a Bazarr client for the connection in effect (nil + why not).
func (r *Resolver) Bazarr(ctx context.Context, st store.Settings) (*bazarr.Client, *Conn, error) {
	c, err := r.For(ctx, st, Bazarr)
	if err != nil {
		return nil, nil, err
	}
	return bazarr.New(c.URL, c.Key, st.BazarrPathMap), c, nil
}

// Jellyfin is a Jellyfin client for the connection in effect (nil + why not).
func (r *Resolver) Jellyfin(ctx context.Context, st store.Settings) (*jellyfin.Client, *Conn, error) {
	c, err := r.For(ctx, st, Jellyfin)
	if err != nil {
		return nil, nil, err
	}
	return jellyfin.New(c.URL, c.Key, st.JellyfinPathMap), c, nil
}

// Status is a service's state for Settings, without fetching a key.
type Status struct {
	Mode   string `json:"mode"`   // in effect
	Via    string `json:"via"`    // core | direct | "" = not connected
	Source string `json:"source"` // through Core: personal | box (from the last use)
	Detail string `json:"detail"` // why not connected
	As     string `json:"as"`     // the admin the app acts as through Core
}

// StatusOf reports a service's state; through Core it asks the link state
// only (GET /api/v1/links/status), never the key.
func (r *Resolver) StatusOf(ctx context.Context, st store.Settings, typ string) Status {
	name := Names[typ]
	mode, url, key := saved(st, typ)
	out := Status{Mode: Mode(mode, key, r.coreOn())}
	switch out.Mode {
	case ModeOff:
		out.Detail = "switched off"
		return out
	case ModeDirect:
		if url == "" || key == "" {
			out.Detail = "add the URL and an API key"
		} else {
			out.Via = ModeDirect
		}
		return out
	}
	if !r.coreOn() {
		out.Detail = "there is no Pocket Core on this box"
		return out
	}
	out.As = r.owner(ctx)
	if out.As == "" {
		out.Detail = "no admin account to act as"
		return out
	}
	list, err := r.Core.LinkStatus(ctx, out.As, "service:"+typ)
	if err != nil {
		out.Detail = coreErr(name, err).Error()
		return out
	}
	if len(list) == 0 {
		// Core answers an empty list both for an unknown person and for an app version it has not seen yet
		out.Detail = "Pocket Core does not know " + out.As + ", or has not seen this Pocket Subtitles version yet (it re-registers within a minute of starting)"
		return out
	}
	for _, l := range list {
		if l.Scope == "account" && !l.On {
			out.Detail = Reason(name, l.State)
			return out
		}
	}
	out.Via = ModeCore
	r.mu.Lock()
	out.Source = r.sources[typ]
	r.mu.Unlock()
	return out
}
