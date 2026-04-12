package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const maxDiffPreviewLines = 12

type FileDiffPreviewTool struct{}

type fileDiffPreviewResult struct {
	FilePath      string `json:"file_path"`
	CompareSource string `json:"compare_source"`
	Changed       bool   `json:"changed"`
	Insertions    int    `json:"insertions"`
	Deletions     int    `json:"deletions"`
	Preview       string `json:"preview,omitempty"`
}

func NewFileDiffPreviewTool() *FileDiffPreviewTool {
	return &FileDiffPreviewTool{}
}

func (t *FileDiffPreviewTool) Name() string {
	return "file_diff_preview"
}

func (t *FileDiffPreviewTool) Description() string {
	return "Preview a compact diff between a file and either another file or proposed replacement content."
}

func (t *FileDiffPreviewTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "The absolute path to the baseline file.",
			},
			"compare_file_path": map[string]any{
				"type":        "string",
				"description": "Optional absolute path to the file to compare against.",
			},
			"compare_content": map[string]any{
				"type":        "string",
				"description": "Optional replacement content to compare against the file.",
			},
		},
		"required": []string{"file_path"},
	}
}

func (t *FileDiffPreviewTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *FileDiffPreviewTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *FileDiffPreviewTool) Validate(input ToolInput) error {
	compareFilePath, hasCompareFile := stringParam(input.Params, "compare_file_path")
	_, hasCompareContent := stringParam(input.Params, "compare_content")
	hasCompareFile = hasCompareFile && strings.TrimSpace(compareFilePath) != ""
	if hasCompareFile == hasCompareContent {
		return fmt.Errorf("file_diff_preview requires exactly one of compare_file_path or compare_content")
	}
	return nil
}

func (t *FileDiffPreviewTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	select {
	case <-ctx.Done():
		return ToolOutput{}, ctx.Err()
	default:
	}

	filePath, ok := stringParam(input.Params, "file_path")
	if !ok || strings.TrimSpace(filePath) == "" {
		return ToolOutput{}, fmt.Errorf("file_diff_preview requires file_path")
	}
	filePath, err := resolveToolPath(filePath)
	if err != nil {
		return ToolOutput{}, err
	}

	baselineBytes, err := os.ReadFile(filePath)
	if err != nil {
		return ToolOutput{}, fmt.Errorf("read baseline file %q: %w", filePath, err)
	}
	baseline := string(baselineBytes)

	compareSource := "inline_content"
	compareValue := ""
	if compareFilePath, ok := stringParam(input.Params, "compare_file_path"); ok && strings.TrimSpace(compareFilePath) != "" {
		compareFilePath, err = resolveToolPath(compareFilePath)
		if err != nil {
			return ToolOutput{}, err
		}
		compareBytes, err := os.ReadFile(compareFilePath)
		if err != nil {
			return ToolOutput{}, fmt.Errorf("read comparison file %q: %w", compareFilePath, err)
		}
		compareSource = compareFilePath
		compareValue = string(compareBytes)
	} else if compareContent, ok := stringParam(input.Params, "compare_content"); ok {
		compareValue = compareContent
	}

	preview, insertions, deletions := buildFileDiffPreview(baseline, compareValue)
	result := fileDiffPreviewResult{
		FilePath:      filePath,
		CompareSource: compareSource,
		Changed:       insertions > 0 || deletions > 0,
		Insertions:    insertions,
		Deletions:     deletions,
		Preview:       preview,
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return ToolOutput{}, fmt.Errorf("marshal file_diff_preview: %w", err)
	}
	return ToolOutput{Output: string(encoded), Preview: preview, FilePath: filePath, Insertions: insertions, Deletions: deletions}, nil
}

func buildFileDiffPreview(oldContent, newContent string) (string, int, int) {
	oldLines := splitDiffLines(oldContent)
	newLines := splitDiffLines(newContent)

	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}

	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix {
		oldIndex := len(oldLines) - 1 - suffix
		newIndex := len(newLines) - 1 - suffix
		if oldLines[oldIndex] != newLines[newIndex] {
			break
		}
		suffix++
	}

	changedOld := oldLines[prefix : len(oldLines)-suffix]
	changedNew := newLines[prefix : len(newLines)-suffix]
	insertions := len(changedNew)
	deletions := len(changedOld)

	previewLines := make([]string, 0, min(maxDiffPreviewLines, insertions+deletions+1))
	if prefix > 0 {
		previewLines = append(previewLines, "@@")
	}
	for _, line := range changedOld {
		if len(previewLines) >= maxDiffPreviewLines {
			break
		}
		previewLines = append(previewLines, "-"+line)
	}
	for _, line := range changedNew {
		if len(previewLines) >= maxDiffPreviewLines {
			break
		}
		previewLines = append(previewLines, "+"+line)
	}
	if insertions+deletions > len(previewLines) {
		previewLines = append(previewLines[:maxDiffPreviewLines-1], "...")
	}

	return strings.Join(previewLines, "\n"), insertions, deletions
}

func splitDiffLines(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}
