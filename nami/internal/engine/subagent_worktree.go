package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/channyeintun/nami/internal/swarm"
	toolpkg "github.com/channyeintun/nami/internal/tools"
)

type delegatedWorkspace struct {
	Strategy   swarm.WorkspaceStrategy
	Path       string
	Repository string
	Branch     string
	Created    bool
}

var delegatedWorktreeMu sync.Mutex

func prepareDelegatedWorkspace(ctx context.Context, req toolpkg.AgentRunRequest, invocationID string, cwd string) (delegatedWorkspace, error) {
	strategy, err := resolveDelegatedWorkspaceStrategy(cwd, req)
	if err != nil {
		return delegatedWorkspace{}, err
	}
	workspace := delegatedWorkspace{Strategy: strategy, Path: cwd}
	if strategy != swarm.WorkspaceWorktree {
		return workspace, nil
	}
	if req.Background {
		return delegatedWorkspace{}, fmt.Errorf("worktree-backed child agents are not supported in background mode yet")
	}
	if hasRunningBackgroundAgents() {
		return delegatedWorkspace{}, fmt.Errorf("worktree-backed child agents require no active background agents because the runtime still uses a process-wide working directory")
	}
	return createDelegatedWorktree(ctx, req, invocationID, cwd)
}

func resolveDelegatedWorkspaceStrategy(cwd string, req toolpkg.AgentRunRequest) (swarm.WorkspaceStrategy, error) {
	if strategy, ok := swarm.ParseWorkspaceStrategy(req.WorkspaceStrategy); ok {
		return strategy, nil
	}
	if strings.TrimSpace(req.WorkspaceStrategy) != "" {
		return "", fmt.Errorf("unsupported workspace_strategy %q", req.WorkspaceStrategy)
	}
	if strings.TrimSpace(req.Role) == "" {
		return swarm.WorkspaceShared, nil
	}
	strategy, err := swarm.LoadRoleWorkspaceStrategy(cwd, req.Role)
	if err != nil {
		if os.IsNotExist(err) {
			return swarm.WorkspaceShared, nil
		}
		return "", err
	}
	if strategy == "" {
		return swarm.WorkspaceShared, nil
	}
	return strategy, nil
}

func createDelegatedWorktree(ctx context.Context, req toolpkg.AgentRunRequest, invocationID string, cwd string) (delegatedWorkspace, error) {
	repoRoot, err := runGitWorktreeCommand(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return delegatedWorkspace{}, fmt.Errorf("resolve repository root for child worktree: %w", err)
	}
	repoRoot = strings.TrimSpace(repoRoot)
	label := sanitizeWorktreeName(firstNonEmptyTrimmed(strings.TrimSpace(req.Role), strings.TrimSpace(req.Description), invocationID))
	shortID := sanitizeWorktreeName(invocationID)
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	branch := strings.TrimSpace(req.Role)
	if branch == "" {
		branch = label
	}
	branch = "nami/" + sanitizeWorktreeName(branch) + "-" + shortID
	path := filepath.Join(filepath.Dir(repoRoot), filepath.Base(repoRoot)+"-"+label+"-"+shortID)
	if _, err := runGitWorktreeCommand(ctx, repoRoot, "worktree", "add", "-b", branch, path, "HEAD"); err != nil {
		return delegatedWorkspace{}, err
	}
	return delegatedWorkspace{
		Strategy:   swarm.WorkspaceWorktree,
		Path:       path,
		Repository: repoRoot,
		Branch:     branch,
		Created:    true,
	}, nil
}

// cleanupDelegatedWorktree removes a worktree (and its branch) that was
// created for a child agent which never ran, so early setup failures do not
// leave orphaned checkouts behind. Only safe before the child has done any
// work: the branch still points at HEAD with no commits of its own.
func cleanupDelegatedWorktree(workspace delegatedWorkspace) {
	if !workspace.Created {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := runGitWorktreeCommand(ctx, workspace.Repository, "worktree", "remove", "--force", workspace.Path); err != nil {
		return
	}
	_, _ = runGitWorktreeCommand(ctx, workspace.Repository, "branch", "-D", workspace.Branch)
}

func withDelegatedWorkspace(workspace delegatedWorkspace, run func(string) (toolpkg.AgentRunResult, error)) (toolpkg.AgentRunResult, error) {
	if workspace.Strategy != swarm.WorkspaceWorktree {
		return run(workspace.Path)
	}
	delegatedWorktreeMu.Lock()
	defer delegatedWorktreeMu.Unlock()

	previous, err := os.Getwd()
	if err != nil {
		return toolpkg.AgentRunResult{}, fmt.Errorf("capture working directory: %w", err)
	}
	if err := os.Chdir(workspace.Path); err != nil {
		return toolpkg.AgentRunResult{}, fmt.Errorf("switch to child worktree %q: %w", workspace.Path, err)
	}
	defer func() {
		_ = os.Chdir(previous)
	}()

	return run(workspace.Path)
}

func hasRunningBackgroundAgents() bool {
	backgroundAgentsMu.RLock()
	defer backgroundAgentsMu.RUnlock()
	for _, bg := range backgroundAgents {
		bg.mu.Lock()
		running := bg.running
		bg.mu.Unlock()
		if running {
			return true
		}
	}
	return false
}

func decorateChildWorkspaceMetadata(metadata *toolpkg.ChildAgentMetadata, workspace delegatedWorkspace) *toolpkg.ChildAgentMetadata {
	if metadata == nil {
		metadata = &toolpkg.ChildAgentMetadata{}
	}
	metadata.WorkspaceStrategy = strings.TrimSpace(string(workspace.Strategy))
	metadata.WorkspacePath = strings.TrimSpace(workspace.Path)
	metadata.RepositoryRoot = strings.TrimSpace(workspace.Repository)
	metadata.WorktreeBranch = strings.TrimSpace(workspace.Branch)
	metadata.WorktreeCreated = workspace.Created
	return metadata
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
