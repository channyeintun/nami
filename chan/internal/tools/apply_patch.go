package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ApplyPatchTool struct{}

type applyPatchAction string

const (
	applyPatchActionAdd    applyPatchAction = "add"
	applyPatchActionUpdate applyPatchAction = "update"
	applyPatchActionDelete applyPatchAction = "delete"
)

type applyPatchDocument struct {
	Operations []applyPatchFileOperation
}

type applyPatchFileOperation struct {
	Action applyPatchAction
	Path   string
	Hunks  []applyPatchHunk
	Lines  []string
}

type applyPatchHunk struct {
	Lines []applyPatchLine
}

type applyPatchLine struct {
	Kind  byte
	Value string
}

type applyPatchReplacement struct {
	Start    int
	End      int
	OldBlock string
	NewBlock string
}

type applyPatchFileChange struct {
	action     applyPatchAction
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
	document, err := parseApplyPatchDocument(patchText)
	if err != nil {
		return err
	}
	if len(document.Operations) == 0 {
		return NewEditFailure(EditFailureInvalidPatchFormat, "", "apply_patch did not contain any file operations", "Add one or more *** Add File, *** Update File, or *** Delete File sections.")
	}
	for _, operation := range document.Operations {
		resolvedPath, err := resolveToolPath(operation.Path)
		if err != nil {
			return err
		}
		info, statErr := os.Stat(resolvedPath)
		switch operation.Action {
		case applyPatchActionAdd:
			if statErr == nil {
				if info.IsDir() {
					return NewEditFailure(EditFailureInvalidRequest, resolvedPath, fmt.Sprintf("cannot add file at directory path: %s", resolvedPath), "Choose a file path that does not already exist.")
				}
				return NewEditFailure(EditFailureInvalidRequest, resolvedPath, fmt.Sprintf("file already exists: %s", resolvedPath), "Use file_write to overwrite the file, file_edit for exact replacements, or switch this section to *** Update File.")
			}
			if !os.IsNotExist(statErr) {
				return fmt.Errorf("stat file %q: %w", resolvedPath, statErr)
			}
		case applyPatchActionUpdate:
			if statErr != nil {
				if os.IsNotExist(statErr) {
					return NewEditFailure(EditFailureTargetMissing, resolvedPath, fmt.Sprintf("file does not exist: %s", resolvedPath), "Use create_file to create it first, or switch this section to *** Add File if you intend to create a new file.")
				}
				return fmt.Errorf("stat file %q: %w", resolvedPath, statErr)
			}
			if info.IsDir() {
				return NewEditFailure(EditFailureInvalidRequest, resolvedPath, fmt.Sprintf("cannot update directory path: %s", resolvedPath), "Target a regular text file instead of a directory.")
			}
		case applyPatchActionDelete:
			if statErr != nil {
				if os.IsNotExist(statErr) {
					return NewEditFailure(EditFailureTargetMissing, resolvedPath, fmt.Sprintf("file does not exist: %s", resolvedPath), "Reread the workspace and remove the delete section if the file is already gone.")
				}
				return fmt.Errorf("stat file %q: %w", resolvedPath, statErr)
			}
			if info.IsDir() {
				return NewEditFailure(EditFailureUnsupportedOperation, resolvedPath, fmt.Sprintf("apply_patch does not delete directories: %s", resolvedPath), "Delete files with *** Delete File sections only; handle directories through shell commands when explicitly approved.")
			}
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

	document, err := parseApplyPatchDocument(patchText)
	if err != nil {
		if editFailure, ok := ExtractEditFailure(err); ok {
			return EditFailureOutput(editFailure.Kind, editFailure.FilePath, editFailure.Message, editFailure.Hint), nil
		}
		return ToolOutput{}, err
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

		switch operation.Action {
		case applyPatchActionAdd:
			content := strings.Join(operation.Lines, "\n")
			trackFileBeforeWrite(resolvedPath)
			if err := os.MkdirAll(filepath.Dir(resolvedPath), 0o755); err != nil {
				return ToolOutput{}, fmt.Errorf("create parent directory %q: %w", filepath.Dir(resolvedPath), err)
			}
			if err := os.WriteFile(resolvedPath, []byte(content), 0o644); err != nil {
				return ToolOutput{}, fmt.Errorf("write file %q: %w", resolvedPath, err)
			}
			invalidateFileReadState(resolvedPath)
			preview, insertions, deletions := buildFileDiffPreview("", content)
			changes = append(changes, applyPatchFileChange{action: operation.Action, path: resolvedPath, preview: preview, insertions: insertions, deletions: deletions})
			totalInsertions += insertions
			totalDeletions += deletions
		case applyPatchActionDelete:
			oldBytes, err := os.ReadFile(resolvedPath)
			if err != nil {
				if os.IsNotExist(err) {
					return EditFailureOutput(EditFailureTargetMissing, resolvedPath, fmt.Sprintf("file does not exist: %s", resolvedPath), "Reread the workspace and remove the delete section if the file is already gone."), nil
				}
				return ToolOutput{}, fmt.Errorf("read file %q: %w", resolvedPath, err)
			}
			trackFileBeforeWrite(resolvedPath)
			if err := os.Remove(resolvedPath); err != nil {
				return ToolOutput{}, fmt.Errorf("delete file %q: %w", resolvedPath, err)
			}
			invalidateFileReadState(resolvedPath)
			preview, insertions, deletions := buildFileDiffPreview(string(oldBytes), "")
			changes = append(changes, applyPatchFileChange{action: operation.Action, path: resolvedPath, preview: preview, insertions: insertions, deletions: deletions})
			totalInsertions += insertions
			totalDeletions += deletions
		case applyPatchActionUpdate:
			updatedContent, preview, insertions, deletions, err := applyPatchUpdateOperation(resolvedPath, operation)
			if err != nil {
				if editFailure, ok := ExtractEditFailure(err); ok {
					return EditFailureOutput(editFailure.Kind, editFailure.FilePath, editFailure.Message, editFailure.Hint), nil
				}
				return ToolOutput{}, err
			}
			trackFileBeforeWrite(resolvedPath)
			if err := os.WriteFile(resolvedPath, []byte(updatedContent), 0o644); err != nil {
				return ToolOutput{}, fmt.Errorf("write file %q: %w", resolvedPath, err)
			}
			invalidateFileReadState(resolvedPath)
			changes = append(changes, applyPatchFileChange{action: operation.Action, path: resolvedPath, preview: preview, insertions: insertions, deletions: deletions})
			totalInsertions += insertions
			totalDeletions += deletions
		default:
			return EditFailureOutput(EditFailureUnsupportedOperation, resolvedPath, fmt.Sprintf("unsupported apply_patch action: %s", operation.Action), "Use only *** Add File, *** Update File, or *** Delete File sections."), nil
		}
	}

	combinedPreview := buildApplyPatchPreview(changes)
	output := renderApplyPatchSummary(changes, totalInsertions, totalDeletions)
	changedPaths := make([]string, 0, len(changes))
	for _, change := range changes {
		changedPaths = append(changedPaths, change.path)
	}
	diagnostics := runPostEditDiagnostics(ctx, changedPaths)
	primaryPath := ""
	if len(changes) == 1 {
		primaryPath = changes[0].path
	} else if len(changes) > 1 {
		primaryPath = fmt.Sprintf("%d files", len(changes))
	}

	return ToolOutput{
		Output:      output,
		FilePath:    primaryPath,
		Preview:     combinedPreview,
		Insertions:  totalInsertions,
		Deletions:   totalDeletions,
		Diagnostics: diagnostics,
	}, nil
}

func ExtractApplyPatchTargets(patchText string) ([]string, error) {
	document, err := parseApplyPatchDocument(patchText)
	if err != nil {
		return nil, err
	}
	targets := make([]string, 0, len(document.Operations))
	for _, operation := range document.Operations {
		targets = append(targets, strings.TrimSpace(operation.Path))
	}
	return targets, nil
}

func applyPatchUpdateOperation(filePath string, operation applyPatchFileOperation) (string, string, int, int, error) {
	if len(operation.Hunks) == 0 {
		return "", "", 0, 0, NewEditFailure(EditFailureInvalidPatchFormat, filePath, fmt.Sprintf("update section for %s did not contain any hunks", filePath), "Add one or more hunks with context lines and +/- changes under *** Update File.")
	}
	originalBytes, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
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

	originalContent := string(originalBytes)
	normalizedOriginal, originalLineEnding, hadTrailingNewline := normalizeFileForLineEditing(originalContent)
	replacements := make([]applyPatchReplacement, 0, len(operation.Hunks))
	for _, hunk := range operation.Hunks {
		replacement, err := locateApplyPatchHunk(normalizedOriginal, filePath, hunk)
		if err != nil {
			return "", "", 0, 0, err
		}
		replacements = append(replacements, replacement)
	}

	sort.Slice(replacements, func(i, j int) bool {
		return replacements[i].Start > replacements[j].Start
	})
	for i := 1; i < len(replacements); i++ {
		if replacements[i-1].Start < replacements[i].End {
			return "", "", 0, 0, NewEditFailure(EditFailureOverlap, filePath, fmt.Sprintf("patch hunks overlap in %s", filePath), "Split the patch into non-overlapping hunks or merge the overlapping edits into a single hunk.")
		}
	}

	updatedContent := normalizedOriginal
	for _, replacement := range replacements {
		updatedContent = updatedContent[:replacement.Start] + replacement.NewBlock + updatedContent[replacement.End:]
	}
	if updatedContent == normalizedOriginal {
		return "", "", 0, 0, NewEditFailure(EditFailureNoOp, filePath, fmt.Sprintf("patch did not change %s", filePath), "Adjust the hunk contents or skip this file if it is already in the desired state.")
	}
	if hadTrailingNewline && !strings.HasSuffix(updatedContent, "\n") {
		updatedContent += "\n"
	}
	if originalLineEnding == "\r\n" {
		updatedContent = strings.ReplaceAll(updatedContent, "\n", "\r\n")
	}
	preview, insertions, deletions := buildFileDiffPreview(normalizedOriginal, strings.ReplaceAll(updatedContent, "\r\n", "\n"))
	return updatedContent, preview, insertions, deletions, nil
}

func locateApplyPatchHunk(content, filePath string, hunk applyPatchHunk) (applyPatchReplacement, error) {
	oldLines := make([]string, 0, len(hunk.Lines))
	newLines := make([]string, 0, len(hunk.Lines))
	hasChange := false
	for _, line := range hunk.Lines {
		switch line.Kind {
		case ' ':
			oldLines = append(oldLines, line.Value)
			newLines = append(newLines, line.Value)
		case '-':
			hasChange = true
			oldLines = append(oldLines, line.Value)
		case '+':
			hasChange = true
			newLines = append(newLines, line.Value)
		default:
			return applyPatchReplacement{}, NewEditFailure(EditFailureInvalidPatchFormat, filePath, fmt.Sprintf("unsupported hunk line kind %q in %s", string(line.Kind), filePath), "Use context lines, - removals, and + additions inside update hunks.")
		}
	}
	if !hasChange {
		return applyPatchReplacement{}, NewEditFailure(EditFailureInvalidPatchFormat, filePath, fmt.Sprintf("update hunk for %s does not contain any changes", filePath), "Include at least one - removal or + addition in each update hunk.")
	}
	oldBlock := strings.Join(oldLines, "\n")
	newBlock := strings.Join(newLines, "\n")
	start, matchCount := findUniqueApplyPatchMatch(content, oldBlock)
	if matchCount == 0 {
		return applyPatchReplacement{}, NewEditFailure(EditFailureNoMatch, filePath, fmt.Sprintf("patch hunk did not match the current file contents: %s", filePath), "Reread the file and refresh the patch context so the old block matches exactly.")
	}
	if matchCount > 1 {
		return applyPatchReplacement{}, NewEditFailure(EditFailureMultipleMatch, filePath, fmt.Sprintf("patch hunk matched multiple locations in %s", filePath), "Include more surrounding context lines in the hunk so it matches exactly once.")
	}
	return applyPatchReplacement{
		Start:    start,
		End:      start + len(oldBlock),
		OldBlock: oldBlock,
		NewBlock: newBlock,
	}, nil
}

func findUniqueApplyPatchMatch(content, needle string) (int, int) {
	if needle == "" {
		return -1, 0
	}
	count := 0
	firstIndex := -1
	for offset := 0; offset <= len(content); {
		index := strings.Index(content[offset:], needle)
		if index < 0 {
			break
		}
		absoluteIndex := offset + index
		if count == 0 {
			firstIndex = absoluteIndex
		}
		count++
		offset = absoluteIndex + 1
	}
	return firstIndex, count
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
		case applyPatchActionAdd:
			verb = "added"
		case applyPatchActionDelete:
			verb = "deleted"
		}
		lines = append(lines, fmt.Sprintf("- %s %s (+%d -%d)", verb, change.path, change.insertions, change.deletions))
	}
	lines = append(lines, fmt.Sprintf("Total: +%d -%d", insertions, deletions))
	return strings.Join(lines, "\n")
}

func parseApplyPatchDocument(patchText string) (applyPatchDocument, error) {
	normalized := strings.ReplaceAll(patchText, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "*** Begin Patch" {
		return applyPatchDocument{}, NewEditFailure(EditFailureInvalidPatchFormat, "", "apply_patch must start with *** Begin Patch", "Wrap the patch body between *** Begin Patch and *** End Patch.")
	}

	document := applyPatchDocument{}
	var current *applyPatchFileOperation
	body := make([]string, 0)
	finishCurrent := func() error {
		if current == nil {
			return nil
		}
		operation, err := finalizeApplyPatchOperation(*current, body)
		if err != nil {
			return err
		}
		document.Operations = append(document.Operations, operation)
		current = nil
		body = body[:0]
		return nil
	}

	for index := 1; index < len(lines); index++ {
		line := lines[index]
		trimmed := strings.TrimSpace(line)
		if trimmed == "*** End Patch" {
			if err := finishCurrent(); err != nil {
				return applyPatchDocument{}, err
			}
			if index != len(lines)-1 && strings.TrimSpace(strings.Join(lines[index+1:], "")) != "" {
				return applyPatchDocument{}, NewEditFailure(EditFailureInvalidPatchFormat, "", "unexpected content after *** End Patch", "Remove any trailing lines after the end-of-patch marker.")
			}
			return document, nil
		}
		if strings.HasPrefix(trimmed, "*** Add File:") || strings.HasPrefix(trimmed, "*** Update File:") || strings.HasPrefix(trimmed, "*** Delete File:") {
			if err := finishCurrent(); err != nil {
				return applyPatchDocument{}, err
			}
			operation, err := parseApplyPatchHeader(trimmed)
			if err != nil {
				return applyPatchDocument{}, err
			}
			current = &operation
			continue
		}
		if current == nil {
			if trimmed == "" {
				continue
			}
			return applyPatchDocument{}, NewEditFailure(EditFailureInvalidPatchFormat, "", fmt.Sprintf("unexpected line outside a file section: %s", trimmed), "Start each file section with *** Add File, *** Update File, or *** Delete File.")
		}
		body = append(body, line)
	}

	return applyPatchDocument{}, NewEditFailure(EditFailureInvalidPatchFormat, "", "apply_patch must end with *** End Patch", "Add *** End Patch after the last file section.")
}

func parseApplyPatchHeader(line string) (applyPatchFileOperation, error) {
	parsePath := func(prefix string) (string, error) {
		pathValue := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		if pathValue == "" {
			return "", NewEditFailure(EditFailureInvalidPatchFormat, "", fmt.Sprintf("missing file path in patch header: %s", line), "Provide a file path after the patch action header.")
		}
		return pathValue, nil
	}
	switch {
	case strings.HasPrefix(line, "*** Add File:"):
		pathValue, err := parsePath("*** Add File:")
		if err != nil {
			return applyPatchFileOperation{}, err
		}
		return applyPatchFileOperation{Action: applyPatchActionAdd, Path: pathValue}, nil
	case strings.HasPrefix(line, "*** Update File:"):
		pathValue, err := parsePath("*** Update File:")
		if err != nil {
			return applyPatchFileOperation{}, err
		}
		return applyPatchFileOperation{Action: applyPatchActionUpdate, Path: pathValue}, nil
	case strings.HasPrefix(line, "*** Delete File:"):
		pathValue, err := parsePath("*** Delete File:")
		if err != nil {
			return applyPatchFileOperation{}, err
		}
		return applyPatchFileOperation{Action: applyPatchActionDelete, Path: pathValue}, nil
	default:
		return applyPatchFileOperation{}, NewEditFailure(EditFailureInvalidPatchFormat, "", fmt.Sprintf("unsupported patch header: %s", line), "Use *** Add File, *** Update File, or *** Delete File headers.")
	}
}

func finalizeApplyPatchOperation(operation applyPatchFileOperation, body []string) (applyPatchFileOperation, error) {
	switch operation.Action {
	case applyPatchActionAdd:
		contentLines := make([]string, 0, len(body))
		for _, line := range body {
			if strings.HasPrefix(line, "@@") {
				continue
			}
			if !strings.HasPrefix(line, "+") {
				return applyPatchFileOperation{}, NewEditFailure(EditFailureInvalidPatchFormat, operation.Path, fmt.Sprintf("add file sections only support + lines: %s", line), "Prefix every added line with + inside *** Add File sections.")
			}
			contentLines = append(contentLines, strings.TrimPrefix(line, "+"))
		}
		operation.Lines = contentLines
		return operation, nil
	case applyPatchActionDelete:
		for _, line := range body {
			if strings.TrimSpace(line) != "" {
				return applyPatchFileOperation{}, NewEditFailure(EditFailureInvalidPatchFormat, operation.Path, fmt.Sprintf("delete file sections cannot contain body lines: %s", strings.TrimSpace(line)), "Remove hunk lines from *** Delete File sections.")
			}
		}
		return operation, nil
	case applyPatchActionUpdate:
		hunks := make([]applyPatchHunk, 0)
		currentHunk := applyPatchHunk{}
		hasContent := false
		finishHunk := func() error {
			if len(currentHunk.Lines) == 0 {
				return nil
			}
			hunks = append(hunks, currentHunk)
			currentHunk = applyPatchHunk{}
			return nil
		}
		for _, line := range body {
			if strings.HasPrefix(line, "@@") {
				if err := finishHunk(); err != nil {
					return applyPatchFileOperation{}, err
				}
				continue
			}
			hasContent = true
			kind := byte(' ')
			value := line
			if line != "" {
				switch line[0] {
				case ' ', '+', '-':
					kind = line[0]
					value = line[1:]
				}
			}
			currentHunk.Lines = append(currentHunk.Lines, applyPatchLine{Kind: kind, Value: value})
		}
		if err := finishHunk(); err != nil {
			return applyPatchFileOperation{}, err
		}
		if !hasContent || len(hunks) == 0 {
			return applyPatchFileOperation{}, NewEditFailure(EditFailureInvalidPatchFormat, operation.Path, fmt.Sprintf("update section for %s did not contain any hunk lines", operation.Path), "Add context lines plus +/- changes under *** Update File sections.")
		}
		operation.Hunks = hunks
		return operation, nil
	default:
		return applyPatchFileOperation{}, NewEditFailure(EditFailureUnsupportedOperation, operation.Path, fmt.Sprintf("unsupported patch action: %s", operation.Action), "Use *** Add File, *** Update File, or *** Delete File sections only.")
	}
}
