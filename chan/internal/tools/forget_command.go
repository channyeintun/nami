package tools

import (
	"context"
	"fmt"
)

type ForgetCommandTool struct{}

func NewForgetCommandTool() *ForgetCommandTool {
	return &ForgetCommandTool{}
}

func (t *ForgetCommandTool) Name() string {
	return "forget_command"
}

func (t *ForgetCommandTool) Description() string {
	return "Remove a completed, stopped, or failed background command from retention and return its final metadata plus any unread output."
}

func (t *ForgetCommandTool) InputSchema() any {
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
		},
		"anyOf": []map[string]any{
			{"required": []string{"CommandId"}},
			{"required": []string{"command_id"}},
		},
	}
}

func (t *ForgetCommandTool) Permission() PermissionLevel {
	return PermissionExecute
}

func (t *ForgetCommandTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *ForgetCommandTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	select {
	case <-ctx.Done():
		return ToolOutput{}, ctx.Err()
	default:
	}

	commandID, ok := firstStringParam(input.Params, "CommandId", "command_id")
	if !ok || commandID == "" {
		return ToolOutput{}, fmt.Errorf("forget_command requires CommandId")
	}

	resultPayload, err := forgetBackgroundCommand(commandID)
	if err != nil {
		return ToolOutput{}, err
	}

	result, err := renderBackgroundCommandResult(resultPayload)
	if err != nil {
		return ToolOutput{}, fmt.Errorf("render forget command result: %w", err)
	}
	return ToolOutput{Output: result}, nil
}
