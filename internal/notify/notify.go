// Package notify sends short messages to Apprise, ntfy or a JSON webhook.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Level is the kind of message.
type Level string

const (
	Success Level = "success"
	Warning Level = "warning"
	Failure Level = "failure"
)

var client = &http.Client{Timeout: 15 * time.Second}

// Send delivers one message. kind is apprise | ntfy | webhook.
func Send(ctx context.Context, kind, target, title, body string, level Level) error {
	if kind == "" || target == "" {
		return nil
	}
	var req *http.Request
	var err error
	switch kind {
	case "apprise":
		// Apprise API: POST /notify/<key> (form) — or a stateless /notify with urls=
		form := url.Values{"title": {title}, "body": {body}, "type": {string(level)}}
		req, err = http.NewRequestWithContext(ctx, "POST", target, strings.NewReader(form.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	case "ntfy":
		req, err = http.NewRequestWithContext(ctx, "POST", target, strings.NewReader(body))
		if err == nil {
			req.Header.Set("Title", title)
			tags := map[Level]string{Success: "white_check_mark,subtitles", Warning: "warning,subtitles", Failure: "x,subtitles"}[level]
			req.Header.Set("Tags", tags)
			if level == Failure {
				req.Header.Set("Priority", "high")
			}
		}
	case "webhook":
		b, _ := json.Marshal(map[string]string{"title": title, "body": body, "level": string(level), "app": "pocket-subtitles"})
		req, err = http.NewRequestWithContext(ctx, "POST", target, bytes.NewReader(b))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	default:
		return fmt.Errorf("unknown notification kind %q", kind)
	}
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return fmt.Errorf("HTTP %d %s", res.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}
