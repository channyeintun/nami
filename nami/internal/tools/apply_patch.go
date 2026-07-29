package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/channyeintun/nami/internal/patch"
)

type ApplyPatchTool struct{}

type applyPatchFileChange struct {
	action     patch.Action
	path       string
	preview    string
	insertions int
	deletions  int
}

func NewApplyPatchTool() *ApplyPatchTool {
	return &ApplyPatchTool{}
}

func (t *ApplyPatchTool) Name() string {
	return "apply_patch"
}

func (t *ApplyPatchTool) Description() string {
	return "Edit text files with a structured patch. Use this for multi-line, multi-hunk, or multi-file edits, and for creating or deleting files. Patch format: *** Begin Patch, one or more *** Add File, *** Update File, or *** Delete File sections, then *** End Patch."
}

func (t *ApplyPatchTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"input": map[string]any{
				"type":        "string",
				"description": "The structured patch to apply. Must start with *** Begin Patch and end with *** End Patch.",
			},
			"patch": map[string]any{
				"type":        "string",
				"description": "Compatibility alias for the structured patch to apply.",
			},
			"explanation": map[string]any{
				"type":        "string",
				"description": "Optional short description of what the patch is intended to do.",
			},
		},
		"anyOf": []map[string]any{
			{"required": []string{"input"}},
			{"required": []string{"patch"}},
		},
	}
}

func (t *ApplyPatchTool) Permission() PermissionLevel {
	return PermissionWrite
}

func (t *ApplyPatchTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *ApplyPatchTool) Validate(input ToolInput) error {
	patchText, ok := firstStringParam(input.Params, "input", "patch")
	if !ok || strings.TrimSpace(patchText) == "" {
		return NewEditFailure(EditFailureInvalidRequest, "", "apply_patch requires input", "Provide a structured patch that starts with *** Begin Patch and ends with *** End Patch.")
	}
	document, err := patch.Parse(patchText)
	if err != nil {
		return editFailureFromPatchError(err)
	}
	if len(document.Operations) == 0 {
		return NewEditFailure(EditFailureInvalidPatchFormat, "", "apply_patch did not contain any file operations", "Add one or more *** Add File, *** Update File, or *** Delete File sections.")
	}
	for _, operation := range document.Operations {
		resolvedPath, err := resolveToolPath(operation.Path)
		if err != nil {
			return err
		}
		if err := validatePatchTarget(operation.Action, resolvedPath); err != nil {
			return err
		}
	}
	return nil
}

// validatePatchTarget checks that the on-disk state of a target matches what the
// requested action expects before any file is touched.
func validatePatchTarget(action patch.Action, resolvedPath string) error {
	info, statErr := os.Stat(resolvedPath)
	switch action {
	case patch.ActionAdd:
		if statErr == nil {
			if info.IsDir() {
				return NewEditFailure(EditFailureInvalidRequest, resolvedPath, fmt.Sprintf("cannot add file at directory path: %s", resolvedPath), "Choose a file path that does not already exist.")
			}
			return NewEditFailure(EditFailureInvalidRequest, resolvedPath, fmt.Sprintf("file already exists: %s", resolvedPath), "Use file_write to overwrite the file, file_edit for exact replacements, or switch this section to *** Update File.")
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("stat file %q: %w", resolvedPath, statErr)
		}
	case patch.ActionUpdate:
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				return NewEditFailure(EditFailureTargetMissing, resolvedPath, fmt.Sprintf("file does not exist: %s", resolvedPath), "Use create_file to create it first, or switch this section to *** Add File if you intend to create a new file.")
			}
			return fmt.Errorf("stat file %q: %w", resolvedPath, statErr)
		}
		if info.IsDir() {
			return NewEditFailure(EditFailureInvalidRequest, resolvedPath, fmt.Sprintf("cannot update directory path: %s", resolvedPath), "Target a regular text file instead of a directory.")
		}
	case patch.ActionDelete:
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				return NewEditFailure(EditFailureTargetMissing, resolvedPath, fmt.Sprintf("file does not exist: %s", resolvedPath), "Reread the workspace and remove the delete section if the file is already gone.")
			}
			return fmt.Errorf("stat file %q: %w", resolvedPath, statErr)
		}
		if info.IsDir() {
			return NewEditFailure(EditFailureUnsupportedOperation, resolvedPath, fmt.Sprintf("apply_patch does not delete directories: %s", resolvedPath), "Delete files with *** Delete File sections only; handle directories through shell commands when explicitly approved.")
		}
	}
	return nil
}

func (t *ApplyPatchTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	select {
	case <-ctx.Done():
		return ToolOutput{}, ctx.Err()
	default:
	}

	patchText, ok := firstStringParam(input.Params, "input", "patch")
	if !ok || strings.TrimSpace(patchText) == "" {
		return EditFailureOutput(EditFailureInvalidRequest, "", "apply_patch requires input", "Provide a structured patch that starts with *** Begin Patch and ends with *** End Patch."), nil
	}

	document, err := patch.Parse(patchText)
	if err != nil {
		return editFailureOutputFor(editFailureFromPatchError(err))
	}

	changes := make([]applyPatchFileChange, 0, len(document.Operations))
	totalInsertions := 0
	totalDeletions := 0

	for _, operation := range document.Operations {
		select {
		case <-ctx.Done():
			return ToolOutput{}, ctx.Err()
		default:
		}

		resolvedPath, err := resolveToolPath(operation.Path)
		if err != nil {
			return ToolOutput{}, err
		}

		change, err := applyPatchOperation(resolvedPath, operation)
		if err != nil {
			return editFailureOutputFor(err)
		}
		changes = append(changes, change)
		totalInsertions += change.insertions
		totalDeletions += change.deletions
	}

	changedPaths := make([]string, 0, len(changes))
	for _, change := range changes {
		changedPaths = append(changedPaths, change.path)
	}

	return ToolOutput{
		Output:      renderApplyPatchSummary(changes, totalInsertions, totalDeletions),
		FilePath:    applyPatchPrimaryPath(changes),
		Preview:     buildApplyPatchPreview(changes),
		Insertions:  totalInsertions,
		Deletions:   totalDeletions,
		Diagnostics: runPostEditDiagnostics(ctx, changedPaths),
	}, nil
}

// ExtractApplyPatchTargets lists the files a patch would touch, for permission
// prompts and risk assessment.
func ExtractApplyPatchTargets(patchText string) ([]string, error) {
	return patch.Targets(patchText)
}

func applyPatchOperation(resolvedPath string, operation patch.FileOperation) (applyPatchFileChange, error) {
	switch operation.Action {
	case patch.ActionAdd:
		return applyPatchAddFile(resolvedPath, operation)
	case patch.ActionDelete:
		return applyPatchDeleteFile(resolvedPath, operation)
	case patch.ActionUpdate:
		return applyPatchUpdateFile(resolvedPath, operation)
	default:
		return applyPatchFileChange{}, NewEditFailure(EditFailureUnsupportedOperation, resolvedPath, fmt.Sprintf("unsupported apply_patch action: %s", operation.Action), "Use only *** Add File, *** Update File, or *** Delete File sections.")
	}
}

func applyPatchAddFile(resolvedPath string, operation patch.FileOperation) (applyPatchFileChange, error) {
	content := strings.Join(operation.Lines, "\n")
	trackFileBeforeWrite(resolvedPath)
	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0o755); err != nil {
		return applyPatchFileChange{}, fmt.Errorf("create parent directory %q: %w", filepath.Dir(resolvedPath), err)
	}
	if err := os.WriteFile(resolvedPath, []byte(content), 0o644); err != nil {
		return applyPatchFileChange{}, fmt.Errorf("write file %q: %w", resolvedPath, err)
	}
	invalidateFileReadState(resolvedPath)
	preview, insertions, deletions := buildFileDiffPreview("", content)
	return applyPatchFileChange{action: operation.Action, path: resolvedPath, preview: preview, insertions: insertions, deletions: deletions}, nil
}

func applyPatchDeleteFile(resolvedPath string, operation patch.FileOperation) (applyPatchFileChange, error) {
	oldBytes, err := os.ReadFile(resolvedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return applyPatchFileChange{}, NewEditFailure(EditFailureTargetMissing, resolvedPath, fmt.Sprintf("file does not exist: %s", resolvedPath), "Reread the workspace and remove the delete section if the file is already gone.")
		}
		return applyPatchFileChange{}, fmt.Errorf("read file %q: %w", resolvedPath, err)
	}
	trackFileBeforeWrite(resolvedPath)
	if err := os.Remove(resolvedPath); err != nil {
		return applyPatchFileChange{}, fmt.Errorf("delete file %q: %w", resolvedPath, err)
	}
	invalidateFileReadState(resolvedPath)
	preview, insertions, deletions := buildFileDiffPreview(string(oldBytes), "")
	return applyPatchFileChange{action: operation.Action, path: resolvedPath, preview: preview, insertions: insertions, deletions: deletions}, nil
}

func applyPatchUpdateFile(resolvedPath string, operation patch.FileOperation) (applyPatchFileChange, error) {
	updatedContent, preview, insertions, deletions, err := patchUpdatedFileContent(resolvedPath, operation)
	if err != nil {
		return applyPatchFileChange{}, err
	}
	trackFileBeforeWrite(resolvedPath)
	if err := os.WriteFile(resolvedPath, []byte(updatedContent), 0o644); err != nil {
		return applyPatchFileChange{}, fmt.Errorf("write file %q: %w", resolvedPath, err)
	}
	invalidateFileReadState(resolvedPath)
	return applyPatchFileChange{action: operation.Action, path: resolvedPath, preview: preview, insertions: insertions, deletions: deletions}, nil
}

func patchUpdatedFileContent(filePath string, operation patch.FileOperation) (string, string, int, int, error) {
	originalBytes, err := os.ReadFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", 0, 0, NewEditFailure(EditFailureTargetMissing, filePath, fmt.Sprintf("file does not exist: %s", filePath), "Use create_file to create it first, or switch this section to *** Add File.")
		}
		return "", "", 0, 0, fmt.Errorf("read existing file %q: %w", filePath, err)
	}
	sample := originalBytes
	if len(sample) > fileReadBinarySampleBytes {
		sample = sample[:fileReadBinarySampleBytes]
	}
	if isLikelyBinaryFile(filePath, sample) {
		return "", "", 0, 0, NewEditFailure(EditFailureUnsupportedOperation, filePath, fmt.Sprintf("apply_patch only supports text files: %s", filePath), "Use file_write for full-text replacements or approved shell commands for non-text assets.")
	}

	normalizedOriginal, originalLineEnding, hadTrailingNewline := normalizeFileForLineEditing(string(originalBytes))
	updatedContent, err := patch.Apply(normalizedOriginal, filePath, operation.Hunks)
	if err != nil {
		return "", "", 0, 0, editFailureFromPatchError(err)
	}

	if hadTrailingNewline && !strings.HasSuffix(updatedContent, "\n") {
		updatedContent += "\n"
	}
	preview, insertions, deletions := buildFileDiffPreview(normalizedOriginal, updatedContent)
	if originalLineEnding == "\r\n" {
		updatedContent = strings.ReplaceAll(updatedContent, "\n", "\r\n")
	}
	return updatedContent, preview, insertions, deletions, nil
}

// editFailureFromPatchError translates a patch-format failure into the tool
// layer's edit-failure taxonomy, leaving unexpected errors untouched.
func editFailureFromPatchError(err error) error {
	failure, ok := errors.AsType[*patch.Failure](err)
	if !ok || failure == nil {
		return err
	}
	return NewEditFailure(EditFailureKind(failure.Kind), failure.Path, failure.Message, failure.Hint)
}

// editFailureOutputFor renders recoverable edit failures as tool output the
// model can act on, and propagates everything else as a real error.
func editFailureOutputFor(err error) (ToolOutput, error) {
	if editFailure, ok := ExtractEditFailure(err); ok {
		return EditFailureOutput(editFailure.Kind, editFailure.FilePath, editFailure.Message, editFailure.Hint), nil
	}
	return ToolOutput{}, err
}

func applyPatchPrimaryPath(changes []applyPatchFileChange) string {
	switch len(changes) {
	case 0:
		return ""
	case 1:
		return changes[0].path
	default:
		return fmt.Sprintf("%d files", len(changes))
	}
}

func buildApplyPatchPreview(changes []applyPatchFileChange) string {
	if len(changes) == 0 {
		return ""
	}
	sections := make([]string, 0, min(len(changes), 3)+1)
	for index, change := range changes {
		if index == 3 {
			sections = append(sections, fmt.Sprintf("... %d more file%s", len(changes)-index, pluralSuffix(len(changes)-index)))
			break
		}
		if strings.TrimSpace(change.preview) == "" {
			sections = append(sections, fmt.Sprintf("*** %s\n(no diff preview available)", change.path))
			continue
		}
		sections = append(sections, fmt.Sprintf("*** %s\n%s", change.path, change.preview))
	}
	return strings.Join(sections, "\n\n")
}

func renderApplyPatchSummary(changes []applyPatchFileChange, insertions, deletions int) string {
	lines := []string{fmt.Sprintf("Applied patch successfully: %d file%s changed", len(changes), pluralSuffix(len(changes)))}
	for _, change := range changes {
		verb := "updated"
		switch change.action {
		case patch.ActionAdd:
			verb = "added"
		case patch.ActionDelete:
			verb = "deleted"
		}
		lines = append(lines, fmt.Sprintf("- %s %s (+%d -%d)", verb, change.path, change.insertions, change.deletions))
	}
	lines = append(lines, fmt.Sprintf("Total: +%d -%d", insertions, deletions))
	return strings.Join(lines, "\n")
}
