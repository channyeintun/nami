package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CreateFileTool creates a new text file and fails if the target already exists.
type CreateFileTool struct{}

// NewCreateFileTool constructs the create-file tool.
func NewCreateFileTool() *CreateFileTool {
	return &CreateFileTool{}
}

func (t *CreateFileTool) Name() string {
	return "create_file"
}

func (t *CreateFileTool) Description() string {
	return "Create a new text file on disk. Fails if the target file already exists."
}

func (t *CreateFileTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "The absolute path to the file to create.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The full text content to write to the new file.",
			},
		},
		"required": []string{"file_path", "content"},
	}
}

func (t *CreateFileTool) Permission() PermissionLevel {
	return PermissionWrite
}

func (t *CreateFileTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *CreateFileTool) Validate(input ToolInput) error {
	filePath, ok := stringParam(input.Params, "file_path")
	if !ok || strings.TrimSpace(filePath) == "" {
		return fmt.Errorf("create_file requires file_path")
	}
	resolvedPath, err := resolveToolPath(filePath)
	if err != nil {
		return err
	}
	info, err := os.Stat(resolvedPath)
	if err == nil {
		if info.IsDir() {
			return fmt.Errorf("%q is a directory", resolvedPath)
		}
		return fmt.Errorf("file already exists: %s (use file_write to overwrite it, or replace_string_in_file for in-place changes)", resolvedPath)
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("stat file %q: %w", resolvedPath, err)
	}
	return nil
}

func (t *CreateFileTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	select {
	case <-ctx.Done():
		return ToolOutput{}, ctx.Err()
	default:
	}

	filePath, ok := stringParam(input.Params, "file_path")
	if !ok || strings.TrimSpace(filePath) == "" {
		return ToolOutput{}, fmt.Errorf("create_file requires file_path")
	}
	filePath, err := resolveToolPath(filePath)
	if err != nil {
		return ToolOutput{}, err
	}

	content, ok := stringParam(input.Params, "content")
	if !ok {
		return ToolOutput{}, fmt.Errorf("create_file requires content")
	}

	if info, err := os.Stat(filePath); err == nil {
		if info.IsDir() {
			return ToolOutput{}, fmt.Errorf("%q is a directory", filePath)
		}
		return ToolOutput{}, fmt.Errorf("file already exists: %s (use file_write to overwrite it, or replace_string_in_file for in-place changes)", filePath)
	} else if !os.IsNotExist(err) {
		return ToolOutput{}, fmt.Errorf("stat file %q: %w", filePath, err)
	}

	trackFileBeforeWrite(filePath)

	parentDir := filepath.Dir(filePath)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return ToolOutput{}, fmt.Errorf("create parent directory %q: %w", parentDir, err)
	}

	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return ToolOutput{}, fmt.Errorf("write file %q: %w", filePath, err)
	}
	invalidateFileReadState(filePath)

	preview, insertions, deletions := buildFileDiffPreview("", content)
	diagnostics := runPostEditDiagnostics(ctx, []string{filePath})
	return ToolOutput{
		Output:      fmt.Sprintf("File created successfully: %s", filePath),
		FilePath:    filePath,
		Preview:     preview,
		Insertions:  insertions,
		Deletions:   deletions,
		Diagnostics: diagnostics,
	}, nil
}
