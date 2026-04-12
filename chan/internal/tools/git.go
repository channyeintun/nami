package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const defaultGitTimeout = 30 * time.Second

// GitTool exposes structured, read-only git operations without invoking a shell.
type GitTool struct{}

// NewGitTool constructs the git tool.
func NewGitTool() *GitTool {
	return &GitTool{}
}

func (t *GitTool) Name() string {
	return "git"
}

func (t *GitTool) Description() string {
	return "Run structured, read-only git operations such as status, diff, log, show, branch, and blame."
}

func (t *GitTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"operation": map[string]any{
				"type":        "string",
				"description": "Read-only git operation to run.",
				"enum":        []string{"status", "diff", "log", "show", "branch", "blame"},
			},
			"cwd": map[string]any{
				"type":        "string",
				"description": "Optional repository working directory.",
			},
			"revision": map[string]any{
				"type":        "string",
				"description": "Optional revision, revision range, or ref.",
			},
			"file_path": map[string]any{
				"type":        "string",
				"description": "Optional file path for blame or path-limited operations.",
			},
			"pathspecs": map[string]any{
				"type":        "array",
				"description": "Optional list of paths to limit the operation to.",
				"items":       map[string]any{"type": "string"},
			},
			"max_count": map[string]any{
				"type":        "integer",
				"description": "Optional max number of results for log.",
				"minimum":     1,
			},
			"line_start": map[string]any{
				"type":        "integer",
				"description": "Optional start line for blame.",
				"minimum":     1,
			},
			"line_end": map[string]any{
				"type":        "integer",
				"description": "Optional end line for blame.",
				"minimum":     1,
			},
			"cached": map[string]any{
				"type":        "boolean",
				"description": "For diff: compare staged changes.",
			},
			"name_only": map[string]any{
				"type":        "boolean",
				"description": "Use name-only output where supported.",
			},
			"stat": map[string]any{
				"type":        "boolean",
				"description": "Use stat output where supported.",
			},
			"short": map[string]any{
				"type":        "boolean",
				"description": "For status: use short output.",
			},
			"show_branch": map[string]any{
				"type":        "boolean",
				"description": "For status: include branch information.",
			},
			"oneline": map[string]any{
				"type":        "boolean",
				"description": "For log: use oneline format.",
			},
			"all": map[string]any{
				"type":        "boolean",
				"description": "For branch: include remote branches.",
			},
			"verbose": map[string]any{
				"type":        "boolean",
				"description": "For branch: show verbose output.",
			},
			"timeout_ms": map[string]any{
				"type":        "integer",
				"description": "Optional timeout in milliseconds. Defaults to 30000.",
				"minimum":     1,
			},
		},
		"required": []string{"operation"},
	}
}

func (t *GitTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *GitTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *GitTool) Validate(input ToolInput) error {
	operation, ok := stringParam(input.Params, "operation")
	if !ok || strings.TrimSpace(operation) == "" {
		return fmt.Errorf("git requires operation")
	}
	if strings.TrimSpace(operation) == "blame" {
		filePath, ok := stringParam(input.Params, "file_path")
		if !ok || strings.TrimSpace(filePath) == "" {
			return fmt.Errorf("git blame requires file_path")
		}
	}
	return nil
}

func (t *GitTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	operation, ok := stringParam(input.Params, "operation")
	if !ok || strings.TrimSpace(operation) == "" {
		return ToolOutput{}, fmt.Errorf("git requires operation")
	}

	workingDir, err := resolveWorkingDirectory(input.Params)
	if err != nil {
		return ToolOutput{}, err
	}
	repoRoot, err := gitRepoRoot(ctx, workingDir)
	if err != nil {
		return ToolOutput{}, err
	}

	commandCtx := ctx
	if timeout := timeoutFromParams(input.Params); timeout > 0 {
		var cancel context.CancelFunc
		commandCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	args, err := buildGitArgs(strings.TrimSpace(operation), input.Params, repoRoot)
	if err != nil {
		return ToolOutput{}, err
	}

	output, runErr := runGitCommand(commandCtx, repoRoot, args...)
	if runErr != nil {
		if commandCtx.Err() != nil {
			return ToolOutput{}, commandCtx.Err()
		}
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return ToolOutput{Output: output, IsError: true}, nil
		}
		return ToolOutput{}, runErr
	}
	if strings.TrimSpace(output) == "" {
		output = "No output"
	}

	// Route large diff outputs to a diff-preview artifact.
	const diffPreviewThreshold = 3000
	if strings.TrimSpace(operation) == "diff" && len(output) >= diffPreviewThreshold {
		revision, _ := stringParam(input.Params, "revision")
		description := "git diff"
		if strings.TrimSpace(revision) != "" {
			description = "git diff " + strings.TrimSpace(revision)
		} else if boolParam(input.Params, "cached") {
			description = "git diff --cached"
		}
		if mutation, ok := saveDiffPreviewArtifact(ctx, description, output); ok {
			return ToolOutput{Output: output, Artifacts: []ArtifactMutation{mutation}}, nil
		}
	}

	return ToolOutput{Output: output}, nil
}

func gitRepoRoot(ctx context.Context, workingDir string) (string, error) {
	output, err := runGitCommand(ctx, workingDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("resolve git repository root: %w", err)
	}
	return strings.TrimSpace(output), nil
}

func buildGitArgs(operation string, params map[string]any, repoRoot string) ([]string, error) {
	switch operation {
	case "status":
		args := []string{"status"}
		if boolParam(params, "short") {
			args = append(args, "--short")
		}
		if boolParam(params, "show_branch") {
			args = append(args, "--branch")
		}
		return args, nil
	case "diff":
		args := []string{"diff"}
		if boolParam(params, "cached") {
			args = append(args, "--cached")
		}
		if boolParam(params, "name_only") {
			args = append(args, "--name-only")
		}
		if boolParam(params, "stat") {
			args = append(args, "--stat")
		}
		if revision, ok := stringParam(params, "revision"); ok && strings.TrimSpace(revision) != "" {
			args = append(args, strings.TrimSpace(revision))
		}
		return appendPathspecs(args, params, repoRoot)
	case "log":
		args := []string{"log"}
		if boolParam(params, "oneline") {
			args = append(args, "--oneline")
		}
		if boolParam(params, "stat") {
			args = append(args, "--stat")
		}
		if maxCount, ok := intParam(params, "max_count"); ok {
			if maxCount < 1 {
				return nil, fmt.Errorf("max_count must be >= 1")
			}
			args = append(args, fmt.Sprintf("--max-count=%d", maxCount))
		}
		if revision, ok := stringParam(params, "revision"); ok && strings.TrimSpace(revision) != "" {
			args = append(args, strings.TrimSpace(revision))
		}
		return appendPathspecs(args, params, repoRoot)
	case "show":
		args := []string{"show"}
		if boolParam(params, "name_only") {
			args = append(args, "--name-only")
		}
		if boolParam(params, "stat") {
			args = append(args, "--stat")
		}
		revision := "HEAD"
		if value, ok := stringParam(params, "revision"); ok && strings.TrimSpace(value) != "" {
			revision = strings.TrimSpace(value)
		}
		args = append(args, revision)
		return appendPathspecs(args, params, repoRoot)
	case "branch":
		args := []string{"branch"}
		if boolParam(params, "all") {
			args = append(args, "--all")
		}
		if boolParam(params, "verbose") {
			args = append(args, "--verbose")
		}
		return args, nil
	case "blame":
		args := []string{"blame", "--"}
		fileArg, err := gitFileArg(params, repoRoot)
		if err != nil {
			return nil, err
		}
		args = args[:1]
		if lineStart, ok := intParam(params, "line_start"); ok {
			if lineStart < 1 {
				return nil, fmt.Errorf("line_start must be >= 1")
			}
			if lineEnd, ok := intParam(params, "line_end"); ok {
				if lineEnd < lineStart {
					return nil, fmt.Errorf("line_end must be >= line_start")
				}
				args = append(args, "-L", fmt.Sprintf("%d,%d", lineStart, lineEnd))
			} else {
				args = append(args, "-L", fmt.Sprintf("%d,+1", lineStart))
			}
		}
		if revision, ok := stringParam(params, "revision"); ok && strings.TrimSpace(revision) != "" {
			args = append(args, strings.TrimSpace(revision))
		}
		args = append(args, "--", fileArg)
		return args, nil
	default:
		return nil, fmt.Errorf("unsupported git operation %q", operation)
	}
}

func appendPathspecs(args []string, params map[string]any, repoRoot string) ([]string, error) {
	fileValue, hasFile := stringParam(params, "file_path")
	pathspecs := stringSliceParam(params, "pathspecs")
	if hasFile && strings.TrimSpace(fileValue) != "" {
		pathspecs = append(pathspecs, fileValue)
	}
	if len(pathspecs) == 0 {
		return args, nil
	}

	resolved := make([]string, 0, len(pathspecs))
	for _, pathspec := range pathspecs {
		resolvedPath, err := gitPathspec(pathspec, repoRoot)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, resolvedPath)
	}
	args = append(args, "--")
	args = append(args, resolved...)
	return args, nil
}

func gitFileArg(params map[string]any, repoRoot string) (string, error) {
	filePath, ok := stringParam(params, "file_path")
	if !ok || strings.TrimSpace(filePath) == "" {
		return "", fmt.Errorf("git blame requires file_path")
	}
	return gitPathspec(filePath, repoRoot)
}

func gitPathspec(pathspec string, repoRoot string) (string, error) {
	pathspec = strings.TrimSpace(pathspec)
	if pathspec == "" {
		return "", fmt.Errorf("git pathspec cannot be empty")
	}
	if filepath.IsAbs(pathspec) {
		relPath, err := filepath.Rel(repoRoot, pathspec)
		if err != nil {
			return "", fmt.Errorf("convert %q to repo-relative path: %w", pathspec, err)
		}
		if strings.HasPrefix(relPath, "..") {
			return "", fmt.Errorf("path %q is outside repository root", pathspec)
		}
		return filepath.ToSlash(relPath), nil
	}
	cleaned := filepath.Clean(pathspec)
	if cleaned == "." || strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("path %q is outside repository root", pathspec)
	}
	return filepath.ToSlash(cleaned), nil
}

func runGitCommand(ctx context.Context, workingDir string, args ...string) (string, error) {
	commandArgs := make([]string, 0, len(args)+1)
	commandArgs = append(commandArgs, "--no-pager")
	commandArgs = append(commandArgs, args...)

	cmd := exec.CommandContext(ctx, "git", commandArgs...)
	cmd.Dir = workingDir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	output := strings.TrimSpace(joinOutputs(stdout.String(), stderr.String()))
	if err != nil {
		return output, err
	}
	return output, nil
}
