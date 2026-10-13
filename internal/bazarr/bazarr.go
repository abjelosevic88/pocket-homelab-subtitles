// Package bazarr is a small client for the parts of Bazarr's API the app
// uses: wanted counts, blacklisting a rejected download (which also triggers a
// replacement search), and the season tools (list, delete, search, download).
package bazarr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to one Bazarr instance.
type Client struct {
	BaseURL string
	APIKey  string
	Paths   PathMap // Bazarr's paths ↔ ours
	HTTP    *http.Client
}

// New returns a client, or nil when Bazarr is not configured.
func New(baseURL, key, pathMap string) *Client {
	if baseURL == "" || key == "" {
		return nil
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: key, Paths: ParsePathMap(pathMap), HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, form url.Values, timeout time.Duration, out any) (int, error) {
	u := c.BaseURL + "/api" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-API-KEY", c.APIKey)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	hc := *c.HTTP
	if timeout > 0 {
		hc.Timeout = timeout
	}
	res, err := hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if res.StatusCode >= 300 {
		return res.StatusCode, fmt.Errorf("bazarr %s %s: HTTP %d %s", method, path, res.StatusCode, strings.TrimSpace(string(b[:min(len(b), 200)])))
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return res.StatusCode, fmt.Errorf("bazarr %s: %w", path, err)
		}
	}
	return res.StatusCode, nil
}

// Status returns Bazarr's version (connection test).
func (c *Client) Status(ctx context.Context) (string, error) {
	var r struct {
		Data struct {
			Version string `json:"bazarr_version"`
		} `json:"data"`
	}
	if _, err := c.do(ctx, "GET", "/system/status", nil, nil, 8*time.Second, &r); err != nil {
		return "", err
	}
	return r.Data.Version, nil
}

// WantedItem is an episode or movie Bazarr is still missing subtitles for.
type WantedItem struct {
	Kind      string   `json:"kind"` // episode | movie
	SeriesID  int64    `json:"series_id,omitempty"`
	EpisodeID int64    `json:"episode_id,omitempty"`
	MovieID   int64    `json:"movie_id,omitempty"`
	Title     string   `json:"title"`
	Detail    string   `json:"detail"`
	Languages []string `json:"languages"`
	HI        []bool   `json:"-"`
}

// Wanted returns the first n wanted episodes and movies and the totals.
func (c *Client) Wanted(ctx context.Context, n int) (items []WantedItem, episodes, movies int, err error) {
	var eps struct {
		Total int `json:"total"`
		Data  []struct {
			SeriesID   int64  `json:"sonarrSeriesId"`
			EpisodeID  int64  `json:"sonarrEpisodeId"`
			Series     string `json:"seriesTitle"`
			Number     string `json:"episode_number"`
			EpTitle    string `json:"episodeTitle"`
			MissingSub []struct {
				Code2 string `json:"code2"`
				HI    bool   `json:"hi"`
			} `json:"missing_subtitles"`
		} `json:"data"`
	}
	q := url.Values{"start": {"0"}, "length": {strconv.Itoa(n)}}
	if _, err = c.do(ctx, "GET", "/episodes/wanted", q, nil, 10*time.Second, &eps); err != nil {
		return
	}
	for _, e := range eps.Data {
		it := WantedItem{Kind: "episode", SeriesID: e.SeriesID, EpisodeID: e.EpisodeID, Title: e.Series, Detail: e.Number}
		if e.EpTitle != "" {
			it.Detail += " · " + e.EpTitle
		}
		for _, m := range e.MissingSub {
			it.Languages = append(it.Languages, m.Code2)
		}
		items = append(items, it)
	}
	var mov struct {
		Total int `json:"total"`
		Data  []struct {
			RadarrID   int64  `json:"radarrId"`
			Title      string `json:"title"`
			MissingSub []struct {
				Code2 string `json:"code2"`
			} `json:"missing_subtitles"`
		} `json:"data"`
	}
	if _, err = c.do(ctx, "GET", "/movies/wanted", q, nil, 10*time.Second, &mov); err != nil {
		return
	}
	for _, m := range mov.Data {
		it := WantedItem{Kind: "movie", MovieID: m.RadarrID, Title: m.Title, Detail: "movie"}
		for _, l := range m.MissingSub {
			it.Languages = append(it.Languages, l.Code2)
		}
		items = append(items, it)
	}
	return items, eps.Total, mov.Total, nil
}

// Extras are the ids Bazarr's post-processing passes for a fresh download.
type Extras struct {
	Language   string
	Provider   string
	SubtitleID string
	SeriesID   string
	EpisodeID  string // the radarr id for movies
}

// Valid reports whether the ids are enough to blacklist the download.
func (e Extras) Valid() bool {
	return e.Provider != "" && e.SubtitleID != "" && e.EpisodeID != "" && e.Provider != "None" && e.EpisodeID != "None"
}

// IsMovie reports whether the download was for a movie.
func (e Extras) IsMovie() bool { return e.SeriesID == "" || e.SeriesID == "None" }

// Blacklist bans one downloaded subtitle. Bazarr deletes the file and its
// record and immediately searches for a replacement, whose download comes back
// through the post-processing hook. The search runs before Bazarr answers, so
// a timeout here usually means it is still working.
func (c *Client) Blacklist(ctx context.Context, e Extras, subtitlePath string) error {
	form := url.Values{"provider": {e.Provider}, "subs_id": {e.SubtitleID}, "language": {e.Language}, "subtitles_path": {c.Paths.ToBazarr(subtitlePath)}}
	var path string
	var q url.Values
	if e.IsMovie() {
		path, q = "/movies/blacklist", url.Values{"radarrid": {e.EpisodeID}}
	} else {
		path, q = "/episodes/blacklist", url.Values{"seriesid": {e.SeriesID}, "episodeid": {e.EpisodeID}}
	}
	_, err := c.do(ctx, "POST", path, q, form, 60*time.Second, nil)
	return err
}

// Series is a show known to Bazarr.
type Series struct {
	ID    int64  `json:"sonarrSeriesId"`
	Title string `json:"title"`
	Path  string `json:"path"`
}

// ListSeries returns every show.
func (c *Client) ListSeries(ctx context.Context) ([]Series, error) {
	var r struct {
		Data []Series `json:"data"`
	}
	_, err := c.do(ctx, "GET", "/series", url.Values{"length": {"-1"}}, nil, 30*time.Second, &r)
	return r.Data, err
}

// FindSeries finds a show by exact (else unique partial) title.
func (c *Client) FindSeries(ctx context.Context, title string) (*Series, error) {
	all, err := c.ListSeries(ctx)
	if err != nil {
		return nil, err
	}
	var partial []Series
	for _, s := range all {
		if strings.EqualFold(s.Title, title) {
			return &s, nil
		}
		if strings.Contains(strings.ToLower(s.Title), strings.ToLower(title)) || strings.Contains(strings.ToLower(title), strings.ToLower(s.Title)) {
			partial = append(partial, s)
		}
	}
	if len(partial) == 1 {
		return &partial[0], nil
	}
	if len(partial) == 0 {
		return nil, fmt.Errorf("Bazarr has no show called %q", title)
	}
	names := []string{}
	for _, s := range partial {
		names = append(names, s.Title)
	}
	return nil, fmt.Errorf("%q matches several shows in Bazarr: %s", title, strings.Join(names, ", "))
}

// EpisodeSub is a subtitle Bazarr knows for an episode.
type EpisodeSub struct {
	Code2  string `json:"code2"`
	Path   string `json:"path"`
	HI     bool   `json:"hi"`
	Forced bool   `json:"forced"`
}

// Episode is one episode with its subtitles.
type Episode struct {
	ID        int64        `json:"sonarrEpisodeId"`
	SeriesID  int64        `json:"sonarrSeriesId"`
	Season    int          `json:"season"`
	Episode   int          `json:"episode"`
	Title     string       `json:"title"`
	Path      string       `json:"path"`
	Subtitles []EpisodeSub `json:"subtitles"`
}

// Episodes returns all episodes of a show (paths mapped to ours).
func (c *Client) Episodes(ctx context.Context, seriesID int64) ([]Episode, error) {
	var r struct {
		Data []Episode `json:"data"`
	}
	if _, err := c.do(ctx, "GET", "/episodes", url.Values{"seriesid[]": {strconv.FormatInt(seriesID, 10)}}, nil, 30*time.Second, &r); err != nil {
		return nil, err
	}
	for i := range r.Data {
		r.Data[i].Path = c.Paths.ToOurs(r.Data[i].Path)
		for k := range r.Data[i].Subtitles {
			if r.Data[i].Subtitles[k].Path != "" {
				r.Data[i].Subtitles[k].Path = c.Paths.ToOurs(r.Data[i].Subtitles[k].Path)
			}
		}
	}
	return r.Data, nil
}

// DeleteEpisodeSubtitle deletes one subtitle file through Bazarr (keeps its DB consistent).
func (c *Client) DeleteEpisodeSubtitle(ctx context.Context, seriesID, episodeID int64, s EpisodeSub) error {
	form := url.Values{"seriesid": {strconv.FormatInt(seriesID, 10)}, "episodeid": {strconv.FormatInt(episodeID, 10)},
		"language": {s.Code2}, "forced": {strconv.FormatBool(s.Forced)}, "hi": {strconv.FormatBool(s.HI)}, "path": {c.Paths.ToBazarr(s.Path)}}
	_, err := c.do(ctx, "DELETE", "/episodes/subtitles", nil, form, 30*time.Second, nil)
	return err
}

// SearchEpisode asks Bazarr to search and download one language automatically
// (returns at once; the download happens in the background).
func (c *Client) SearchEpisode(ctx context.Context, seriesID, episodeID int64, lang string) error {
	q := url.Values{"seriesid": {strconv.FormatInt(seriesID, 10)}, "episodeid": {strconv.FormatInt(episodeID, 10)}, "language": {lang}, "forced": {"false"}, "hi": {"false"}}
	_, err := c.do(ctx, "PATCH", "/episodes/subtitles", q, url.Values{}, 60*time.Second, nil)
	return err
}

// SearchMovie asks Bazarr to search one language for a movie.
func (c *Client) SearchMovie(ctx context.Context, movieID int64, lang string) error {
	q := url.Values{"radarrid": {strconv.FormatInt(movieID, 10)}, "language": {lang}, "forced": {"false"}, "hi": {"false"}}
	_, err := c.do(ctx, "PATCH", "/movies/subtitles", q, url.Values{}, 60*time.Second, nil)
	return err
}

// Candidate is one result of a manual provider search.
type Candidate struct {
	Provider    string   `json:"provider"`
	Subtitle    string   `json:"subtitle"`
	Language    string   `json:"language"`
	HI          bool     `json:"hearing_impaired"`
	Score       float64  `json:"orig_score"`
	ReleaseInfo []string `json:"release_info"`
}

// ManualSearch lists what the providers have for one episode (slow: every provider is asked).
func (c *Client) ManualSearch(ctx context.Context, episodeID int64) ([]Candidate, error) {
	var r struct {
		Data []Candidate `json:"data"`
	}
	_, err := c.do(ctx, "GET", "/providers/episodes", url.Values{"episodeid": {strconv.FormatInt(episodeID, 10)}}, nil, 5*time.Minute, &r)
	return r.Data, err
}

// ManualDownload downloads one candidate from a manual search.
func (c *Client) ManualDownload(ctx context.Context, seriesID, episodeID int64, cand Candidate, lang string) error {
	q := url.Values{"seriesid": {strconv.FormatInt(seriesID, 10)}, "episodeid": {strconv.FormatInt(episodeID, 10)}}
	form := url.Values{"seriesid": q["seriesid"], "episodeid": q["episodeid"], "provider": {cand.Provider}, "subtitle": {cand.Subtitle},
		"language": {lang}, "hi": {strconv.FormatBool(cand.HI)}, "forced": {"False"}, "original_format": {"False"}}
	_, err := c.do(ctx, "POST", "/providers/episodes", q, form, 5*time.Minute, nil)
	return err
}

// Throttled lists providers that are not currently healthy.
func (c *Client) Throttled(ctx context.Context) []string {
	var r struct {
		Data []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if _, err := c.do(ctx, "GET", "/providers", nil, nil, 10*time.Second, &r); err != nil {
		return nil
	}
	var out []string
	for _, p := range r.Data {
		if p.Status != "" && p.Status != "Good" {
			out = append(out, p.Name)
		}
	}
	return out
}

// ---- path mapping ----

// PathMap translates between Bazarr's view of the library and ours.
type PathMap [][2]string // {bazarr prefix, our prefix}

// ParsePathMap reads "bazarr-prefix=our-prefix,…".
func ParsePathMap(s string) PathMap {
	var m PathMap
	for _, part := range strings.Split(s, ",") {
		a, b, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && a != "" && b != "" {
			m = append(m, [2]string{strings.TrimRight(a, "/"), strings.TrimRight(b, "/")})
		}
	}
	return m
}

func swap(p string, from, to string) (string, bool) {
	if p == from || strings.HasPrefix(p, from+"/") {
		return to + p[len(from):], true
	}
	return p, false
}

// ToOurs maps a Bazarr path to ours.
func (m PathMap) ToOurs(p string) string {
	for _, x := range m {
		if q, ok := swap(p, x[0], x[1]); ok {
			return q
		}
	}
	return p
}

// ToBazarr maps one of our paths to Bazarr's.
func (m PathMap) ToBazarr(p string) string {
	for _, x := range m {
		if q, ok := swap(p, x[1], x[0]); ok {
			return q
		}
	}
	return p
}

// ErrNotConfigured is returned when Bazarr is not set up.
var ErrNotConfigured = errors.New("Bazarr is not configured (Settings → Integrations)")
