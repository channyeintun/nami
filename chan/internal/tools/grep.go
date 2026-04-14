package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const defaultGrepHeadLimit = 250

// GrepTool searches file contents using ripgrep, with grep fallback.
type GrepTool struct{}

// NewGrepTool constructs the grep search tool.
func NewGrepTool() *GrepTool {
	return &GrepTool{}
}

func (t *GrepTool) Name() string {
	return "grep_search"
}

func (t *GrepTool) Description() string {
	return "Do a fast text search in the workspace. Use this when you want exact-string or regex search over file contents and need matching lines or file locations back."
}

func (t *GrepTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "The exact string or regex pattern to search for in files.",
			},
			"isRegexp": map[string]any{
				"type":        "boolean",
				"description": "Whether query should be treated as a regex. Defaults to true when pattern is provided directly.",
			},
			"includePattern": map[string]any{
				"type":        "string",
				"description": "Optional file glob or absolute path scope for the search.",
			},
			"maxResults": map[string]any{
				"type":        "integer",
				"description": "Optional maximum number of results to return.",
				"minimum":     1,
			},
			"includeIgnoredFiles": map[string]any{
				"type":        "boolean",
				"description": "Whether to include ignored files. Currently accepted for compatibility but not used by the local implementation.",
			},
			"pattern": map[string]any{
				"type":        "string",
				"description": "Compatibility alias for the regular expression pattern to search for in file contents.",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Optional file or directory to search in. Defaults to the current working directory.",
			},
			"glob": map[string]any{
				"type":        "string",
				"description": "Optional glob filter for files, for example *.go or *.{ts,tsx}.",
			},
			"output_mode": map[string]any{
				"type":        "string",
				"enum":        []string{"content", "files_with_matches", "count"},
				"description": "content shows matching lines, files_with_matches shows only file paths, count shows match counts. Defaults to files_with_matches.",
			},
			"-B":      map[string]any{"type": "integer", "minimum": 0},
			"-A":      map[string]any{"type": "integer", "minimum": 0},
			"-C":      map[string]any{"type": "integer", "minimum": 0},
			"context": map[string]any{"type": "integer", "minimum": 0},
			"-n":      map[string]any{"type": "boolean"},
			"-i":      map[string]any{"type": "boolean"},
			"type": map[string]any{
				"type":        "string",
				"description": "Optional ripgrep file type filter, for example go, js, py.",
			},
			"head_limit": map[string]any{"type": "integer", "minimum": 0},
			"offset":     map[string]any{"type": "integer", "minimum": 0},
			"multiline":  map[string]any{"type": "boolean"},
		},
		"anyOf": []map[string]any{
			{"required": []string{"query"}},
			{"required": []string{"pattern"}},
		},
	}
}

func (t *GrepTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *GrepTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *GrepTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	normalizedParams := map[string]any{}
	for key, value := range input.Params {
		normalizedParams[key] = value
	}
	if pattern, ok := stringParam(normalizedParams, "pattern"); !ok || strings.TrimSpace(pattern) == "" {
		if query, ok := stringParam(normalizedParams, "query"); ok && strings.TrimSpace(query) != "" {
			if isRegexp, ok := normalizedParams["isRegexp"].(bool); ok && !isRegexp {
				normalizedParams["pattern"] = regexp.QuoteMeta(query)
			} else {
				normalizedParams["pattern"] = query
			}
		}
	}
	if _, ok := stringParam(normalizedParams, "path"); !ok {
		if includePattern, ok := stringParam(normalizedParams, "includePattern"); ok && strings.TrimSpace(includePattern) != "" {
			if strings.ContainsAny(includePattern, "*?[") {
				normalizedParams["glob"] = includePattern
			} else {
				normalizedParams["path"] = includePattern
			}
		}
	}
	if _, ok := normalizedParams["head_limit"]; !ok {
		if maxResults, ok := intParam(normalizedParams, "maxResults"); ok && maxResults > 0 {
			normalizedParams["head_limit"] = maxResults
		}
	}
	pattern, ok := stringParam(normalizedParams, "pattern")
	if !ok || strings.TrimSpace(pattern) == "" {
		return ToolOutput{}, fmt.Errorf("grep_search requires query")
	}

	searchPath, err := resolveSearchPath(normalizedParams)
	if err != nil {
		return ToolOutput{}, err
	}

	outputMode := stringOrDefault(normalizedParams, "output_mode", "files_with_matches")
	if outputMode != "content" && outputMode != "files_with_matches" && outputMode != "count" {
		return ToolOutput{}, fmt.Errorf("invalid output_mode %q", outputMode)
	}

	headLimit := intOrDefault(normalizedParams, "head_limit", defaultGrepHeadLimit)
	offset := intOrDefault(normalizedParams, "offset", 0)
	if headLimit < 0 || offset < 0 {
		return ToolOutput{}, fmt.Errorf("head_limit and offset must be >= 0")
	}

	var rawOutput string
	var toolErr error
	if _, lookupErr := exec.LookPath("rg"); lookupErr == nil {
		rawOutput, toolErr = runRipgrep(ctx, searchPath, pattern, outputMode, normalizedParams)
	} else {
		rawOutput, toolErr = runGrepFallback(ctx, searchPath, pattern, outputMode, normalizedParams)
	}
	if toolErr != nil {
		var exitErr *exec.ExitError
		if errors.As(toolErr, &exitErr) && exitErr.ExitCode() == 1 {
			return ToolOutput{Output: "No matches found"}, nil
		}
		return ToolOutput{}, toolErr
	}

	lines := splitOutputLines(rawOutput)
	if len(lines) == 0 {
		return ToolOutput{Output: "No matches found"}, nil
	}

	lines = applyOffset(lines, offset)
	truncated := false
	if headLimit != 0 && len(lines) > headLimit {
		lines = lines[:headLimit]
		truncated = true
	}

	output := strings.Join(lines, "\n")
	if truncated {
		output += fmt.Sprintf("\n(Results are truncated. Use offset=%d to continue.)", offset+len(lines))
	}

	return ToolOutput{Output: output, Truncated: truncated}, nil
}

func resolveSearchPath(params map[string]any) (string, error) {
	searchPath, ok := stringParam(params, "path")
	if !ok || strings.TrimSpace(searchPath) == "" {
		return os.Getwd()
	}
	if !filepath.IsAbs(searchPath) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
		searchPath = filepath.Join(cwd, searchPath)
	}
	if _, err := os.Stat(searchPath); err != nil {
		return "", fmt.Errorf("stat path %q: %w", searchPath, err)
	}
	return searchPath, nil
}

func runRipgrep(ctx context.Context, searchPath, pattern, outputMode string, params map[string]any) (string, error) {
	args := []string{"--color=never"}

	switch outputMode {
	case "files_with_matches":
		args = append(args, "-l")
	case "count":
		args = append(args, "-c")
	default:
		if boolOrDefault(params, "-n", true) {
			args = append(args, "-n")
		}
	}

	if boolParam(params, "-i") {
		args = append(args, "-i")
	}
	if boolParam(params, "multiline") {
		args = append(args, "-U", "--multiline-dotall")
	}
	if typeName, ok := stringParam(params, "type"); ok && strings.TrimSpace(typeName) != "" {
		args = append(args, "--type", typeName)
	}
	appendContextArgs(&args, params, outputMode)
	appendGlobArgs(&args, params)

	if strings.HasPrefix(pattern, "-") {
		args = append(args, "-e", pattern)
	} else {
		args = append(args, pattern)
	}
	args = append(args, searchPath)

	cmd := exec.CommandContext(ctx, "rg", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if stderr.Len() > 0 {
			return "", fmt.Errorf("rg: %s", strings.TrimSpace(stderr.String()))
		}
		return "", err
	}
	return stdout.String(), nil
}

func runGrepFallback(ctx context.Context, searchPath, pattern, outputMode string, params map[string]any) (string, error) {
	args := []string{"-R", "-E"}
	if outputMode == "files_with_matches" {
		args = append(args, "-l")
	} else if outputMode == "count" {
		args = append(args, "-c")
	} else if boolOrDefault(params, "-n", true) {
		args = append(args, "-n")
	}
	if boolParam(params, "-i") {
		args = append(args, "-i")
	}
	if before, ok := intParam(params, "-B"); ok && outputMode == "content" {
		args = append(args, "-B", strconv.Itoa(before))
	}
	if after, ok := intParam(params, "-A"); ok && outputMode == "content" {
		args = append(args, "-A", strconv.Itoa(after))
	}
	if contextLines, ok := intParam(params, "context"); ok && outputMode == "content" {
		args = append(args, "-C", strconv.Itoa(contextLines))
	} else if contextLines, ok := intParam(params, "-C"); ok && outputMode == "content" {
		args = append(args, "-C", strconv.Itoa(contextLines))
	}
	args = append(args, pattern, searchPath)

	cmd := exec.CommandContext(ctx, "grep", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if stderr.Len() > 0 {
			return "", fmt.Errorf("grep: %s", strings.TrimSpace(stderr.String()))
		}
		return "", err
	}

	lines := splitOutputLines(stdout.String())
	if glob, ok := stringParam(params, "glob"); ok && strings.TrimSpace(glob) != "" {
		patterns := splitGlobPatterns(glob)
		lines = filterGrepLinesByGlob(lines, patterns)
	}
	return strings.Join(lines, "\n"), nil
}

func appendContextArgs(args *[]string, params map[string]any, outputMode string) {
	if outputMode != "content" {
		return
	}
	if contextLines, ok := intParam(params, "context"); ok {
		*args = append(*args, "-C", strconv.Itoa(contextLines))
		return
	}
	if contextLines, ok := intParam(params, "-C"); ok {
		*args = append(*args, "-C", strconv.Itoa(contextLines))
		return
	}
	if before, ok := intParam(params, "-B"); ok {
		*args = append(*args, "-B", strconv.Itoa(before))
	}
	if after, ok := intParam(params, "-A"); ok {
		*args = append(*args, "-A", strconv.Itoa(after))
	}
}

func appendGlobArgs(args *[]string, params map[string]any) {
	glob, ok := stringParam(params, "glob")
	if !ok || strings.TrimSpace(glob) == "" {
		return
	}
	for _, pattern := range splitGlobPatterns(glob) {
		*args = append(*args, "--glob", pattern)
	}
}

func splitGlobPatterns(glob string) []string {
	var patterns []string
	for _, raw := range strings.Fields(glob) {
		if strings.Contains(raw, "{") && strings.Contains(raw, "}") {
			patterns = append(patterns, raw)
			continue
		}
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				patterns = append(patterns, part)
			}
		}
	}
	return patterns
}

func filterGrepLinesByGlob(lines, patterns []string) []string {
	if len(patterns) == 0 {
		return lines
	}
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		filePath := line
		if colon := strings.Index(line, ":"); colon > 0 {
			filePath = line[:colon]
		}
		for _, pattern := range patterns {
			matched, err := filepath.Match(pattern, filepath.Base(filePath))
			if err == nil && matched {
				filtered = append(filtered, line)
				break
			}
		}
	}
	return filtered
}

func splitOutputLines(output string) []string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func applyOffset(lines []string, offset int) []string {
	if offset <= 0 {
		return lines
	}
	if offset >= len(lines) {
		return nil
	}
	return lines[offset:]
}

func intOrDefault(params map[string]any, key string, fallback int) int {
	if value, ok := intParam(params, key); ok {
		return value
	}
	return fallback
}

func stringOrDefault(params map[string]any, key, fallback string) string {
	if value, ok := stringParam(params, key); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func boolOrDefault(params map[string]any, key string, fallback bool) bool {
	value, ok := params[key]
	if !ok {
		return fallback
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		if strings.EqualFold(v, "true") {
			return true
		}
		if strings.EqualFold(v, "false") {
			return false
		}
	}
	return fallback
}
