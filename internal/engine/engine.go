// Package engine runs the Python sync engine (engine/pocket_sync.py) as a
// child process: one JSON line out, "@@stage …" progress lines on stderr.
package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Engine knows how to start the sync engine.
type Engine struct {
	Python   string
	Script   string
	CacheDir string
	Alass    string
	FFLibs   string
	Nice     int
}

// SyncOptions map one to one onto `pocket_sync.py sync` flags.
type SyncOptions struct {
	BackupPath  string
	LeadMS      int
	CentreRef   bool
	Polish      bool
	RefResidual bool
	Latinize    bool
	MTGate      bool
	ContentGate bool
	Pinned      bool
	DryRun      bool
}

// Result is the engine's JSON verdict. Raw keeps everything for the history.
type Result struct {
	Decision   string   `json:"decision"`
	Reason     string   `json:"reason"`
	Method     string   `json:"method"`
	MaxShift   *float64 `json:"max_shift"`
	ContentFit *float64 `json:"content_fit"`
	RefFit     *float64 `json:"ref_fit"`
	Changed    bool     `json:"changed"`
	Latinized  bool     `json:"latinized"`
	Raw        json.RawMessage
}

// ErrTimeout is returned when a run exceeds its deadline.
var ErrTimeout = errors.New("engine timed out")

func (e *Engine) base() []string {
	args := []string{e.Script, "--cache-dir", e.CacheDir}
	if e.Alass != "" {
		args = append(args, "--alass", e.Alass)
	}
	if e.FFLibs != "" {
		args = append(args, "--ffsubsync-libs", e.FFLibs)
	}
	if e.Nice > 0 {
		args = append(args, "--nice", strconv.Itoa(e.Nice))
	}
	return args
}

// Sync measures (and possibly rewrites) one subtitle.
func (e *Engine) Sync(ctx context.Context, video, subtitle string, o SyncOptions, onStage func(string)) (*Result, error) {
	args := append(e.base(), "sync", video, subtitle, "--lead", strconv.FormatFloat(float64(o.LeadMS)/1000, 'f', 3, 64))
	if o.BackupPath != "" {
		args = append(args, "--backup-path", o.BackupPath)
	}
	for flag, on := range map[string]bool{"--centre-ref": o.CentreRef, "--polish": o.Polish, "--ref-residual": o.RefResidual,
		"--latinize": o.Latinize, "--mt-gate": o.MTGate, "--no-content-gate": !o.ContentGate, "--pinned": o.Pinned, "--dry-run": o.DryRun} {
		if on {
			args = append(args, flag)
		}
	}
	out, err := e.run(ctx, args, onStage)
	if err != nil && out == nil {
		return nil, err
	}
	var r Result
	if jerr := json.Unmarshal(out, &r); jerr != nil {
		return nil, fmt.Errorf("engine output: %w", jerr)
	}
	r.Raw = out
	if r.Decision == "" {
		r.Decision = "error"
	}
	return &r, nil
}

// VerifyResult is an independent check of a file already in the library.
type VerifyResult struct {
	OK        bool     `json:"ok"`
	Offset    *float64 `json:"offset"`
	Target    float64  `json:"target"`
	Tolerance float64  `json:"tolerance"`
	Problems  []string `json:"problems"`
	Tracks    []struct {
		Track  int      `json:"track"`
		OK     bool     `json:"ok"`
		Offset *float64 `json:"offset"`
		Why    string   `json:"why"`
	} `json:"tracks"`
	Decision string          `json:"decision,omitempty"` // "error" when the check itself failed
	Reason   string          `json:"reason,omitempty"`
	Raw      json.RawMessage `json:"-"`
}

// Verify measures a subtitle against the audio without changing it.
func (e *Engine) Verify(ctx context.Context, video, subtitle string, leadMS, tolMS int, cyrillic, mt bool, onStage func(string)) (*VerifyResult, error) {
	args := append(e.base(), "verify", video, subtitle,
		"--lead", strconv.FormatFloat(float64(leadMS)/1000, 'f', 3, 64),
		"--tolerance", strconv.FormatFloat(float64(tolMS)/1000, 'f', 3, 64))
	if cyrillic {
		args = append(args, "--check-cyrillic")
	}
	if mt {
		args = append(args, "--check-mt")
	}
	out, err := e.run(ctx, args, onStage)
	if err != nil && out == nil {
		return nil, err
	}
	var r VerifyResult
	if jerr := json.Unmarshal(out, &r); jerr != nil {
		return nil, fmt.Errorf("engine output: %w", jerr)
	}
	r.Raw = out
	if r.Decision == "error" {
		return &r, errors.New(r.Reason)
	}
	return &r, nil
}

// Doctor reports the toolchain versions.
func (e *Engine) Doctor(ctx context.Context) (map[string]any, error) {
	out, err := e.run(ctx, append(e.base(), "doctor"), nil)
	if out == nil {
		return nil, err
	}
	var m map[string]any
	if jerr := json.Unmarshal(out, &m); jerr != nil {
		return nil, jerr
	}
	return m, nil
}

// run starts the engine in its own process group (so a cancel also stops
// the ffmpeg children) and returns its last JSON line.
func (e *Engine) run(ctx context.Context, args []string, onStage func(string)) ([]byte, error) {
	cmd := exec.Command(e.Python, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start engine (%s): %w", e.Python, err)
	}
	var tail tailBuffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "@@stage ") {
				if onStage != nil {
					onStage(strings.TrimPrefix(line, "@@stage "))
				}
				continue
			}
			tail.Write([]byte(line + "\n"))
		}
		_, _ = io.Copy(io.Discard, stderr)
	}()
	done := make(chan error, 1)
	go func() { wg.Wait(); done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case err = <-done:
		case <-time.After(10 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			err = <-done
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrTimeout
		}
		return nil, ctx.Err()
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "{") && json.Valid([]byte(l)) {
			return []byte(l), err
		}
	}
	msg := strings.TrimSpace(tail.String())
	if msg == "" {
		msg = strings.TrimSpace(stdout.String())
	}
	if err == nil {
		err = errors.New("engine printed no result")
	}
	return nil, fmt.Errorf("%w: %s", err, lastChars(msg, 600))
}

func lastChars(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// tailBuffer keeps the last 8 KiB written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 8192 {
		t.buf = t.buf[len(t.buf)-8192:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
