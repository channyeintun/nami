package tools

import (
	"context"
	"fmt"
	"strings"
)

// ThinkTool provides a zero-side-effect scratchpad for intermediate reasoning.
type ThinkTool struct{}

// NewThinkTool constructs the think tool.
func NewThinkTool() *ThinkTool {
	return &ThinkTool{}
}

func (t *ThinkTool) Name() string {
	return "think"
}

func (t *ThinkTool) Description() string {
	return "Record intermediate reasoning with no side effects. Use it as a scratchpad when you need to structure thoughts before acting."
}

func (t *ThinkTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"thought": map[string]any{
				"type":        "string",
				"description": "The intermediate reasoning or scratchpad note to record.",
			},
		},
		"required": []string{"thought"},
	}
}

func (t *ThinkTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *ThinkTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *ThinkTool) Validate(input ToolInput) error {
	thought, ok := stringParam(input.Params, "thought")
	if !ok || strings.TrimSpace(thought) == "" {
		return fmt.Errorf("think requires thought")
	}
	return nil
}

func (t *ThinkTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	select {
	case <-ctx.Done():
		return ToolOutput{}, ctx.Err()
	default:
	}
	return ToolOutput{Output: "Thought recorded."}, nil
}
