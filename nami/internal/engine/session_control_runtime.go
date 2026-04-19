package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/channyeintun/nami/internal/agent"
	costpkg "github.com/channyeintun/nami/internal/cost"
	"github.com/channyeintun/nami/internal/ipc"
	"github.com/channyeintun/nami/internal/session"
	toolpkg "github.com/channyeintun/nami/internal/tools"
)

type sessionControlRuntime struct {
	bridge  *ipc.Bridge
	store   *session.Store
	tracker *costpkg.Tracker
	state   *engineLoopState
}

func newSessionControlRuntime(bridge *ipc.Bridge, store *session.Store, tracker *costpkg.Tracker, state *engineLoopState) *sessionControlRuntime {
	return &sessionControlRuntime{bridge: bridge, store: store, tracker: tracker, state: state}
}

func (r *sessionControlRuntime) SwitchMode(mode string) (string, error) {
	if r == nil || r.state == nil {
		return "", fmt.Errorf("session state is unavailable")
	}
	trimmed := strings.ToLower(strings.TrimSpace(mode))
	var next agent.ExecutionMode
	switch trimmed {
	case string(agent.ModePlan):
		next = agent.ModePlan
	case string(agent.ModeFast):
		next = agent.ModeFast
	default:
		return "", fmt.Errorf("unsupported mode %q", mode)
	}
	r.state.mode = next
	if err := r.persist(); err != nil {
		return "", err
	}
	if err := r.bridge.Emit(ipc.EventModeChanged, ipc.ModeChangedPayload{Mode: string(r.state.mode)}); err != nil {
		return "", err
	}
	return string(r.state.mode), nil
}

func (r *sessionControlRuntime) EnterWorktree(ctx context.Context, req toolpkg.WorktreeControlRequest) (toolpkg.WorktreeControlResult, error) {
	repoRoot, err := r.repoRoot(ctx)
	if err != nil {
		return toolpkg.WorktreeControlResult{}, err
	}
	targetPath, created, err := r.prepareWorktree(ctx, repoRoot, req)
	if err != nil {
		return toolpkg.WorktreeControlResult{}, err
	}
	previous := r.state.cwd
	if err := os.Chdir(targetPath); err != nil {
		return toolpkg.WorktreeControlResult{}, fmt.Errorf("switch to worktree %q: %w", targetPath, err)
	}
	r.state.cwd = targetPath
	if err := r.persist(); err != nil {
		return toolpkg.WorktreeControlResult{}, err
	}
	if err := r.bridge.EmitNotice(fmt.Sprintf("Switched session cwd to %s", targetPath)); err != nil {
		return toolpkg.WorktreeControlResult{}, err
	}
	return toolpkg.WorktreeControlResult{Path: targetPath, Previous: previous, Branch: strings.TrimSpace(req.Branch), Created: created, Repository: repoRoot}, nil
}

func (r *sessionControlRuntime) ExitWorktree(ctx context.Context) (toolpkg.WorktreeControlResult, error) {
	repoRoot, err := r.repoRoot(ctx)
	if err != nil {
		return toolpkg.WorktreeControlResult{}, err
	}
	paths, err := r.worktreePaths(ctx, repoRoot)
	if err != nil {
		return toolpkg.WorktreeControlResult{}, err
	}
	if len(paths) == 0 {
		return toolpkg.WorktreeControlResult{}, fmt.Errorf("no git worktrees found")
	}
	targetPath := paths[0]
	previous := r.state.cwd
	if filepath.Clean(previous) == filepath.Clean(targetPath) {
		return toolpkg.WorktreeControlResult{Path: targetPath, Previous: previous, Repository: repoRoot}, nil
	}
	if err := os.Chdir(targetPath); err != nil {
		return toolpkg.WorktreeControlResult{}, fmt.Errorf("switch to primary worktree %q: %w", targetPath, err)
	}
	r.state.cwd = targetPath
	if err := r.persist(); err != nil {
		return toolpkg.WorktreeControlResult{}, err
	}
	if err := r.bridge.EmitNotice(fmt.Sprintf("Switched session cwd to %s", targetPath)); err != nil {
		return toolpkg.WorktreeControlResult{}, err
	}
	return toolpkg.WorktreeControlResult{Path: targetPath, Previous: previous, Repository: repoRoot}, nil
}

func (r *sessionControlRuntime) prepareWorktree(ctx context.Context, repoRoot string, req toolpkg.WorktreeControlRequest) (string, bool, error) {
	targetPath := strings.TrimSpace(req.Path)
	branch := strings.TrimSpace(req.Branch)
	if targetPath == "" {
		if branch == "" {
			return "", false, fmt.Errorf("worktree path or branch is required")
		}
		targetPath = filepath.Join(filepath.Dir(repoRoot), filepath.Base(repoRoot)+"-"+sanitizeWorktreeName(branch))
	}
	if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(repoRoot, targetPath)
	}
	targetPath = filepath.Clean(targetPath)
	if info, err := os.Stat(targetPath); err == nil && info.IsDir() {
		return targetPath, false, nil
	}
	if branch == "" {
		return "", false, fmt.Errorf("branch is required when creating a new worktree")
	}
	args := []string{"worktree", "add"}
	if req.CreateBranch {
		args = append(args, "-b", branch, targetPath)
	} else {
		args = append(args, targetPath, branch)
	}
	if _, err := runGitWorktreeCommand(ctx, repoRoot, args...); err != nil {
		return "", false, err
	}
	return targetPath, true, nil
}

func (r *sessionControlRuntime) repoRoot(ctx context.Context) (string, error) {
	output, err := runGitWorktreeCommand(ctx, r.state.cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("resolve git repository root: %w", err)
	}
	return strings.TrimSpace(output), nil
}

func (r *sessionControlRuntime) worktreePaths(ctx context.Context, repoRoot string) ([]string, error) {
	output, err := runGitWorktreeCommand(ctx, repoRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	paths := make([]string, 0, 4)
	for _, line := range lines {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		path := strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func (r *sessionControlRuntime) persist() error {
	if err := persistSessionState(r.store, sessionStateParams{
		SessionID:     r.state.sessionID,
		CreatedAt:     r.state.startedAt,
		Mode:          r.state.mode,
		Model:         r.state.activeModelID,
		SubagentModel: r.state.subagentModelID,
		CWD:           r.state.cwd,
		Branch:        agent.LoadTurnContext().GitBranch,
		Tracker:       r.tracker,
		Messages:      r.state.messages,
	}); err != nil {
		return err
	}
	return persistConversationHydratedPayload(r.store, r.state.sessionID, r.state.timeline, r.state.messages, r.state.activeModelID)
}

func runGitWorktreeCommand(ctx context.Context, workingDir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workingDir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		output := strings.TrimSpace(combineCommandOutput(stdout.String(), stderr.String()))
		if output != "" {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), output)
		}
		return "", err
	}
	return strings.TrimSpace(combineCommandOutput(stdout.String(), stderr.String())), nil
}

func combineCommandOutput(stdout, stderr string) string {
	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)
	switch {
	case stdout == "":
		return stderr
	case stderr == "":
		return stdout
	default:
		return stdout + "\n" + stderr
	}
}

func sanitizeWorktreeName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "worktree"
	}
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-' || r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteRune('-')
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return "worktree"
	}
	return result
}
