package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
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
				"description": "The text to search for in files. Treated as an exact string unless isRegexp is true.",
			},
			"isRegexp": map[string]any{
				"type":        "boolean",
				"description": "Whether query should be treated as a regex. Defaults to false for query and true for pattern.",
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
				"description": "Compatibility alias for a regex pattern to search for in file contents.",
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
	maps.Copy(normalizedParams, input.Params)
	if pattern, ok := stringParam(normalizedParams, "pattern"); !ok || strings.TrimSpace(pattern) == "" {
		if query, ok := stringParam(normalizedParams, "query"); ok && strings.TrimSpace(query) != "" {
			isRegexp, hasRegexpFlag := normalizedParams["isRegexp"].(bool)
			if hasRegexpFlag && isRegexp {
				normalizedParams["pattern"] = query
			} else {
				normalizedParams["pattern"] = regexp.QuoteMeta(query)
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

	var result searchOutput
	var toolErr error
	if _, lookupErr := exec.LookPath("rg"); lookupErr == nil {
		result, toolErr = runRipgrep(ctx, searchPath, pattern, outputMode, normalizedParams)
	} else {
		result, toolErr = runGrepFallback(ctx, searchPath, pattern, outputMode, normalizedParams)
	}
	if toolErr != nil {
		return ToolOutput{}, toolErr
	}

	lines := splitOutputLines(result.lines)
	if len(lines) == 0 {
		return ToolOutput{Output: appendSkippedPathsNote("No matches found", result.skipped)}, nil
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
	if result.capped {
		truncated = true
		output += fmt.Sprintf("\n(Search output passed %d MB and was cut there. Narrow the pattern, path or glob to see the rest.)", maxSearchOutputBytes>>20)
	}

	return ToolOutput{Output: appendSkippedPathsNote(output, result.skipped), Truncated: truncated}, nil
}

// maxSearchOutputBytes bounds how much of a search backend's output is kept.
// offset and head_limit only apply afterwards, and a broad pattern over a
// large tree can print far more than grep_search could ever show.
const maxSearchOutputBytes = 8 << 20

// searchOutput is what a search backend printed.
type searchOutput struct {
	lines   string // result lines
	skipped string // errors for paths the backend could not read
	capped  bool   // lines end at maxSearchOutputBytes; the rest was dropped
}

const maxSkippedPathLines = 5

// appendSkippedPathsNote lists the errors a search backend reported for
// paths it could not read, so a partial result is not mistaken for a
// complete one.
func appendSkippedPathsNote(output, skipped string) string {
	lines := splitOutputLines(skipped)
	if len(lines) == 0 {
		return output
	}
	note := "(Some paths could not be searched:\n" + strings.Join(lines[:min(len(lines), maxSkippedPathLines)], "\n")
	if extra := len(lines) - maxSkippedPathLines; extra > 0 {
		note += fmt.Sprintf("\n... and %d more", extra)
	}
	return output + "\n" + note + ")"
}

// runSearchCommand runs rg or grep and interprets its exit status. Both exit 1
// when nothing matched, and 2 when any path failed — even after printing the
// matches from the rest of the tree. A failure that still produced output is
// therefore a partial result: the output is kept and the error text is
// returned as skipped. A failure with no output is a real error, such as an
// invalid pattern.
func runSearchCommand(ctx context.Context, name string, args []string) (searchOutput, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	stdout := &cappedBuffer{limit: maxSearchOutputBytes}
	var stderr bytes.Buffer
	cmd.Stdout = stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr == nil {
		return keptSearchLines(stdout), nil
	}
	if ctx.Err() != nil {
		return searchOutput{}, ctx.Err()
	}
	errorText := strings.TrimSpace(stderr.String())
	if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
		if exitErr.ExitCode() == 1 {
			return searchOutput{}, nil
		}
		if result := keptSearchLines(stdout); result.lines != "" {
			result.skipped = errorText
			return result, nil
		}
	}
	if errorText == "" {
		return searchOutput{}, fmt.Errorf("%s: %w", name, runErr)
	}
	return searchOutput{}, fmt.Errorf("%s: %s: %w", name, errorText, runErr)
}

// keptSearchLines returns the output a capped buffer kept. When the cap cut
// the output it cut the last line too, and part of a line is not a result.
func keptSearchLines(stdout *cappedBuffer) searchOutput {
	lines := stdout.buffer.String()
	if stdout.dropped == 0 {
		return searchOutput{lines: lines}
	}
	if complete, _, found := strings.CutLast(lines, "\n"); found {
		lines = complete
	}
	return searchOutput{lines: lines, capped: true}
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

func runRipgrep(ctx context.Context, searchPath, pattern, outputMode string, params map[string]any) (searchOutput, error) {
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

	return runSearchCommand(ctx, "rg", args)
}

func runGrepFallback(ctx context.Context, searchPath, pattern, outputMode string, params map[string]any) (searchOutput, error) {
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
	// -e keeps a pattern such as "--verbose" from being parsed as an option.
	args = append(args, "-e", pattern, "--", searchPath)

	result, err := runSearchCommand(ctx, "grep", args)
	if err != nil {
		return searchOutput{}, err
	}

	lines := splitOutputLines(result.lines)
	if glob, ok := stringParam(params, "glob"); ok && strings.TrimSpace(glob) != "" {
		patterns := splitGlobPatterns(glob)
		lines = filterGrepLinesByGlob(lines, patterns)
	}
	result.lines = strings.Join(lines, "\n")
	return result, nil
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
	for raw := range strings.FieldsSeq(glob) {
		if strings.Contains(raw, "{") && strings.Contains(raw, "}") {
			patterns = append(patterns, raw)
			continue
		}
		for part := range strings.SplitSeq(raw, ",") {
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
