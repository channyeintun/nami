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
	return "replace_string_in_file"
}

func (t *FileEditTool) Description() string {
	return "Make one exact in-place replacement in an existing text file. Prefer this as the first choice for small, precise edits where oldString matches exactly once. Use apply_patch only when exact replacements are awkward or the change is broader and structural."
}

func (t *FileEditTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"filePath": map[string]any{
				"type":        "string",
				"description": "The absolute path to the file to modify.",
			},
			"file_path": map[string]any{
				"type":        "string",
				"description": "Compatibility alias for the absolute path to the file to modify.",
			},
			"oldString": map[string]any{
				"type":        "string",
				"description": "The exact literal text to replace. Include enough surrounding context to uniquely identify the target occurrence.",
			},
			"old_string": map[string]any{
				"type":        "string",
				"description": "Compatibility alias for the exact text to replace.",
			},
			"newString": map[string]any{
				"type":        "string",
				"description": "The replacement text.",
			},
			"new_string": map[string]any{
				"type":        "string",
				"description": "Compatibility alias for the replacement text.",
			},
			"replaceAll": map[string]any{
				"type":        "boolean",
				"description": "Replace all occurrences of oldString instead of one.",
			},
			"replace_all": map[string]any{
				"type":        "boolean",
				"description": "Compatibility alias for replacing all occurrences.",
			},
		},
		"allOf": []map[string]any{
			{
				"anyOf": []map[string]any{
					{"required": []string{"filePath"}},
					{"required": []string{"file_path"}},
				},
			},
			{
				"anyOf": []map[string]any{
					{"required": []string{"oldString"}},
					{"required": []string{"old_string"}},
				},
			},
			{
				"anyOf": []map[string]any{
					{"required": []string{"newString"}},
					{"required": []string{"new_string"}},
				},
			},
		},
	}
}

func (t *FileEditTool) Permission() PermissionLevel {
	return PermissionWrite
}

func (t *FileEditTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *FileEditTool) Validate(input ToolInput) error {
	filePath, ok := firstStringParam(input.Params, "filePath", "file_path")
	if !ok || strings.TrimSpace(filePath) == "" {
		return fmt.Errorf("replace_string_in_file requires filePath")
	}
	resolvedPath, err := resolveToolPath(filePath)
	if err != nil {
		return err
	}
	oldString, ok := firstStringParam(input.Params, "oldString", "old_string")
	if !ok {
		return NewEditFailure(EditFailureInvalidRequest, resolvedPath, "replace_string_in_file requires oldString", "Include the exact oldString you want to replace.")
	}
	if oldString == "" {
		return NewEditFailure(EditFailureInvalidRequest, resolvedPath, "replace_string_in_file requires a non-empty oldString", "Use create_file for new files, file_write for full overwrites, or provide the exact existing text you want to replace.")
	}
	info, err := os.Stat(resolvedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return NewEditFailure(EditFailureTargetMissing, resolvedPath, fmt.Sprintf("file does not exist: %s", resolvedPath), "Use create_file to create it first, then retry replace_string_in_file with the exact existing text.")
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

	filePath, ok := firstStringParam(input.Params, "filePath", "file_path")
	if !ok || strings.TrimSpace(filePath) == "" {
		return ToolOutput{}, fmt.Errorf("replace_string_in_file requires filePath")
	}
	filePath, err := resolveToolPath(filePath)
	if err != nil {
		return ToolOutput{}, err
	}

	oldString, ok := firstStringParam(input.Params, "oldString", "old_string")
	if !ok {
		return ToolOutput{}, fmt.Errorf("replace_string_in_file requires oldString")
	}
	newString, ok := firstStringParam(input.Params, "newString", "new_string")
	if !ok {
		return ToolOutput{}, NewEditFailure(EditFailureInvalidRequest, filePath, "replace_string_in_file requires newString", "Provide the replacement text in newString.")
	}
	if oldString == "" {
		return ToolOutput{}, NewEditFailure(EditFailureInvalidRequest, filePath, "replace_string_in_file requires a non-empty oldString", "Use create_file for new files, file_write for full overwrites, or provide the exact existing text you want to replace.")
	}
	if oldString == newString {
		return EditFailureOutput(EditFailureNoOp, filePath, "no changes to make: old_string and new_string are exactly the same", "Change new_string or skip the edit if the file is already in the desired state."), nil
	}

	replaceAll := boolParam(input.Params, "replace_all") || boolParam(input.Params, "replaceAll")

	contentBytes, err := os.ReadFile(filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			return ToolOutput{}, fmt.Errorf("read file %q: %w", filePath, err)
		}
		return EditFailureOutput(EditFailureTargetMissing, filePath, fmt.Sprintf("file does not exist: %s", filePath), "Use create_file to create it first, then retry replace_string_in_file with the exact existing text."), nil
	}

	trackFileBeforeWrite(filePath)

	originalContent := string(contentBytes)
	content, originalLineEnding, hadTrailingNewline := normalizeFileForLineEditing(originalContent)
	normalizedOldString := strings.ReplaceAll(oldString, "\r\n", "\n")
	normalizedNewString := strings.ReplaceAll(newString, "\r\n", "\n")

	matchCount := strings.Count(content, normalizedOldString)
	if matchCount == 0 {
		return EditFailureOutput(EditFailureNoMatch, filePath, "string to replace not found in file", "Reread the file, copy a longer exact snippet into oldString, or use apply_patch for broader structural changes."), nil
	}
	if matchCount > 1 && !replaceAll {
		return EditFailureOutput(EditFailureMultipleMatch, filePath, fmt.Sprintf("found %d matches of oldString", matchCount), "Provide a more specific oldString with surrounding context, set replaceAll=true only if every match should change, or switch to apply_patch when the edit is structural rather than one exact replacement."), nil
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
	invalidateFileReadState(filePath)

	preview, insertions, deletions := buildFileDiffPreview(content, strings.ReplaceAll(updatedContent, "\r\n", "\n"))
	diagnostics := runPostEditDiagnostics(ctx, []string{filePath})

	return ToolOutput{
		Output:      fmt.Sprintf("Edited file successfully: %s (%d replacement%s)", filePath, replacements, pluralSuffix(replacements)),
		FilePath:    filePath,
		Preview:     preview,
		Insertions:  insertions,
		Deletions:   deletions,
		Diagnostics: diagnostics,
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
