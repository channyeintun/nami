package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/config"
	"github.com/channyeintun/nami/internal/debuglog"
)

const (
	// defaultHookTimeout bounds a single hook script. Hooks run inline — a
	// pre_tool_use hook gates the tool call and a stop hook gates the end of
	// the turn — so a script that hangs would otherwise stall the session
	// until the whole turn is cancelled.
	defaultHookTimeout = 60 * time.Second
	// hookPipeGrace bounds how long a finished or killed hook may keep its
	// output open through a background child it started.
	hookPipeGrace = time.Second
	// maxHookOutputBytes caps what a hook may print. A response is a small
	// JSON object or a short message; a runaway script must not grow the
	// engine's memory without bound.
	maxHookOutputBytes = 1 << 20
	// maxHookStderrBytes keeps enough of stderr to explain a failure.
	maxHookStderrBytes = 4 << 10
)

// Runner executes lifecycle hooks from the hooks directory.
type Runner struct {
	hooksDir string
	timeout  time.Duration
}

// NewRunner creates a hook runner scanning the given directory.
func NewRunner(hooksDir string) *Runner {
	return &Runner{hooksDir: hooksDir, timeout: defaultHookTimeout}
}

// DefaultHooksDir returns the platform-correct hooks root.
func DefaultHooksDir() string {
	return config.HooksDir()
}

// Run executes every script registered for the hook type, in name order.
func (r *Runner) Run(ctx context.Context, payload Payload) ([]Response, error) {
	scripts, err := r.scriptsFor(payload.Type)
	if err != nil {
		return nil, err
	}

	var responses []Response
	for _, script := range scripts {
		resp, err := r.runScript(ctx, script, payload)
		if err != nil {
			// Hooks are best-effort, so a failing one is skipped rather than
			// failing the operation it hooks; the debug log keeps the reason.
			debuglog.Log("hooks", "hook_failed", map[string]any{
				"hook":  string(payload.Type),
				"error": err.Error(),
			})
			continue
		}
		responses = append(responses, resp)
	}
	return responses, nil
}

// scriptsFor lists the executables registered for a hook. A script is either
// named exactly after the hook or extends it with an extension or a "-" suffix,
// so "stop.sh" and "stop-notify" run for the stop hook while "stop_failure.sh"
// stays with its own hook.
func (r *Runner) scriptsFor(hookType HookType) ([]string, error) {
	entries, err := os.ReadDir(r.hooksDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read hooks dir: %w", err)
	}

	scripts := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !matchesHookName(entry.Name(), hookType) {
			continue
		}
		scripts = append(scripts, filepath.Join(r.hooksDir, entry.Name()))
	}
	sort.Strings(scripts)
	return scripts, nil
}

func matchesHookName(name string, hookType HookType) bool {
	prefix := string(hookType)
	if name == prefix {
		return true
	}
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	switch name[len(prefix)] {
	case '.', '-':
		return true
	default:
		return false
	}
}

func (r *Runner) runScript(ctx context.Context, script string, payload Payload) (Response, error) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return Response{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	name := filepath.Base(script)
	stdout := &limitedBuffer{limit: maxHookOutputBytes}
	stderr := &limitedBuffer{limit: maxHookStderrBytes}
	cmd := exec.CommandContext(ctx, script)
	cmd.Stdin = bytes.NewReader(payloadJSON)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// A background child that inherits the output pipes would otherwise keep
	// Wait blocked long after the script itself has exited or been killed.
	cmd.WaitDelay = hookPipeGrace

	// ErrWaitDelay means the script itself exited successfully and only a
	// background child still held its output open; what it printed stands.
	if err := cmd.Run(); err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		if ctx.Err() != nil {
			return Response{}, fmt.Errorf("hook %s: %w", name, context.Cause(ctx))
		}
		if detail := strings.TrimSpace(stderr.buf.String()); detail != "" {
			return Response{}, fmt.Errorf("hook %s: %w: %s", name, err, detail)
		}
		return Response{}, fmt.Errorf("hook %s: %w", name, err)
	}
	if stdout.truncated {
		return Response{}, fmt.Errorf("hook %s: output exceeded %d bytes", name, maxHookOutputBytes)
	}

	out := stdout.buf.Bytes()
	var resp Response
	if err := json.Unmarshal(out, &resp); err != nil {
		// Plain text response
		return Response{Message: strings.TrimSpace(string(out))}, nil
	}
	return resp, nil
}

// limitedBuffer keeps the first limit bytes written to it and drops the rest.
// It never fails a write, so an overlong hook runs to completion instead of
// dying on a broken pipe; the caller checks truncated.
type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	remaining := b.limit - b.buf.Len()
	if len(p) > remaining {
		b.truncated = true
		p = p[:max(remaining, 0)]
	}
	b.buf.Write(p)
	return written, nil
}
