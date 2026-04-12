package tools

import (
	"context"
	"fmt"
	"time"
)

type SendCommandInputTool struct{}

func NewSendCommandInputTool() *SendCommandInputTool {
	return &SendCommandInputTool{}
}

func (t *SendCommandInputTool) Name() string {
	return "send_command_input"
}

func (t *SendCommandInputTool) Description() string {
	return "Send stdin to a running background command and return the updated command status plus any newly produced output."
}

func (t *SendCommandInputTool) InputSchema() any {
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
			"Input": map[string]any{
				"type":        "string",
				"description": "The exact stdin content to send to the process, usually including a trailing newline.",
			},
			"input": map[string]any{
				"type":        "string",
				"description": "Snake_case alias for the stdin content to send to the process.",
			},
			"WaitMs": map[string]any{
				"type":        "integer",
				"description": "Optional number of milliseconds to wait for fresh output after sending input.",
				"minimum":     0,
			},
			"wait_ms": map[string]any{
				"type":        "integer",
				"description": "Snake_case alias for the optional wait duration in milliseconds.",
				"minimum":     0,
			},
		},
		"allOf": []map[string]any{
			{
				"anyOf": []map[string]any{
					{"required": []string{"CommandId"}},
					{"required": []string{"command_id"}},
				},
			},
			{
				"anyOf": []map[string]any{
					{"required": []string{"Input"}},
					{"required": []string{"input"}},
				},
			},
		},
	}
}

func (t *SendCommandInputTool) Permission() PermissionLevel {
	return PermissionExecute
}

func (t *SendCommandInputTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *SendCommandInputTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	select {
	case <-ctx.Done():
		return ToolOutput{}, ctx.Err()
	default:
	}

	commandID, ok := firstStringParam(input.Params, "CommandId", "command_id")
	if !ok || commandID == "" {
		return ToolOutput{}, fmt.Errorf("send_command_input requires CommandId")
	}
	stdinInput, ok := firstStringParam(input.Params, "Input", "input")
	if !ok {
		return ToolOutput{}, fmt.Errorf("send_command_input requires Input")
	}
	waitMs, _ := firstIntParam(input.Params, "WaitMs", "wait_ms")
	if waitMs < 0 {
		return ToolOutput{}, fmt.Errorf("WaitMs must be >= 0")
	}

	bg, err := getBackgroundCommand(commandID)
	if err != nil {
		return ToolOutput{}, err
	}

	resultPayload, err := bg.sendInput(stdinInput, time.Duration(waitMs)*time.Millisecond)
	if err != nil {
		return ToolOutput{}, err
	}
	result, err := renderBackgroundCommandResult(resultPayload)
	if err != nil {
		return ToolOutput{}, fmt.Errorf("render command input result: %w", err)
	}
	return ToolOutput{Output: result}, nil
}
