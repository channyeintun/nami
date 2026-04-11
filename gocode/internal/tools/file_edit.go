package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileEditTool performs exact string replacement edits on text files.
type FileEditTool struct{}

// NewFileEditTool constructs the file edit tool.
func NewFileEditTool() *FileEditTool {
	return &FileEditTool{}
}

func (t *FileEditTool) Name() string {
	return "file_edit"
}

func (t *FileEditTool) Description() string {
	return "Perform exact string replacements in an existing text file, or create a new file when old_string is empty."
}

func (t *FileEditTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "The absolute path to the file to modify.",
			},
			"old_string": map[string]any{
				"type":        "string",
				"description": "The exact text to replace.",
			},
			"new_string": map[string]any{
				"type":        "string",
				"description": "The replacement text.",
			},
			"replace_all": map[string]any{
				"type":        "boolean",
				"description": "Replace all occurrences of old_string. Defaults to false.",
			},
		},
		"required": []string{"file_path", "old_string", "new_string"},
	}
}

func (t *FileEditTool) Permission() PermissionLevel {
	return PermissionWrite
}

func (t *FileEditTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *FileEditTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	select {
	case <-ctx.Done():
		return ToolOutput{}, ctx.Err()
	default:
	}

	filePath, ok := stringParam(input.Params, "file_path")
	if !ok || strings.TrimSpace(filePath) == "" {
		return ToolOutput{}, fmt.Errorf("file_edit requires file_path")
	}
	filePath, err := resolveToolPath(filePath)
	if err != nil {
		return ToolOutput{}, err
	}

	oldString, ok := stringParam(input.Params, "old_string")
	if !ok {
		return ToolOutput{}, fmt.Errorf("file_edit requires old_string")
	}
	newString, ok := stringParam(input.Params, "new_string")
	if !ok {
		return ToolOutput{}, fmt.Errorf("file_edit requires new_string")
	}
	if oldString == newString {
		return ToolOutput{}, fmt.Errorf("no changes to make: old_string and new_string are exactly the same")
	}

	replaceAll := boolParam(input.Params, "replace_all")

	trackFileBeforeWrite(filePath)

	parentDir := filepath.Dir(filePath)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return ToolOutput{}, fmt.Errorf("create parent directory %q: %w", parentDir, err)
	}

	contentBytes, err := os.ReadFile(filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			return ToolOutput{}, fmt.Errorf("read file %q: %w", filePath, err)
		}

		if oldString != "" {
			return ToolOutput{}, fmt.Errorf("file does not exist: %s", filePath)
		}

		if err := os.WriteFile(filePath, []byte(newString), 0o644); err != nil {
			return ToolOutput{}, fmt.Errorf("write file %q: %w", filePath, err)
		}
		return ToolOutput{Output: fmt.Sprintf("File created successfully: %s", filePath)}, nil
	}

	originalContent := string(contentBytes)
	content, originalLineEnding, hadTrailingNewline := normalizeFileForLineEditing(originalContent)
	normalizedOldString := strings.ReplaceAll(oldString, "\r\n", "\n")
	normalizedNewString := strings.ReplaceAll(newString, "\r\n", "\n")
	if oldString == "" {
		if strings.TrimSpace(content) != "" {
			return ToolOutput{}, fmt.Errorf("cannot create new file: file already exists and is not empty")
		}
		if err := os.WriteFile(filePath, []byte(newString), 0o644); err != nil {
			return ToolOutput{}, fmt.Errorf("write file %q: %w", filePath, err)
		}
		preview, insertions, deletions := buildFileDiffPreview(content, newString)
		return ToolOutput{
			Output:     fmt.Sprintf("File initialized successfully: %s", filePath),
			FilePath:   filePath,
			Preview:    preview,
			Insertions: insertions,
			Deletions:  deletions,
		}, nil
	}

	matchCount := strings.Count(content, normalizedOldString)
	if matchCount == 0 {
		return ToolOutput{}, fmt.Errorf("string to replace not found in file")
	}
	if matchCount > 1 && !replaceAll {
		return ToolOutput{}, fmt.Errorf("found %d matches of old_string; set replace_all=true or provide more context", matchCount)
	}

	updatedContent := strings.Replace(content, normalizedOldString, normalizedNewString, 1)
	replacements := 1
	if replaceAll {
		updatedContent = strings.ReplaceAll(content, normalizedOldString, normalizedNewString)
		replacements = matchCount
	}
	if hadTrailingNewline && !strings.HasSuffix(updatedContent, "\n") {
		updatedContent += "\n"
	}
	if originalLineEnding == "\r\n" {
		updatedContent = strings.ReplaceAll(updatedContent, "\n", "\r\n")
	}

	select {
	case <-ctx.Done():
		return ToolOutput{}, ctx.Err()
	default:
	}

	if err := os.WriteFile(filePath, []byte(updatedContent), 0o644); err != nil {
		return ToolOutput{}, fmt.Errorf("write file %q: %w", filePath, err)
	}

	preview, insertions, deletions := buildFileDiffPreview(content, strings.ReplaceAll(updatedContent, "\r\n", "\n"))

	return ToolOutput{
		Output:     fmt.Sprintf("Edited file successfully: %s (%d replacement%s)", filePath, replacements, pluralSuffix(replacements)),
		FilePath:   filePath,
		Preview:    preview,
		Insertions: insertions,
		Deletions:  deletions,
	}, nil
}

func boolParam(params map[string]any, key string) bool {
	value, ok := params[key]
	if !ok {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true")
	default:
		return false
	}
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
