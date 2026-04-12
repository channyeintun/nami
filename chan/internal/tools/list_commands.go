package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

const defaultListCommandsMaxResults = 20

type ListCommandsTool struct{}

type backgroundCommandSummary struct {
	CommandID       string    `json:"CommandId"`
	Command         string    `json:"Command"`
	Cwd             string    `json:"Cwd"`
	Running         bool      `json:"Running"`
	Error           string    `json:"Error,omitempty"`
	ExitCode        *int      `json:"ExitCode,omitempty"`
	StartedAt       time.Time `json:"StartedAt,omitempty"`
	UpdatedAt       time.Time `json:"UpdatedAt,omitempty"`
	HasUnreadOutput bool      `json:"HasUnreadOutput,omitempty"`
	UnreadBytes     int       `json:"UnreadBytes,omitempty"`
	UnreadPreview   string    `json:"UnreadPreview,omitempty"`
}

func NewListCommandsTool() *ListCommandsTool {
	return &ListCommandsTool{}
}

func (t *ListCommandsTool) Name() string {
	return "list_commands"
}

func (t *ListCommandsTool) Description() string {
	return "List active or recently completed background commands, including ids, command text, cwd, run state, recent activity, and unread output previews."
}

func (t *ListCommandsTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"include_completed": map[string]any{
				"type":        "boolean",
				"description": "Include recently completed retained commands. Defaults to false.",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Optional maximum number of commands to return. Defaults to 20.",
				"minimum":     1,
			},
		},
	}
}

func (t *ListCommandsTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *ListCommandsTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *ListCommandsTool) Validate(input ToolInput) error {
	if value, ok := intParam(input.Params, "max_results"); ok && value < 1 {
		return fmt.Errorf("max_results must be >= 1")
	}
	return nil
}

func (t *ListCommandsTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	select {
	case <-ctx.Done():
		return ToolOutput{}, ctx.Err()
	default:
	}

	includeCompleted := boolOrDefault(input.Params, "include_completed", false)
	maxResults := intOrDefault(input.Params, "max_results", defaultListCommandsMaxResults)
	commands := listBackgroundCommands(includeCompleted)

	sort.Slice(commands, func(i, j int) bool {
		if commands[i].Running != commands[j].Running {
			return commands[i].Running
		}
		if !commands[i].UpdatedAt.Equal(commands[j].UpdatedAt) {
			return commands[i].UpdatedAt.After(commands[j].UpdatedAt)
		}
		return commands[i].CommandID < commands[j].CommandID
	})

	truncated := false
	if len(commands) > maxResults {
		commands = commands[:maxResults]
		truncated = true
	}

	encoded, err := json.MarshalIndent(commands, "", "  ")
	if err != nil {
		return ToolOutput{}, fmt.Errorf("marshal list_commands: %w", err)
	}

	return ToolOutput{Output: string(encoded), Truncated: truncated}, nil
}
