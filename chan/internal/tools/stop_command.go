package tools

import (
	"context"
	"fmt"
	"time"
)

type StopCommandTool struct{}

func NewStopCommandTool() *StopCommandTool {
	return &StopCommandTool{}
}

func (t *StopCommandTool) Name() string {
	return "stop_command"
}

func (t *StopCommandTool) Description() string {
	return "Stop a running background command and return its final status, command metadata, and unread output."
}

func (t *StopCommandTool) InputSchema() any {
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
			"WaitMs": map[string]any{
				"type":        "integer",
				"description": "Optional number of milliseconds to wait for the process to exit after stopping it.",
				"minimum":     0,
			},
			"wait_ms": map[string]any{
				"type":        "integer",
				"description": "Snake_case alias for the optional wait duration in milliseconds.",
				"minimum":     0,
			},
		},
		"anyOf": []map[string]any{
			{"required": []string{"CommandId"}},
			{"required": []string{"command_id"}},
		},
	}
}

func (t *StopCommandTool) Permission() PermissionLevel {
	return PermissionExecute
}

func (t *StopCommandTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *StopCommandTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	select {
	case <-ctx.Done():
		return ToolOutput{}, ctx.Err()
	default:
	}

	commandID, ok := firstStringParam(input.Params, "CommandId", "command_id")
	if !ok || commandID == "" {
		return ToolOutput{}, fmt.Errorf("stop_command requires CommandId")
	}
	waitMs, _ := firstIntParam(input.Params, "WaitMs", "wait_ms")
	if waitMs < 0 {
		return ToolOutput{}, fmt.Errorf("WaitMs must be >= 0")
	}

	bg, err := getBackgroundCommand(commandID)
	if err != nil {
		return ToolOutput{}, err
	}

	resultPayload := bg.stop(time.Duration(waitMs) * time.Millisecond)
	result, err := renderBackgroundCommandResult(resultPayload)
	if err != nil {
		return ToolOutput{}, fmt.Errorf("render stop command result: %w", err)
	}
	return ToolOutput{Output: result}, nil
}
