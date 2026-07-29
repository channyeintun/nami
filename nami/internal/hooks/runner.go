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

	"github.com/channyeintun/nami/internal/config"
)

// Runner executes lifecycle hooks from the hooks directory.
type Runner struct {
	hooksDir string
}

// NewRunner creates a hook runner scanning the given directory.
func NewRunner(hooksDir string) *Runner {
	return &Runner{hooksDir: hooksDir}
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
			continue // hooks are best-effort
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

	cmd := exec.CommandContext(ctx, script)
	cmd.Stdin = bytes.NewReader(payloadJSON)
	out, err := cmd.Output()
	if err != nil {
		return Response{}, fmt.Errorf("hook %s: %w", filepath.Base(script), err)
	}

	var resp Response
	if err := json.Unmarshal(out, &resp); err != nil {
		// Plain text response
		return Response{Message: strings.TrimSpace(string(out))}, nil
	}
	return resp, nil
}
