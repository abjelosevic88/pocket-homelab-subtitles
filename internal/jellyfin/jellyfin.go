// Package jellyfin tells Jellyfin (or Emby) that a subtitle changed. Network
// mounts deliver no inotify events, so without this a new timing stays
// invisible until the next library scan.
package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-subtitles/internal/bazarr"
)

// Client talks to one Jellyfin server.
type Client struct {
	BaseURL string
	APIKey  string
	Paths   bazarr.PathMap // {our prefix, jellyfin prefix} — note the order
	HTTP    *http.Client
}

// New returns a client, or nil when Jellyfin is not configured. pathMap is
// "our-prefix=jellyfin-prefix,…".
func New(baseURL, key, pathMap string) *Client {
	if baseURL == "" || key == "" {
		return nil
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: key, Paths: bazarr.ParsePathMap(pathMap), HTTP: &http.Client{Timeout: 10 * time.Second}}
}

func (c *Client) post(ctx context.Context, path string, body any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Emby-Token", c.APIKey)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return fmt.Errorf("jellyfin: HTTP %d %s", res.StatusCode, msg)
	}
	return nil
}

// Refresh reports the subtitle's folder as modified.
func (c *Client) Refresh(ctx context.Context, subtitlePath string) error {
	folder := filepath.Dir(c.Paths.ToOurs(subtitlePath))
	return c.post(ctx, "/Library/Media/Updated", map[string]any{"Updates": []map[string]string{{"Path": folder, "UpdateType": "Modified"}}})
}

// Info returns the server name and version (connection test).
func (c *Client) Info(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/System/Info", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Emby-Token", c.APIKey)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("jellyfin: HTTP %d", res.StatusCode)
	}
	var r struct {
		ServerName string `json:"ServerName"`
		Version    string `json:"Version"`
	}
	_ = json.NewDecoder(res.Body).Decode(&r)
	return strings.TrimSpace(r.ServerName + " " + r.Version), nil
}
