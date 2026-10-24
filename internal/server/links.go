package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/store"
)

// Pocket Core app links (docs/links.md in pocket-homelab-core), Subtitles as
// the target: another Pocket app the person allowed in Core may act here for
// them with a pcx_ token, only within these scopes. Pocket Watch uses
// subtitles:sync so the person can fix a subtitle's timing while watching.
// Every route not listed in linkRoutes stays closed to it (403).

var linkOffers = []core.Offer{
	{Scope: "subtitles:sync", Name: "Fix subtitle timing", Description: "Look up the subtitle file of what you are watching and shift it earlier or later (pinned, so the nightly sweep keeps your timing), or undo the last shift."},
}

type linkRoute struct {
	methods string
	path    *regexp.Regexp
	scope   string // "" = any linked app
}

var linkRoutes = []linkRoute{
	{"GET", regexp.MustCompile(`^/api/v1/auth/me$`), ""},
	{"GET", regexp.MustCompile(`^/api/v1/files/by-path$`), "subtitles:sync"},
	{"GET", regexp.MustCompile(`^/api/v1/files/\d+/?$`), "subtitles:sync"},
	{"POST", regexp.MustCompile(`^/api/v1/files/\d+/(nudge|undo)$`), "subtitles:sync"},
}

var errLinkScope = errors.New("outside the link's scope")

// linkAllows reports whether a link token with these scopes may make this request.
func linkAllows(scopes []string, r *http.Request) bool {
	for _, lr := range linkRoutes {
		if !strings.Contains(" "+lr.methods+" ", " "+r.Method+" ") || !lr.path.MatchString(r.URL.Path) {
			continue
		}
		if lr.scope == "" {
			return true
		}
		for _, sc := range scopes {
			if sc == lr.scope {
				return true
			}
		}
	}
	return false
}

// linkUser validates a pcx_ token, checks the route against its scopes and
// maps it to the person it acts for.
func (s *Server) linkUser(ctx context.Context, tok string, r *http.Request) (*store.User, error) {
	in, err := s.core.Introspect(ctx, tok)
	if err != nil {
		return nil, err
	}
	if !in.Active || !in.Delegated() || in.User == nil || in.User.Email == "" || in.App == nil || in.App.Slug == "" {
		return nil, http.ErrNoCookie
	}
	if !linkAllows(strings.Fields(in.Scope), r) {
		return nil, errLinkScope
	}
	return s.coreIdentityUser(ctx, in)
}
