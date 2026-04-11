package tools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// FileSearchAliasTool is a compatibility alias for local models that invent
// Copilot-style file_search calls instead of using the native glob tool.
type FileSearchAliasTool struct{}

func NewFileSearchAliasTool() *FileSearchAliasTool {
	return &FileSearchAliasTool{}
}

func (t *FileSearchAliasTool) Name() string {
	return "file_search"
}

func (t *FileSearchAliasTool) Description() string {
	return "Compatibility alias for searching files by path fragment or glob pattern."
}

func (t *FileSearchAliasTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "A filename, path fragment, or glob-like pattern to search for.",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Optional directory to search in. Defaults to the current working directory.",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Optional maximum number of results to return.",
				"minimum":     1,
			},
		},
		"required": []string{"query"},
	}
}

func (t *FileSearchAliasTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *FileSearchAliasTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *FileSearchAliasTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	query, ok := firstStringParam(input.Params, "query", "pattern")
	if !ok || strings.TrimSpace(query) == "" {
		return ToolOutput{}, fmt.Errorf("file_search requires query")
	}

	searchDir, err := resolveGlobSearchDir(input.Params)
	if err != nil {
		return ToolOutput{}, err
	}

	limit := intOrDefault(input.Params, "max_results", maxGlobResults)
	if limit <= 0 || limit > maxGlobResults {
		limit = maxGlobResults
	}

	normalizedQuery := filepath.ToSlash(strings.TrimSpace(query))
	matcher := buildFileSearchMatcher(normalizedQuery)
	results, truncated, err := walkMatchingFiles(ctx, searchDir, limit, matcher)
	if err != nil {
		return ToolOutput{}, err
	}
	if len(results) == 0 {
		return ToolOutput{Output: "No files found"}, nil
	}

	output := strings.Join(results, "\n")
	if truncated {
		output += "\n(Results are truncated. Consider using a more specific query.)"
	}

	return ToolOutput{Output: output, Truncated: truncated}, nil
}

// ReadFileAliasTool is a compatibility alias for local models that emit
// read_file instead of the native file_read tool.
type ReadFileAliasTool struct{}

func NewReadFileAliasTool() *ReadFileAliasTool {
	return &ReadFileAliasTool{}
}

func (t *ReadFileAliasTool) Name() string {
	return "read_file"
}

func (t *ReadFileAliasTool) Description() string {
	return "Compatibility alias for reading a file from disk."
}

func (t *ReadFileAliasTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The absolute or relative path to the file to read.",
			},
			"filePath": map[string]any{
				"type":        "string",
				"description": "Compatibility alias for the file path.",
			},
			"file_path": map[string]any{
				"type":        "string",
				"description": "Snake_case compatibility alias for the file path.",
			},
			"startLine": map[string]any{
				"type":        "integer",
				"description": "Optional 1-based start line.",
				"minimum":     1,
			},
			"endLine": map[string]any{
				"type":        "integer",
				"description": "Optional 1-based inclusive end line.",
				"minimum":     1,
			},
		},
		"anyOf": []map[string]any{
			{"required": []string{"path"}},
			{"required": []string{"filePath"}},
			{"required": []string{"file_path"}},
		},
	}
}

func (t *ReadFileAliasTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *ReadFileAliasTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *ReadFileAliasTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	filePath, ok := firstStringParam(input.Params, "path", "filePath", "file_path")
	if !ok || strings.TrimSpace(filePath) == "" {
		return ToolOutput{}, fmt.Errorf("read_file requires path, filePath, or file_path")
	}

	params := map[string]any{
		"file_path": filePath,
	}
	if startLine, ok := firstIntParam(input.Params, "startLine", "start_line"); ok {
		params["start_line"] = startLine
	}
	if endLine, ok := firstIntParam(input.Params, "endLine", "end_line"); ok {
		params["end_line"] = endLine
	}

	delegate := NewFileReadTool()
	return delegate.Execute(ctx, ToolInput{
		Name:   delegate.Name(),
		Params: params,
		Raw:    input.Raw,
	})
}

func buildFileSearchMatcher(query string) func(string) bool {
	if strings.ContainsAny(query, "*?[") {
		return func(relPath string) bool {
			matched, err := doublestar.Match(query, filepath.ToSlash(relPath))
			return err == nil && matched
		}
	}

	needle := strings.ToLower(query)
	return func(relPath string) bool {
		relPath = filepath.ToSlash(relPath)
		return strings.Contains(strings.ToLower(relPath), needle)
	}
}

func walkMatchingFiles(
	ctx context.Context,
	searchDir string,
	limit int,
	matcher func(string) bool,
) ([]string, bool, error) {
	results := make([]string, 0, min(limit, maxGlobResults))
	truncated := false

	err := filepath.WalkDir(searchDir, func(path string, d fs.DirEntry, walkErr error) error {
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
		if !matcher(relPath) {
			return nil
		}

		absPath, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		results = append(results, absPath)
		if len(results) >= limit {
			truncated = true
			return errGlobLimitReached
		}
		return nil
	})
	if err != nil && !errors.Is(err, errGlobLimitReached) {
		if errors.Is(err, ctx.Err()) {
			return nil, false, err
		}
		return nil, false, err
	}
	if errors.Is(err, ctx.Err()) {
		return nil, false, err
	}

	sort.Strings(results)
	if truncated && len(results) > limit {
		results = results[:limit]
	}
	return results, truncated, nil
}

func firstStringParam(params map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := stringParam(params, key); ok && strings.TrimSpace(value) != "" {
			return value, true
		}
	}
	return "", false
}

func firstIntParam(params map[string]any, keys ...string) (int, bool) {
	for _, key := range keys {
		if value, ok := intParam(params, key); ok {
			return value, true
		}
	}
	return 0, false
}

func firstParam(params map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := params[key]; ok {
			return value, true
		}
	}
	return nil, false
}

func firstBoolParam(params map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := params[key]; ok {
			switch typed := value.(type) {
			case bool:
				return typed
			case string:
				return strings.EqualFold(typed, "true")
			}
		}
	}
	return false
}
