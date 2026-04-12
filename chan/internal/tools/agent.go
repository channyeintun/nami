package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const subagentTypeExplore = "explore"
const subagentTypeSearch = "search"
const subagentTypeExecution = "execution"
const subagentTypeGeneralPurpose = "general-purpose"

type AgentRunRequest struct {
	Description  string
	Prompt       string
	SubagentType string
	Background   bool
}

type ChildAgentMetadata struct {
	InvocationID    string   `json:"invocation_id,omitempty"`
	AgentID         string   `json:"agent_id,omitempty"`
	Description     string   `json:"description,omitempty"`
	SubagentType    string   `json:"subagent_type,omitempty"`
	LifecycleState  string   `json:"lifecycle_state,omitempty"`
	StatusMessage   string   `json:"status_message,omitempty"`
	StopBlockReason string   `json:"stop_block_reason,omitempty"`
	StopBlockCount  int      `json:"stop_block_count,omitempty"`
	SessionID       string   `json:"session_id,omitempty"`
	TranscriptPath  string   `json:"transcript_path,omitempty"`
	ResultPath      string   `json:"result_path,omitempty"`
	Tools           []string `json:"tools,omitempty"`
}

type AgentRunResult struct {
	Status         string              `json:"status"`
	InvocationID   string              `json:"invocation_id,omitempty"`
	AgentID        string              `json:"agent_id,omitempty"`
	SubagentType   string              `json:"subagent_type"`
	SessionID      string              `json:"session_id"`
	TranscriptPath string              `json:"transcript_path"`
	OutputFile     string              `json:"output_file,omitempty"`
	Summary        string              `json:"summary"`
	Error          string              `json:"error,omitempty"`
	TotalCostUSD   float64             `json:"total_cost_usd,omitempty"`
	InputTokens    int                 `json:"input_tokens,omitempty"`
	OutputTokens   int                 `json:"output_tokens,omitempty"`
	Tools          []string            `json:"tools,omitempty"`
	Metadata       *ChildAgentMetadata `json:"metadata,omitempty"`
}

type AgentRunner func(context.Context, AgentRunRequest) (AgentRunResult, error)

type AgentStatusRequest struct {
	AgentID string
	WaitMs  int
}

type AgentStatusLookup func(context.Context, AgentStatusRequest) (AgentRunResult, error)

type AgentStopRequest struct {
	AgentID string
	WaitMs  int
}

type AgentStopLookup func(context.Context, AgentStopRequest) (AgentRunResult, error)

type AgentTool struct {
	runner AgentRunner
}

func NewAgentTool(runner AgentRunner) *AgentTool {
	return &AgentTool{runner: runner}
}

func (t *AgentTool) Name() string {
	return "agent"
}

func (t *AgentTool) Description() string {
	return "Spawn a bounded child agent in a fresh context. Prefer search for code discovery, execution for terminal-heavy tasks, explore for broad read-only research, and general-purpose for broader delegated work."
}

func (t *AgentTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"description": map[string]any{
				"type":        "string",
				"description": "Short 3-5 word summary of the delegated task.",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "The full task description for the child agent.",
			},
			"subagent_type": map[string]any{
				"type":        "string",
				"description": "The child agent type: explore for broad read-only research, search for iterative code discovery that returns file and line references, execution for terminal-heavy tasks like builds, tests, and log inspection, and general-purpose for broader delegated work.",
				"enum":        []string{subagentTypeExplore, subagentTypeSearch, subagentTypeExecution, subagentTypeGeneralPurpose},
			},
			"run_in_background": map[string]any{
				"type":        "boolean",
				"description": "Launch the child agent asynchronously and return an agent_id for later status checks.",
			},
		},
		"required": []string{"description", "prompt"},
	}
}

func (t *AgentTool) Permission() PermissionLevel {
	return PermissionExecute
}

func (t *AgentTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *AgentTool) Validate(input ToolInput) error {
	description, ok := stringParam(input.Params, "description")
	if !ok || strings.TrimSpace(description) == "" {
		return fmt.Errorf("agent requires description")
	}
	prompt, ok := stringParam(input.Params, "prompt")
	if !ok || strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("agent requires prompt")
	}
	if subagentType, ok := stringParam(input.Params, "subagent_type"); ok && strings.TrimSpace(subagentType) != "" && !IsSupportedSubagentType(strings.TrimSpace(subagentType)) {
		return fmt.Errorf("agent subagent_type %q is not supported", subagentType)
	}
	return nil
}

func (t *AgentTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	if t == nil || t.runner == nil {
		return ToolOutput{}, fmt.Errorf("agent runner is not configured")
	}

	description, _ := stringParam(input.Params, "description")
	prompt, _ := stringParam(input.Params, "prompt")
	subagentType, _ := stringParam(input.Params, "subagent_type")
	if strings.TrimSpace(subagentType) == "" {
		subagentType = subagentTypeExplore
	}

	result, err := t.runner(ctx, AgentRunRequest{
		Description:  strings.TrimSpace(description),
		Prompt:       strings.TrimSpace(prompt),
		SubagentType: strings.TrimSpace(subagentType),
		Background:   boolOrDefault(input.Params, "run_in_background", false),
	})
	if err != nil {
		return ToolOutput{}, err
	}

	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return ToolOutput{}, fmt.Errorf("marshal agent result: %w", err)
	}
	return ToolOutput{Output: string(encoded)}, nil
}

// IsSupportedSubagentType reports whether subagentType is a recognized subagent mode.
func IsSupportedSubagentType(subagentType string) bool {
	switch subagentType {
	case subagentTypeExplore, subagentTypeSearch, subagentTypeExecution, subagentTypeGeneralPurpose:
		return true
	default:
		return false
	}
}

type AgentStatusTool struct {
	lookup AgentStatusLookup
}

func NewAgentStatusTool(lookup AgentStatusLookup) *AgentStatusTool {
	return &AgentStatusTool{lookup: lookup}
}

func (t *AgentStatusTool) Name() string {
	return "agent_status"
}

func (t *AgentStatusTool) Description() string {
	return "Check the latest status for a background child agent and retrieve its final report when complete."
}

func (t *AgentStatusTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"agent_id": map[string]any{
				"type":        "string",
				"description": "The background child agent identifier returned by the agent tool.",
			},
			"wait_ms": map[string]any{
				"type":        "integer",
				"description": "Optional number of milliseconds to wait for completion before returning status.",
				"minimum":     0,
			},
		},
		"required": []string{"agent_id"},
	}
}

func (t *AgentStatusTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *AgentStatusTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *AgentStatusTool) Validate(input ToolInput) error {
	agentID, ok := stringParam(input.Params, "agent_id")
	if !ok || strings.TrimSpace(agentID) == "" {
		return fmt.Errorf("agent_status requires agent_id")
	}
	if value, ok := intParam(input.Params, "wait_ms"); ok && value < 0 {
		return fmt.Errorf("wait_ms must be >= 0")
	}
	return nil
}

func (t *AgentStatusTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	if t == nil || t.lookup == nil {
		return ToolOutput{}, fmt.Errorf("agent status lookup is not configured")
	}
	agentID, _ := stringParam(input.Params, "agent_id")
	result, err := t.lookup(ctx, AgentStatusRequest{
		AgentID: strings.TrimSpace(agentID),
		WaitMs:  intOrDefault(input.Params, "wait_ms", 0),
	})
	if err != nil {
		return ToolOutput{}, err
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return ToolOutput{}, fmt.Errorf("marshal agent status: %w", err)
	}
	return ToolOutput{Output: string(encoded)}, nil
}

type AgentStopTool struct {
	lookup AgentStopLookup
}

func NewAgentStopTool(lookup AgentStopLookup) *AgentStopTool {
	return &AgentStopTool{lookup: lookup}
}

func (t *AgentStopTool) Name() string {
	return "agent_stop"
}

func (t *AgentStopTool) Description() string {
	return "Request a background child agent to stop and return its latest status or final cancellation result."
}

func (t *AgentStopTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"agent_id": map[string]any{
				"type":        "string",
				"description": "The background child agent identifier returned by the agent tool.",
			},
			"wait_ms": map[string]any{
				"type":        "integer",
				"description": "Optional number of milliseconds to wait for the stop request to settle before returning status.",
				"minimum":     0,
			},
		},
		"required": []string{"agent_id"},
	}
}

func (t *AgentStopTool) Permission() PermissionLevel {
	return PermissionExecute
}

func (t *AgentStopTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *AgentStopTool) Validate(input ToolInput) error {
	agentID, ok := stringParam(input.Params, "agent_id")
	if !ok || strings.TrimSpace(agentID) == "" {
		return fmt.Errorf("agent_stop requires agent_id")
	}
	if value, ok := intParam(input.Params, "wait_ms"); ok && value < 0 {
		return fmt.Errorf("wait_ms must be >= 0")
	}
	return nil
}

func (t *AgentStopTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	if t == nil || t.lookup == nil {
		return ToolOutput{}, fmt.Errorf("agent stop lookup is not configured")
	}
	agentID, _ := stringParam(input.Params, "agent_id")
	result, err := t.lookup(ctx, AgentStopRequest{
		AgentID: strings.TrimSpace(agentID),
		WaitMs:  intOrDefault(input.Params, "wait_ms", 0),
	})
	if err != nil {
		return ToolOutput{}, err
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return ToolOutput{}, fmt.Errorf("marshal agent stop result: %w", err)
	}
	return ToolOutput{Output: string(encoded)}, nil
}
