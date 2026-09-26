package tools

import (
	"context"
	"fmt"
	"strings"
)

type MultiReplaceStringInFileTool struct{}

func NewMultiReplaceStringInFileTool() *MultiReplaceStringInFileTool {
	return &MultiReplaceStringInFileTool{}
}

func (t *MultiReplaceStringInFileTool) Name() string {
	return "multi_replace_string_in_file"
}

func (t *MultiReplaceStringInFileTool) Description() string {
	return "Apply multiple replace_string_in_file operations in a single call. Prefer this as the first choice when several exact literal replacements are needed across one file or a small set of files and you want to avoid many separate edit calls."
}

func (t *MultiReplaceStringInFileTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"explanation": map[string]any{
				"type":        "string",
				"description": "A short description of what the grouped replacements are intended to accomplish.",
			},
			"replacements": map[string]any{
				"type":        "array",
				"description": "An array of exact replacement operations to apply sequentially.",
				"minItems":    1,
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"filePath": map[string]any{
							"type":        "string",
							"description": "The absolute path to the file to modify.",
						},
						"oldString": map[string]any{
							"type":        "string",
							"description": "The exact literal text to replace. Include enough surrounding context to uniquely identify the target occurrence.",
							"minLength":   1,
						},
						"newString": map[string]any{
							"type":        "string",
							"description": "The replacement text. Use an empty string to delete oldString.",
							"minLength":   0,
						},
						"replaceAll": map[string]any{
							"type":        "boolean",
							"description": "Replace all occurrences of oldString instead of one for this replacement.",
						},
					},
					"required": []string{"filePath", "oldString", "newString"},
				},
			},
		},
		"required": []string{"explanation", "replacements"},
	}
}

func (t *MultiReplaceStringInFileTool) Permission() PermissionLevel {
	return PermissionWrite
}

func (t *MultiReplaceStringInFileTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *MultiReplaceStringInFileTool) Validate(input ToolInput) error {
	if explanation, ok := stringParam(input.Params, "explanation"); !ok || strings.TrimSpace(explanation) == "" {
		return fmt.Errorf("multi_replace_string_in_file requires explanation")
	}
	replacements, ok := input.Params["replacements"].([]any)
	if !ok || len(replacements) == 0 {
		return fmt.Errorf("multi_replace_string_in_file requires at least one replacement")
	}
	delegate := NewFileEditTool()
	for index, raw := range replacements {
		params, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("multi_replace_string_in_file replacement %d must be an object", index)
		}
		if err := ValidateToolCall(delegate, ToolInput{Name: delegate.Name(), Params: params}); err != nil {
			return fmt.Errorf("multi_replace_string_in_file replacement %d: %w", index, err)
		}
	}
	return nil
}

func (t *MultiReplaceStringInFileTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	replacements, ok := input.Params["replacements"].([]any)
	if !ok || len(replacements) == 0 {
		return ToolOutput{}, fmt.Errorf("multi_replace_string_in_file requires at least one replacement")
	}

	delegate := NewFileEditTool()
	results := make([]string, 0, len(replacements))
	changedFiles := make([]string, 0, len(replacements))
	encounteredError := false

	for index, raw := range replacements {
		select {
		case <-ctx.Done():
			return ToolOutput{}, ctx.Err()
		default:
		}

		params, ok := raw.(map[string]any)
		if !ok {
			return ToolOutput{}, fmt.Errorf("multi_replace_string_in_file replacement %d must be an object", index)
		}

		result, err := delegate.Execute(ctx, ToolInput{
			Name:   delegate.Name(),
			Params: params,
		})
		if err != nil {
			return ToolOutput{}, fmt.Errorf("multi_replace_string_in_file replacement %d: %w", index, err)
		}

		label := fmt.Sprintf("%d. %s", index+1, strings.TrimSpace(result.Output))
		results = append(results, label)
		if strings.TrimSpace(result.FilePath) != "" {
			changedFiles = append(changedFiles, result.FilePath)
		}
		if result.IsError {
			encounteredError = true
		}
	}

	summary := fmt.Sprintf("Applied %d grouped replacements", len(replacements))
	if encounteredError {
		summary += " with one or more failures"
	}

	return ToolOutput{
		Output:  summary + "\n" + strings.Join(results, "\n"),
		IsError: encounteredError,
		FilePath: func() string {
			if len(changedFiles) == 1 {
				return changedFiles[0]
			}
			if len(changedFiles) > 1 {
				return fmt.Sprintf("%d files", len(changedFiles))
			}
			return ""
		}(),
	}, nil
}
