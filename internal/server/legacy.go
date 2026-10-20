package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/store"
)

func readBody(r *http.Request) ([]byte, error) {
	return io.ReadAll(http.MaxBytesReader(nil, r.Body, 8<<20))
}

func unmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// legacyRoutes keeps the endpoints of the sync-status-server this app
// replaces, for clients that already call them (a Homepage customapi card,
// shortcuts, a Raycast extension): GET / as JSON (browsers still get the UI),
// GET /nudge/files and POST /nudge/{apply,undo,unpin}. LEGACY_API=open serves
// them without a login (the old server had none), token requires a bearer
// token, off disables them.
func (s *Server) legacyRoutes(r chi.Router) {
	if s.cfg.LegacyAPI == "off" {
		return
	}
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		if s.cfg.LegacyAPI == "open" {
			return h
		}
		return func(w http.ResponseWriter, r *http.Request) { s.requireAuth(h).ServeHTTP(w, r) }
	}
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "text/html") {
			s.handleSPA(w, r)
			return
		}
		guard(s.handleLegacyStatus)(w, r)
	})
	r.Get("/nudge/files", guard(s.handleLegacyFiles))
	r.Post("/nudge/{action}", guard(s.handleLegacyNudge))
}

func (s *Server) handleLegacyStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.widgetPayload(r.Context()))
}

type legacyFile struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Rel      string `json:"rel"`
	Lang     string `json:"lang"`
	Pinned   bool   `json:"pinned"`
	NudgedMS int    `json:"nudged_ms"`
	ID       int64  `json:"id"`
}

func (s *Server) handleLegacyFiles(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.SubtitleFilter{Q: q.Get("q")}
	if v := q.Get("pinned"); v == "1" || v == "true" {
		f.Status = "pinned"
	}
	if f.Q != "" || f.Status != "" || q.Has("limit") {
		f.Limit = 10
		if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
			f.Limit = n
		}
	}
	items, _, err := s.store.ListSubtitles(r.Context(), f)
	if err != nil {
		s.fail(w, err, "files")
		return
	}
	out := make([]legacyFile, 0, len(items))
	for _, x := range items {
		lang := x.Lang
		if x.Flags != "" {
			lang += "." + x.Flags
		}
		if lang == "" {
			lang = "?"
		}
		name := x.Path[strings.LastIndex(x.Path, "/")+1:]
		out = append(out, legacyFile{Path: x.Path, Name: name, Rel: x.Rel, Lang: lang, Pinned: x.Pinned, NudgedMS: x.NudgeMS, ID: x.ID})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleLegacyNudge(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
		MS   int    `json:"ms"`
	}
	if err := decode(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid json"})
		return
	}
	fail := func(err error) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
	}
	ctx := r.Context()
	switch chi.URLParam(r, "action") {
	case "apply":
		res, err := s.w.Nudge(ctx, in.Path, in.MS, userIDPtr(r))
		if err != nil {
			fail(err)
			return
		}
		msg := "shifted " + strconv.Itoa(res.Cues) + " cues by " + signed(res.MS) + " ms (net " + signed(res.TotalMS) + " ms), pinned"
		if !res.Jellyfin {
			msg += "; Jellyfin not refreshed"
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cues": res.Cues, "ms": res.MS, "pinned": true, "jellyfin": res.Jellyfin, "total_ms": res.TotalMS, "message": msg})
	case "undo":
		res, err := s.w.UndoNudge(ctx, in.Path, userIDPtr(r))
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restored_ms": res.RestoredMS, "jellyfin": res.Jellyfin, "total_ms": res.TotalMS,
			"message": "restored previous timing (" + signed(res.RestoredMS) + " ms), still pinned"})
	case "unpin":
		was, err := s.w.Unpin(ctx, in.Path, userIDPtr(r))
		if err != nil {
			fail(err)
			return
		}
		msg := "was not pinned"
		if was {
			msg = "unpinned - the nightly sync will manage it again"
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "was_pinned": was, "message": msg})
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "unknown route"})
	}
}

func signed(n int) string {
	if n > 0 {
		return "+" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
