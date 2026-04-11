package tools

import (
	"context"
	"fmt"
	"os"
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
	return "Perform exact string replacements in an existing text file. Use apply_patch for larger structural edits, create_file for new files, and file_write for full overwrites."
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

func (t *FileEditTool) Validate(input ToolInput) error {
	filePath, ok := stringParam(input.Params, "file_path")
	if !ok || strings.TrimSpace(filePath) == "" {
		return fmt.Errorf("file_edit requires file_path")
	}
	resolvedPath, err := resolveToolPath(filePath)
	if err != nil {
		return err
	}
	oldString, ok := stringParam(input.Params, "old_string")
	if !ok {
		return NewEditFailure(EditFailureInvalidRequest, resolvedPath, "file_edit requires old_string", "Include the exact old_string you want to replace.")
	}
	if oldString == "" {
		return NewEditFailure(EditFailureInvalidRequest, resolvedPath, "file_edit requires a non-empty old_string", "Use create_file for new files, file_write for full overwrites, or provide the exact existing text you want to replace.")
	}
	info, err := os.Stat(resolvedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return NewEditFailure(EditFailureTargetMissing, resolvedPath, fmt.Sprintf("file does not exist: %s", resolvedPath), "Use create_file to create it first, then retry file_edit with the exact existing text.")
		}
		return fmt.Errorf("stat file %q: %w", resolvedPath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%q is a directory", resolvedPath)
	}
	return nil
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
		return ToolOutput{}, NewEditFailure(EditFailureInvalidRequest, filePath, "file_edit requires new_string", "Provide the replacement text in new_string.")
	}
	if oldString == "" {
		return ToolOutput{}, NewEditFailure(EditFailureInvalidRequest, filePath, "file_edit requires a non-empty old_string", "Use create_file for new files, file_write for full overwrites, or provide the exact existing text you want to replace.")
	}
	if oldString == newString {
		return EditFailureOutput(EditFailureNoOp, filePath, "no changes to make: old_string and new_string are exactly the same", "Change new_string or skip the edit if the file is already in the desired state."), nil
	}

	replaceAll := boolParam(input.Params, "replace_all")

	contentBytes, err := os.ReadFile(filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			return ToolOutput{}, fmt.Errorf("read file %q: %w", filePath, err)
		}
		return EditFailureOutput(EditFailureTargetMissing, filePath, fmt.Sprintf("file does not exist: %s", filePath), "Use create_file to create it first, then retry file_edit with the exact existing text."), nil
	}

	trackFileBeforeWrite(filePath)

	originalContent := string(contentBytes)
	content, originalLineEnding, hadTrailingNewline := normalizeFileForLineEditing(originalContent)
	normalizedOldString := strings.ReplaceAll(oldString, "\r\n", "\n")
	normalizedNewString := strings.ReplaceAll(newString, "\r\n", "\n")

	matchCount := strings.Count(content, normalizedOldString)
	if matchCount == 0 {
		return EditFailureOutput(EditFailureNoMatch, filePath, "string to replace not found in file", "Reread the file, copy a longer exact snippet into old_string, or switch to multi_replace_file_content for line-based edits."), nil
	}
	if matchCount > 1 && !replaceAll {
		return EditFailureOutput(EditFailureMultipleMatch, filePath, fmt.Sprintf("found %d matches of old_string", matchCount), "Provide a more specific old_string with surrounding context, or set replace_all=true only if every match should change."), nil
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
