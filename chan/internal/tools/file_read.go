package tools

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

const fileReadMaxTokenBytes = 2 * 1024 * 1024
const fileReadBinarySampleBytes = 8192

// FileReadTool reads file contents from disk, optionally limited to a line range.
type FileReadTool struct{}

// NewFileReadTool constructs the file read tool.
func NewFileReadTool() *FileReadTool {
	return &FileReadTool{}
}

func (t *FileReadTool) Name() string {
	return "file_read"
}

func (t *FileReadTool) Description() string {
	return "Read a text file from disk, optionally limited to a line range. For large files, continue reading with start_line and end_line."
}

func (t *FileReadTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "The absolute path to the file to read.",
			},
			"start_line": map[string]any{
				"type":        "integer",
				"description": "Optional 1-based start line.",
				"minimum":     1,
			},
			"end_line": map[string]any{
				"type":        "integer",
				"description": "Optional 1-based inclusive end line.",
				"minimum":     1,
			},
		},
		"required": []string{"file_path"},
	}
}

func (t *FileReadTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *FileReadTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *FileReadTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	filePath, ok := stringParam(input.Params, "file_path")
	if !ok || strings.TrimSpace(filePath) == "" {
		return ToolOutput{}, fmt.Errorf("file_read requires file_path")
	}
	filePath, err := resolveToolPath(filePath)
	if err != nil {
		return ToolOutput{}, err
	}

	info, err := os.Stat(filePath)
	if err != nil {
		return ToolOutput{}, fmt.Errorf("stat file %q: %w", filePath, err)
	}
	if info.IsDir() {
		return ToolOutput{}, fmt.Errorf("%q is a directory", filePath)
	}

	startLine, endLine, err := fileReadRange(input.Params)
	if err != nil {
		return ToolOutput{}, err
	}

	file, err := os.Open(filePath)
	if err != nil {
		return ToolOutput{}, fmt.Errorf("open file %q: %w", filePath, err)
	}
	defer file.Close()

	sample := make([]byte, fileReadBinarySampleBytes)
	readCount, readErr := file.Read(sample)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return ToolOutput{}, fmt.Errorf("sample file %q: %w", filePath, readErr)
	}
	if _, err := file.Seek(0, 0); err != nil {
		return ToolOutput{}, fmt.Errorf("rewind file %q: %w", filePath, err)
	}
	if isLikelyBinaryFile(filePath, sample[:readCount]) {
		return ToolOutput{
			Output:  fmt.Sprintf("%s: binary or image-like file detected; file_read only supports text files and skipped this read for safety", filePath),
			IsError: true,
		}, nil
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), fileReadMaxTokenBytes)

	var builder strings.Builder
	lineNo := 0
	hasMoreLines := false
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ToolOutput{}, ctx.Err()
		default:
		}

		lineNo++
		if lineNo < startLine {
			continue
		}
		if endLine > 0 && lineNo > endLine {
			hasMoreLines = true
			break
		}
		fmt.Fprintf(&builder, "%d\t%s\n", lineNo, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			notice := fmt.Sprintf("[Output truncated: encountered a line longer than %d bytes while reading %s]", fileReadMaxTokenBytes, filePath)
			output := strings.TrimRight(builder.String(), "\n")
			if output == "" {
				return ToolOutput{Output: notice, Truncated: true}, nil
			}
			return ToolOutput{Output: output + "\n\n" + notice, Truncated: true}, nil
		}
		return ToolOutput{}, fmt.Errorf("read file %q: %w", filePath, err)
	}

	if builder.Len() == 0 {
		return ToolOutput{Output: fmt.Sprintf("%s: no content in requested range", filePath)}, nil
	}

	output := strings.TrimRight(builder.String(), "\n")
	if hasMoreLines && endLine > 0 {
		nextStart := endLine + 1
		nextEnd := endLine + max(1, endLine-startLine+1)
		output += fmt.Sprintf("\n\n[Partial read. Continue with start_line=%d and end_line=%d.]", nextStart, nextEnd)
		return ToolOutput{Output: output, Truncated: true}, nil
	}
	return ToolOutput{Output: output}, nil
}

func isLikelyBinaryFile(filePath string, sample []byte) bool {
	if len(sample) == 0 {
		return false
	}
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico", ".tif", ".tiff", ".pdf", ".zip", ".gz", ".tar", ".jar", ".exe", ".dll", ".so", ".dylib", ".woff", ".woff2", ".ttf", ".otf":
		return true
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return true
	}
	if !utf8.Valid(sample) {
		return true
	}
	controlBytes := 0
	for _, b := range sample {
		if b < 0x20 && b != '\n' && b != '\r' && b != '\t' && b != '\f' {
			controlBytes++
		}
	}
	return controlBytes > len(sample)/10
}

func fileReadRange(params map[string]any) (int, int, error) {
	startLine := 1
	if value, ok := intParam(params, "start_line"); ok {
		if value < 1 {
			return 0, 0, fmt.Errorf("start_line must be >= 1")
		}
		startLine = value
	}

	endLine := 0
	if value, ok := intParam(params, "end_line"); ok {
		if value < 1 {
			return 0, 0, fmt.Errorf("end_line must be >= 1")
		}
		endLine = value
	}
	if endLine > 0 && endLine < startLine {
		return 0, 0, fmt.Errorf("end_line must be >= start_line")
	}
	return startLine, endLine, nil
}

func intParam(params map[string]any, key string) (int, bool) {
	value, ok := params[key]
	if !ok {
		return 0, false
	}
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		parsed, err := strconv.Atoi(v)
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}
