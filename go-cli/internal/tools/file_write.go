package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileWriteTool creates or overwrites a file on disk.
type FileWriteTool struct{}

// NewFileWriteTool constructs the file write tool.
func NewFileWriteTool() *FileWriteTool {
	return &FileWriteTool{}
}

func (t *FileWriteTool) Name() string {
	return "file_write"
}

func (t *FileWriteTool) Description() string {
	return "Create or overwrite a text file on disk."
}

func (t *FileWriteTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "The absolute path to the file to write.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The full text content to write to the file.",
			},
		},
		"required": []string{"file_path", "content"},
	}
}

func (t *FileWriteTool) Permission() PermissionLevel {
	return PermissionWrite
}

func (t *FileWriteTool) IsConcurrencySafe(input ToolInput) bool {
	return false
}

func (t *FileWriteTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	select {
	case <-ctx.Done():
		return ToolOutput{}, ctx.Err()
	default:
	}

	filePath, ok := stringParam(input.Params, "file_path")
	if !ok || strings.TrimSpace(filePath) == "" {
		return ToolOutput{}, fmt.Errorf("file_write requires file_path")
	}
	if !filepath.IsAbs(filePath) {
		return ToolOutput{}, fmt.Errorf("file_write requires an absolute file_path")
	}

	content, ok := stringParam(input.Params, "content")
	if !ok {
		return ToolOutput{}, fmt.Errorf("file_write requires content")
	}

	writeType := "updated"
	if _, err := os.Stat(filePath); err != nil {
		if os.IsNotExist(err) {
			writeType = "created"
		} else {
			return ToolOutput{}, fmt.Errorf("stat file %q: %w", filePath, err)
		}
	}

	parentDir := filepath.Dir(filePath)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return ToolOutput{}, fmt.Errorf("create parent directory %q: %w", parentDir, err)
	}

	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return ToolOutput{}, fmt.Errorf("write file %q: %w", filePath, err)
	}

	return ToolOutput{Output: fmt.Sprintf("File %s successfully: %s", writeType, filePath)}, nil
}
