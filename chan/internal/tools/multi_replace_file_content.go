package tools

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
)

type MultiReplaceFileContentTool struct{}

type replacementChunk struct {
	StartLine          int
	EndLine            int
	TargetContent      string
	ReplacementContent string
}

func NewMultiReplaceFileContentTool() *MultiReplaceFileContentTool {
	return &MultiReplaceFileContentTool{}

}

func (t *MultiReplaceFileContentTool) Name() string {
	return "multi_replace_file_content"
}

func (t *MultiReplaceFileContentTool) Description() string {
	return "Apply several validated, non-overlapping exact replacements to one existing text file in one write. Best when you know the current line ranges and target text. Use file_edit for one exact snippet replacement and apply_patch for multi-file or larger structural edits."
}

func (t *MultiReplaceFileContentTool) Validate(input ToolInput) error {
	targetFile, ok := firstStringParam(input.Params, "TargetFile", "target_file")
	if !ok || strings.TrimSpace(targetFile) == "" {
		return NewEditFailure(EditFailureInvalidRequest, "", "multi_replace_file_content requires TargetFile", "Provide the absolute path to the existing file you want to modify.")
	}
	resolvedPath, err := resolveToolPath(targetFile)
	if err != nil {
		return err
	}
	info, err := os.Stat(resolvedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return NewEditFailure(EditFailureTargetMissing, resolvedPath, fmt.Sprintf("file does not exist: %s", resolvedPath), "Use create_file to create it first, then retry multi_replace_file_content with fresh line ranges.")
		}
		return fmt.Errorf("stat target file %q: %w", resolvedPath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%q is a directory", resolvedPath)
	}
	chunks, err := parseReplacementChunks(input.Params)
	if err != nil {
		return err
	}
	if len(chunks) == 0 {
		return NewEditFailure(EditFailureInvalidRequest, resolvedPath, "multi_replace_file_content requires at least one replacement chunk", "Add one or more replacement chunks with exact line ranges and target content, or use file_edit for one exact replacement.")
	}
	return nil
}

func (t *MultiReplaceFileContentTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"TargetFile": map[string]any{
				"type":        "string",
				"description": "The absolute path to the file to modify.",
			},
			"target_file": map[string]any{
				"type":        "string",
				"description": "Snake_case alias for the path to the file to modify.",
			},
			"ReplacementChunks": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"StartLine":           map[string]any{"type": "integer", "minimum": 1},
						"EndLine":             map[string]any{"type": "integer", "minimum": 1},
						"TargetContent":       map[string]any{"type": "string"},
						"ReplacementContent":  map[string]any{"type": "string"},
						"start_line":          map[string]any{"type": "integer", "minimum": 1},
						"end_line":            map[string]any{"type": "integer", "minimum": 1},
						"target_content":      map[string]any{"type": "string"},
						"replacement_content": map[string]any{"type": "string"},
					},
					"allOf": []map[string]any{
						{
							"anyOf": []map[string]any{
								{"required": []string{"StartLine"}},
								{"required": []string{"start_line"}},
							},
						},
						{
							"anyOf": []map[string]any{
								{"required": []string{"EndLine"}},
								{"required": []string{"end_line"}},
							},
						},
						{
							"anyOf": []map[string]any{
								{"required": []string{"TargetContent"}},
								{"required": []string{"target_content"}},
							},
						},
						{
							"anyOf": []map[string]any{
								{"required": []string{"ReplacementContent"}},
								{"required": []string{"replacement_content"}},
							},
						},
					},
				},
			},
			"replacement_chunks": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"StartLine":           map[string]any{"type": "integer", "minimum": 1},
						"EndLine":             map[string]any{"type": "integer", "minimum": 1},
						"TargetContent":       map[string]any{"type": "string"},
						"ReplacementContent":  map[string]any{"type": "string"},
						"start_line":          map[string]any{"type": "integer", "minimum": 1},
						"end_line":            map[string]any{"type": "integer", "minimum": 1},
						"target_content":      map[string]any{"type": "string"},
						"replacement_content": map[string]any{"type": "string"},
					},
					"allOf": []map[string]any{
						{
							"anyOf": []map[string]any{
								{"required": []string{"StartLine"}},
								{"required": []string{"start_line"}},
							},
						},
						{
							"anyOf": []map[string]any{
								{"required": []string{"EndLine"}},
								{"required": []string{"end_line"}},
							},
						},
						{
							"anyOf": []map[string]any{
								{"required": []string{"TargetContent"}},
								{"required": []string{"target_content"}},
							},
						},
						{
							"anyOf": []map[string]any{
								{"required": []string{"ReplacementContent"}},
								{"required": []string{"replacement_content"}},
							},
						},
					},
				},
			},
		},
		"allOf": []map[string]any{
			{
				"anyOf": []map[string]any{
					{"required": []string{"TargetFile"}},
					{"required": []string{"target_file"}},
				},
			},
			{
				"anyOf": []map[string]any{
					{"required": []string{"ReplacementChunks"}},
					{"required": []string{"replacement_chunks"}},
				},
			},
		},
	}
}

func (t *MultiReplaceFileContentTool) Permission() PermissionLevel {
	return PermissionWrite
}

func (t *MultiReplaceFileContentTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *MultiReplaceFileContentTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	select {
	case <-ctx.Done():
		return ToolOutput{}, ctx.Err()
	default:
	}

	targetFile, ok := firstStringParam(input.Params, "TargetFile", "target_file")
	if !ok || strings.TrimSpace(targetFile) == "" {
		return ToolOutput{}, NewEditFailure(EditFailureInvalidRequest, "", "multi_replace_file_content requires TargetFile", "Provide the absolute path to the existing file you want to modify.")
	}
	targetFile, err := resolveToolPath(targetFile)
	if err != nil {
		return ToolOutput{}, err
	}

	chunks, err := parseReplacementChunks(input.Params)
	if err != nil {
		return ToolOutput{}, err
	}
	if len(chunks) == 0 {
		return ToolOutput{}, NewEditFailure(EditFailureInvalidRequest, targetFile, "multi_replace_file_content requires at least one replacement chunk", "Add one or more replacement chunks with exact line ranges and target content, or use file_edit for one exact replacement.")
	}

	originalBytes, err := os.ReadFile(targetFile)
	if err != nil {
		if os.IsNotExist(err) {
			return EditFailureOutput(EditFailureTargetMissing, targetFile, fmt.Sprintf("file does not exist: %s", targetFile), "Use create_file to create it first, then retry multi_replace_file_content with fresh line ranges."), nil
		}
		return ToolOutput{}, fmt.Errorf("read target file %q: %w", targetFile, err)
	}

	originalContent := string(originalBytes)
	normalizedOriginal, originalLineEnding, hadTrailingNewline := normalizeFileForLineEditing(originalContent)
	lines := splitIntoLogicalLines(normalizedOriginal)

	sortedChunks := append([]replacementChunk(nil), chunks...)
	sort.Slice(sortedChunks, func(i, j int) bool {
		if sortedChunks[i].StartLine == sortedChunks[j].StartLine {
			return sortedChunks[i].EndLine > sortedChunks[j].EndLine
		}
		return sortedChunks[i].StartLine > sortedChunks[j].StartLine
	})

	if err := validateReplacementChunks(lines, sortedChunks); err != nil {
		if editFailure, ok := ExtractEditFailure(err); ok {
			return EditFailureOutput(editFailure.Kind, editFailure.FilePath, editFailure.Message, editFailure.Hint), nil
		}
		return ToolOutput{}, err
	}

	noOpChunks := 0
	for _, chunk := range sortedChunks {
		if chunk.TargetContent == chunk.ReplacementContent {
			noOpChunks++
		}
	}
	if noOpChunks == len(sortedChunks) {
		return EditFailureOutput(EditFailureNoOp, targetFile, "no changes to make: every replacement chunk already matches its replacement content", "Skip the edit, adjust the replacement chunks so they actually change the file, or use apply_patch if the intended change is more structural than line-ranged replacement."), nil
	}

	updatedLines := append([]string(nil), lines...)
	for _, chunk := range sortedChunks {
		select {
		case <-ctx.Done():
			return ToolOutput{}, ctx.Err()
		default:
		}

		startIdx := chunk.StartLine - 1
		endIdx := chunk.EndLine
		replacementLines := strings.Split(chunk.ReplacementContent, "\n")
		prefix := append([]string(nil), updatedLines[:startIdx]...)
		suffix := append([]string(nil), updatedLines[endIdx:]...)

		combined := make([]string, 0, len(prefix)+len(replacementLines)+len(suffix))
		combined = append(combined, prefix...)
		combined = append(combined, replacementLines...)
		combined = append(combined, suffix...)
		updatedLines = combined
	}

	updatedContent := strings.Join(updatedLines, "\n")
	if hadTrailingNewline {
		updatedContent += "\n"
	}
	if originalLineEnding == "\r\n" {
		updatedContent = strings.ReplaceAll(updatedContent, "\n", "\r\n")
	}

	trackFileBeforeWrite(targetFile)
	if err := os.WriteFile(targetFile, []byte(updatedContent), 0o644); err != nil {
		return ToolOutput{}, fmt.Errorf("write file %q: %w", targetFile, err)
	}

	preview, insertions, deletions := buildFileDiffPreview(normalizedOriginal, strings.ReplaceAll(updatedContent, "\r\n", "\n"))
	diagnostics := runPostEditDiagnostics(ctx, []string{targetFile})
	return ToolOutput{
		Output:      fmt.Sprintf("Edited file successfully: %s (%d replacement chunk%s)", targetFile, len(sortedChunks), pluralSuffix(len(sortedChunks))),
		FilePath:    targetFile,
		Preview:     preview,
		Insertions:  insertions,
		Deletions:   deletions,
		Diagnostics: diagnostics,
	}, nil
}

func parseReplacementChunks(params map[string]any) ([]replacementChunk, error) {
	rawChunks, ok := firstParam(params, "ReplacementChunks", "replacement_chunks")
	if !ok {
		return nil, NewEditFailure(EditFailureInvalidRequest, "", "multi_replace_file_content requires ReplacementChunks", "Provide replacement_chunks with exact line ranges, target_content, and replacement_content.")
	}

	rawSlice, ok := rawChunks.([]any)
	if !ok {
		return nil, NewEditFailure(EditFailureInvalidRequest, "", "ReplacementChunks must be an array", "Provide replacement_chunks as an array of chunk objects.")
	}

	chunks := make([]replacementChunk, 0, len(rawSlice))
	for index, raw := range rawSlice {
		chunkMap, ok := raw.(map[string]any)
		if !ok {
			return nil, NewEditFailure(EditFailureInvalidRequest, "", fmt.Sprintf("ReplacementChunks[%d] must be an object", index), "Each replacement chunk must be an object with line range and content fields.")
		}

		startLine, ok := firstIntParam(chunkMap, "StartLine", "start_line")
		if !ok {
			return nil, NewEditFailure(EditFailureInvalidRequest, "", fmt.Sprintf("ReplacementChunks[%d] requires StartLine", index), "Add start_line for every replacement chunk.")
		}
		endLine, ok := firstIntParam(chunkMap, "EndLine", "end_line")
		if !ok {
			return nil, NewEditFailure(EditFailureInvalidRequest, "", fmt.Sprintf("ReplacementChunks[%d] requires EndLine", index), "Add end_line for every replacement chunk.")
		}
		targetContent, ok := firstStringParam(chunkMap, "TargetContent", "target_content")
		if !ok {
			return nil, NewEditFailure(EditFailureInvalidRequest, "", fmt.Sprintf("ReplacementChunks[%d] requires TargetContent", index), "Add target_content for every replacement chunk.")
		}
		replacementContent, ok := firstStringParam(chunkMap, "ReplacementContent", "replacement_content")
		if !ok {
			return nil, NewEditFailure(EditFailureInvalidRequest, "", fmt.Sprintf("ReplacementChunks[%d] requires ReplacementContent", index), "Add replacement_content for every replacement chunk.")
		}

		chunks = append(chunks, replacementChunk{
			StartLine:          startLine,
			EndLine:            endLine,
			TargetContent:      strings.ReplaceAll(targetContent, "\r\n", "\n"),
			ReplacementContent: strings.ReplaceAll(replacementContent, "\r\n", "\n"),
		})
	}

	return chunks, nil
}

func validateReplacementChunks(lines []string, chunks []replacementChunk) error {
	previousStart := len(lines) + 1
	for _, chunk := range chunks {
		if chunk.StartLine < 1 || chunk.EndLine < chunk.StartLine {
			return NewEditFailure(EditFailureInvalidRange, "", fmt.Sprintf("invalid line range %d-%d", chunk.StartLine, chunk.EndLine), "Reread the file and provide a valid inclusive line range where start_line <= end_line, or switch to apply_patch if the edit is easier to express as contextual hunks.")
		}
		if chunk.EndLine > len(lines) {
			return NewEditFailure(EditFailureInvalidRange, "", fmt.Sprintf("replacement chunk line range %d-%d exceeds file length %d", chunk.StartLine, chunk.EndLine, len(lines)), "Reread the file and refresh the line numbers before retrying the edit, or use apply_patch if exact line ranges are unstable.")
		}
		if chunk.EndLine >= previousStart {
			return NewEditFailure(EditFailureOverlap, "", fmt.Sprintf("replacement chunks overlap at lines %d-%d", chunk.StartLine, chunk.EndLine), "Split or merge the overlapping chunks so each line is changed by at most one chunk.")
		}

		currentSnippet := strings.Join(lines[chunk.StartLine-1:chunk.EndLine], "\n")
		if currentSnippet != chunk.TargetContent {
			return NewEditFailure(EditFailureContentMismatch, "", fmt.Sprintf("target content mismatch for lines %d-%d", chunk.StartLine, chunk.EndLine), "Reread that range and update target_content so it matches the current file exactly, or use apply_patch if contextual hunks are a better fit than fixed line ranges.")
		}
		previousStart = chunk.StartLine
	}
	return nil
}

func normalizeFileForLineEditing(content string) (string, string, bool) {
	lineEnding := "\n"
	if strings.Contains(content, "\r\n") {
		lineEnding = "\r\n"
	}
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	hadTrailingNewline := strings.HasSuffix(normalized, "\n")
	normalized = strings.TrimSuffix(normalized, "\n")
	return normalized, lineEnding, hadTrailingNewline
}

func splitIntoLogicalLines(content string) []string {
	if content == "" {
		return []string{}
	}
	return strings.Split(content, "\n")
}
