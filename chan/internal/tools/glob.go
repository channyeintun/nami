package tools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

const maxGlobResults = 100

var errGlobLimitReached = errors.New("glob result limit reached")

// GlobTool finds files matching a glob pattern.
type GlobTool struct{}

// NewGlobTool constructs the glob search tool.
func NewGlobTool() *GlobTool {
	return &GlobTool{}
}

func (t *GlobTool) Name() string {
	return "file_search"
}

func (t *GlobTool) Description() string {
	return "Search for files in the workspace by glob pattern. Use this when you know the filename or path pattern to match and only need matching file paths back."
}

func (t *GlobTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "A glob pattern to search for, or an absolute path scoped pattern such as /path/to/workspace/**/*.go.",
			},
			"pattern": map[string]any{
				"type":        "string",
				"description": "Compatibility alias for the glob pattern to match files against.",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Optional directory to search in. Defaults to the current working directory.",
			},
			"maxResults": map[string]any{
				"type":        "integer",
				"description": "Optional maximum number of results to return.",
				"minimum":     1,
			},
		},
		"anyOf": []map[string]any{
			{"required": []string{"query"}},
			{"required": []string{"pattern"}},
		},
	}
}

func (t *GlobTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *GlobTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *GlobTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	pattern, ok := firstStringParam(input.Params, "query", "pattern")
	if !ok || strings.TrimSpace(pattern) == "" {
		return ToolOutput{}, fmt.Errorf("file_search requires query")
	}

	searchDir, err := resolveGlobSearchDir(input.Params)
	if err != nil {
		return ToolOutput{}, err
	}
	limit := intOrDefault(input.Params, "maxResults", maxGlobResults)
	if limit <= 0 || limit > maxGlobResults {
		limit = maxGlobResults
	}

	searchPattern := filepath.ToSlash(pattern)
	if filepath.IsAbs(pattern) {
		searchDir, searchPattern = splitAbsoluteGlobPattern(pattern)
	}

	var matches []string
	truncated := false

	err = filepath.WalkDir(searchDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if d.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(searchDir, path)
		if err != nil {
			return err
		}
		matched, err := doublestar.Match(searchPattern, filepath.ToSlash(relPath))
		if err != nil {
			return fmt.Errorf("invalid glob pattern %q: %w", pattern, err)
		}
		if !matched {
			return nil
		}

		absPath, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		matches = append(matches, absPath)
		if len(matches) > limit {
			truncated = true
			matches = matches[:limit]
			return errGlobLimitReached
		}
		return nil
	})
	if err != nil && !errors.Is(err, errGlobLimitReached) {
		return ToolOutput{}, err
	}
	if errors.Is(err, ctx.Err()) {
		return ToolOutput{}, ctx.Err()
	}

	sort.Strings(matches)
	if len(matches) == 0 {
		return ToolOutput{Output: "No files found"}, nil
	}

	output := strings.Join(matches, "\n")
	if truncated {
		output += "\n(Results are truncated. Consider using a more specific path or pattern.)"
	}

	return ToolOutput{Output: output, Truncated: truncated}, nil
}

func resolveGlobSearchDir(params map[string]any) (string, error) {
	searchDir, ok := stringParam(params, "path")
	if !ok || strings.TrimSpace(searchDir) == "" {
		return os.Getwd()
	}
	if !filepath.IsAbs(searchDir) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
		searchDir = filepath.Join(cwd, searchDir)
	}
	info, err := os.Stat(searchDir)
	if err != nil {
		return "", fmt.Errorf("stat path %q: %w", searchDir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path %q is not a directory", searchDir)
	}
	return searchDir, nil
}

func splitAbsoluteGlobPattern(pattern string) (string, string) {
	cleaned := filepath.Clean(pattern)
	index := strings.IndexAny(filepath.ToSlash(cleaned), "*?[")
	if index == -1 {
		return filepath.Dir(cleaned), filepath.Base(cleaned)
	}

	staticPrefix := filepath.ToSlash(cleaned[:index])
	lastSlash := strings.LastIndex(staticPrefix, "/")
	if lastSlash == -1 {
		return string(filepath.Separator), filepath.ToSlash(cleaned)
	}

	baseDir := staticPrefix[:lastSlash]
	if baseDir == "" {
		baseDir = "/"
	}
	relPattern := strings.TrimPrefix(filepath.ToSlash(cleaned), strings.TrimRight(baseDir, "/")+"/")
	return filepath.FromSlash(baseDir), relPattern
}
