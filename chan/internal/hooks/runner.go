package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Runner executes lifecycle hooks from the hooks directory.
type Runner struct {
	hooksDir string
}

// NewRunner creates a hook runner scanning the given directory.
func NewRunner(hooksDir string) *Runner {
	return &Runner{hooksDir: hooksDir}
}

// DefaultHooksDir returns ~/.config/gocode/hooks/.
func DefaultHooksDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "gocode", "hooks")
}

// Run executes all scripts matching the hook type.
func (r *Runner) Run(ctx context.Context, payload Payload) ([]Response, error) {
	pattern := filepath.Join(r.hooksDir, string(payload.Type)+"*")
	scripts, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob hooks: %w", err)
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

func (r *Runner) runScript(ctx context.Context, script string, payload Payload) (Response, error) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return Response{}, err
	}

	cmd := exec.CommandContext(ctx, script)
	cmd.Stdin = strings.NewReader(string(payloadJSON))
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
