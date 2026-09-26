package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/channyeintun/nami/internal/patch"
)

type ApplyPatchTool struct{}

type applyPatchFileChange struct {
	action     patch.Action
	path       string
	content    string // the file's new content; empty for deletes
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
	// Plan the patch exactly as Execute will. That checks every file's
	// existence against the patch's own earlier sections as well as the disk,
	// and rejects a hunk that does not match before anyone is asked to approve
	// the patch.
	_, err = planPatchChanges(context.Background(), document.Operations)
	return err
}

// validatePatchTarget rejects a section aimed at a directory. Whether target
// files exist and whether hunks match is checked by planning the patch.
func validatePatchTarget(action patch.Action, resolvedPath string) error {
	info, err := os.Stat(resolvedPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat file %q: %w", resolvedPath, err)
	}
	if !info.IsDir() {
		return nil
	}
	switch action {
	case patch.ActionAdd:
		return NewEditFailure(EditFailureInvalidRequest, resolvedPath, fmt.Sprintf("cannot add file at directory path: %s", resolvedPath), "Choose a file path that does not already exist.")
	case patch.ActionUpdate:
		return NewEditFailure(EditFailureInvalidRequest, resolvedPath, fmt.Sprintf("cannot update directory path: %s", resolvedPath), "Target a regular text file instead of a directory.")
	case patch.ActionDelete:
		return NewEditFailure(EditFailureUnsupportedOperation, resolvedPath, fmt.Sprintf("apply_patch does not delete directories: %s", resolvedPath), "Delete files with *** Delete File sections only; handle directories through shell commands when explicitly approved.")
	}
	return nil
}

func patchAddTargetExists(resolvedPath string) error {
	return NewEditFailure(EditFailureInvalidRequest, resolvedPath, fmt.Sprintf("file already exists: %s", resolvedPath), "Use file_write to overwrite the file, replace_string_in_file for exact replacements, or switch this section to *** Update File.")
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

	changes, err := planPatchChanges(ctx, document.Operations)
	if err != nil {
		return editFailureOutputFor(err)
	}
	for index, change := range changes {
		if err := writePatchChange(change); err != nil {
			return patchWriteFailure(changes[:index], err)
		}
	}

	totalInsertions := 0
	totalDeletions := 0
	changedPaths := make([]string, 0, len(changes))
	for _, change := range changes {
		totalInsertions += change.insertions
		totalDeletions += change.deletions
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

// plannedFile is a file as the patch has left it so far.
type plannedFile struct {
	content string
	exists  bool
}

// planPatchChanges works out what every section of a patch does before any
// file is touched, so a section that fails to apply leaves the workspace as it
// was instead of half patched. Sections see the result of earlier sections on
// the same file.
func planPatchChanges(ctx context.Context, operations []patch.FileOperation) ([]applyPatchFileChange, error) {
	planned := make(map[string]plannedFile, len(operations))
	changes := make([]applyPatchFileChange, 0, len(operations))
	for _, operation := range operations {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		resolvedPath, err := resolveToolPath(operation.Path)
		if err != nil {
			return nil, err
		}
		current, err := plannedFileState(planned, resolvedPath)
		if err != nil {
			return nil, err
		}
		change, err := planPatchOperation(resolvedPath, operation, current)
		if err != nil {
			return nil, err
		}
		planned[resolvedPath] = plannedFile{content: change.content, exists: change.action != patch.ActionDelete}
		changes = append(changes, change)
	}
	return changes, nil
}

// plannedFileState returns a file as the patch has left it so far: the result
// of an earlier section, or else what is on disk.
func plannedFileState(planned map[string]plannedFile, path string) (plannedFile, error) {
	if file, ok := planned[path]; ok {
		return file, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return plannedFile{}, nil
	}
	if err != nil {
		return plannedFile{}, fmt.Errorf("read file %q: %w", path, err)
	}
	return plannedFile{content: string(data), exists: true}, nil
}

func planPatchOperation(resolvedPath string, operation patch.FileOperation, current plannedFile) (applyPatchFileChange, error) {
	change := applyPatchFileChange{action: operation.Action, path: resolvedPath}
	switch operation.Action {
	case patch.ActionAdd:
		if current.exists {
			return applyPatchFileChange{}, patchAddTargetExists(resolvedPath)
		}
		change.content = strings.Join(operation.Lines, "\n")
		change.preview, change.insertions, change.deletions = buildFileDiffPreview("", change.content)
	case patch.ActionDelete:
		if !current.exists {
			return applyPatchFileChange{}, NewEditFailure(EditFailureTargetMissing, resolvedPath, fmt.Sprintf("file does not exist: %s", resolvedPath), "Reread the workspace and remove the delete section if the file is already gone.")
		}
		change.preview, change.insertions, change.deletions = buildFileDiffPreview(current.content, "")
	case patch.ActionUpdate:
		if !current.exists {
			return applyPatchFileChange{}, NewEditFailure(EditFailureTargetMissing, resolvedPath, fmt.Sprintf("file does not exist: %s", resolvedPath), "Use create_file to create it first, or switch this section to *** Add File.")
		}
		updatedContent, preview, insertions, deletions, err := patchUpdatedFileContent(resolvedPath, current.content, operation)
		if err != nil {
			return applyPatchFileChange{}, err
		}
		change.content = updatedContent
		change.preview, change.insertions, change.deletions = preview, insertions, deletions
	default:
		return applyPatchFileChange{}, NewEditFailure(EditFailureUnsupportedOperation, resolvedPath, fmt.Sprintf("unsupported apply_patch action: %s", operation.Action), "Use only *** Add File, *** Update File, or *** Delete File sections.")
	}
	return change, nil
}

// writePatchChange carries out one planned section on disk.
func writePatchChange(change applyPatchFileChange) error {
	if err := trackFileBeforeWrite(change.path); err != nil {
		return err
	}
	switch change.action {
	case patch.ActionAdd:
		parentDir := filepath.Dir(change.path)
		if err := os.MkdirAll(parentDir, 0o755); err != nil {
			return fmt.Errorf("create parent directory %q: %w", parentDir, err)
		}
		if err := writeNewFile(change.path, []byte(change.content)); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return patchAddTargetExists(change.path)
			}
			return fmt.Errorf("write file %q: %w", change.path, err)
		}
	case patch.ActionDelete:
		if err := os.Remove(change.path); err != nil {
			return fmt.Errorf("delete file %q: %w", change.path, err)
		}
	case patch.ActionUpdate:
		if err := os.WriteFile(change.path, []byte(change.content), 0o644); err != nil {
			return fmt.Errorf("write file %q: %w", change.path, err)
		}
	}
	invalidateFileReadState(change.path)
	return nil
}

// patchWriteFailure reports a failure while writing a planned patch. Planning
// already checked every section, so this is an I/O error or a file that changed
// underneath the patch. The files already written are named so they are not
// patched a second time.
func patchWriteFailure(written []applyPatchFileChange, err error) (ToolOutput, error) {
	if len(written) == 0 {
		return editFailureOutputFor(err)
	}
	paths := make([]string, 0, len(written))
	for _, change := range written {
		paths = append(paths, change.path)
	}
	return ToolOutput{}, fmt.Errorf("apply_patch stopped after changing %s: %w", strings.Join(paths, ", "), err)
}

func patchUpdatedFileContent(filePath, original string, operation patch.FileOperation) (string, string, int, int, error) {
	sample := original[:min(len(original), fileReadBinarySampleBytes)]
	if isLikelyBinaryFile(filePath, []byte(sample)) {
		return "", "", 0, 0, NewEditFailure(EditFailureUnsupportedOperation, filePath, fmt.Sprintf("apply_patch only supports text files: %s", filePath), "Use file_write for full-text replacements or approved shell commands for non-text assets.")
	}

	// patch.Apply validates the hunks against the normalized text: each
	// matches exactly once, none overlap, and together they change something.
	// The same located hunks are then spliced into the original bytes, rather
	// than taking Apply's normalized result, so untouched lines keep their
	// line endings.
	text := newLineEndingText(original)
	if _, err := patch.Apply(text.normalized, filePath, operation.Hunks); err != nil {
		return "", "", 0, 0, editFailureFromPatchError(err)
	}
	edits := make([]textEdit, 0, len(operation.Hunks))
	for _, hunk := range operation.Hunks {
		replacement, err := patch.LocateHunk(text.normalized, filePath, hunk)
		if err != nil {
			return "", "", 0, 0, editFailureFromPatchError(err)
		}
		edits = append(edits, textEdit{start: replacement.Start, end: replacement.End, text: replacement.NewBlock})
	}
	slices.SortFunc(edits, func(a, b textEdit) int { return cmp.Compare(a.start, b.start) })

	updatedContent := text.apply(edits)
	preview, insertions, deletions := buildFileDiffPreview(text.normalized, strings.ReplaceAll(updatedContent, "\r\n", "\n"))
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
