package tools

import (
	"context"
	"fmt"
	"time"
)

type CommandStatusTool struct{}

func NewCommandStatusTool() *CommandStatusTool {
	return &CommandStatusTool{}
}

func (t *CommandStatusTool) Name() string {
	return "command_status"
}

func (t *CommandStatusTool) Description() string {
	return "Check the latest status, command metadata, timing context, and unread output for a previously started background command."
}

func (t *CommandStatusTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"CommandId": map[string]any{
				"type":        "string",
				"description": "The background command identifier returned by the bash tool.",
			},
			"command_id": map[string]any{
				"type":        "string",
				"description": "Snake_case alias for the background command identifier.",
			},
			"WaitDurationSeconds": map[string]any{
				"type":        "integer",
				"description": "Optional number of seconds to wait before checking for new output.",
				"minimum":     0,
			},
			"wait_duration_seconds": map[string]any{
				"type":        "integer",
				"description": "Snake_case alias for the optional wait duration in seconds.",
				"minimum":     0,
			},
		},
		"anyOf": []map[string]any{
			{"required": []string{"CommandId"}},
			{"required": []string{"command_id"}},
		},
	}
}

func (t *CommandStatusTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *CommandStatusTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *CommandStatusTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	select {
	case <-ctx.Done():
		return ToolOutput{}, ctx.Err()
	default:
	}

	commandID, ok := firstStringParam(input.Params, "CommandId", "command_id")
	if !ok || commandID == "" {
		return ToolOutput{}, fmt.Errorf("command_status requires CommandId")
	}
	waitSeconds, _ := firstIntParam(input.Params, "WaitDurationSeconds", "wait_duration_seconds")
	if waitSeconds < 0 {
		return ToolOutput{}, fmt.Errorf("WaitDurationSeconds must be >= 0")
	}

	bg, err := getBackgroundCommand(commandID)
	if err != nil {
		return ToolOutput{}, err
	}

	result, err := renderBackgroundCommandResult(bg.status(time.Duration(waitSeconds) * time.Second))
	if err != nil {
		return ToolOutput{}, fmt.Errorf("render command status: %w", err)
	}
	return ToolOutput{Output: result}, nil
}
