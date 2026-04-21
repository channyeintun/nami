package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	maxAgentDisplaySummaryRunes       = 4000
	maxAgentDisplayErrorRunes         = 2000
	maxAgentDisplayStatusMessageRunes = 2000
	agentDisplayTruncationNote        = "\n\n[truncated for live transcript; full child-agent result is kept on disk]"
)

const subagentTypeExplore = "Explore"
const subagentTypeGeneralPurpose = "general-purpose"
const subagentTypeVerification = "verification"

type AgentRunRequest struct {
	Description       string
	Prompt            string
	Role              string
	WorkspaceStrategy string
	SubagentType      string
	Background        bool
}

type ChildAgentMetadata struct {
	InvocationID      string   `json:"invocation_id,omitempty"`
	AgentID           string   `json:"agent_id,omitempty"`
	Description       string   `json:"description,omitempty"`
	Role              string   `json:"role,omitempty"`
	SubagentType      string   `json:"subagent_type,omitempty"`
	WorkspaceStrategy string   `json:"workspace_strategy,omitempty"`
	WorkspacePath     string   `json:"workspace_path,omitempty"`
	RepositoryRoot    string   `json:"repository_root,omitempty"`
	WorktreeBranch    string   `json:"worktree_branch,omitempty"`
	WorktreeCreated   bool     `json:"worktree_created,omitempty"`
	LifecycleState    string   `json:"lifecycle_state,omitempty"`
	StatusMessage     string   `json:"status_message,omitempty"`
	StopBlockReason   string   `json:"stop_block_reason,omitempty"`
	StopBlockCount    int      `json:"stop_block_count,omitempty"`
	SessionID         string   `json:"session_id,omitempty"`
	TranscriptPath    string   `json:"transcript_path,omitempty"`
	ResultPath        string   `json:"result_path,omitempty"`
	Tools             []string `json:"tools,omitempty"`
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

func DisplaySafeAgentResult(result AgentRunResult) AgentRunResult {
	display := result
	display.Summary = truncateAgentDisplayText(display.Summary, maxAgentDisplaySummaryRunes)
	display.Error = truncateAgentDisplayText(display.Error, maxAgentDisplayErrorRunes)
	if display.Metadata != nil {
		metadata := *display.Metadata
		metadata.Tools = append([]string(nil), display.Metadata.Tools...)
		metadata.StatusMessage = truncateAgentDisplayText(
			metadata.StatusMessage,
			maxAgentDisplayStatusMessageRunes,
		)
		display.Metadata = &metadata
	}
	return display
}

func marshalDisplayAgentResult(result AgentRunResult) (string, error) {
	encoded, err := json.MarshalIndent(DisplaySafeAgentResult(result), "", "  ")
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func truncateAgentDisplayText(value string, maxRunes int) string {
	normalized := strings.TrimSpace(value)
	if normalized == "" || maxRunes <= 0 {
		return normalized
	}
	runes := []rune(normalized)
	if len(runes) <= maxRunes {
		return normalized
	}
	note := []rune(agentDisplayTruncationNote)
	if len(note) >= maxRunes {
		return string(runes[:maxRunes])
	}
	keep := maxRunes - len(note)
	if keep < 0 {
		keep = 0
	}
	return string(runes[:keep]) + agentDisplayTruncationNote
}

func NewAgentTool(runner AgentRunner) *AgentTool {
	return &AgentTool{runner: runner}
}

func (t *AgentTool) Name() string {
	return "agent"
}

func (t *AgentTool) Description() string {
	return "Spawn a bounded child agent in a fresh context. Use Explore for read-only codebase search, general-purpose for broader delegated work, and verification for build/test validation without file edits."
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
				"description": "The child agent type: Explore for read-only codebase search, general-purpose for broader delegated work, and verification for build/test validation without file edits.",
				"enum":        []string{subagentTypeExplore, subagentTypeGeneralPurpose, subagentTypeVerification},
			},
			"role": map[string]any{
				"type":        "string",
				"description": "Optional project-local swarm role name. When set, Nami loads matching files from .nami/swarm to refine the child agent prompt.",
			},
			"workspace_strategy": map[string]any{
				"type":        "string",
				"description": "Optional workspace mode override for this child agent.",
				"enum":        []string{"shared", "worktree"},
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
	if subagentType, ok := stringParam(input.Params, "subagent_type"); ok && strings.TrimSpace(subagentType) != "" && !IsSupportedSubagentType(subagentType) {
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
	role, _ := stringParam(input.Params, "role")
	workspaceStrategy, _ := stringParam(input.Params, "workspace_strategy")
	subagentType, _ := stringParam(input.Params, "subagent_type")
	subagentType = NormalizeSubagentType(subagentType)

	result, err := t.runner(ctx, AgentRunRequest{
		Description:       strings.TrimSpace(description),
		Prompt:            strings.TrimSpace(prompt),
		Role:              strings.TrimSpace(role),
		WorkspaceStrategy: strings.TrimSpace(workspaceStrategy),
		SubagentType:      subagentType,
		Background:        boolOrDefault(input.Params, "run_in_background", false),
	})
	if err != nil {
		return ToolOutput{}, err
	}

	encoded, err := marshalDisplayAgentResult(result)
	if err != nil {
		return ToolOutput{}, fmt.Errorf("marshal agent result: %w", err)
	}
	return ToolOutput{Output: encoded}, nil
}

func NormalizeSubagentType(subagentType string) string {
	switch strings.ToLower(strings.TrimSpace(subagentType)) {
	case "", "explore":
		return subagentTypeExplore
	case "general-purpose":
		return subagentTypeGeneralPurpose
	case "verification":
		return subagentTypeVerification
	default:
		return strings.TrimSpace(subagentType)
	}
}

// IsSupportedSubagentType reports whether subagentType is a recognized subagent mode.
func IsSupportedSubagentType(subagentType string) bool {
	switch NormalizeSubagentType(subagentType) {
	case subagentTypeExplore, subagentTypeGeneralPurpose, subagentTypeVerification:
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
	encoded, err := marshalDisplayAgentResult(result)
	if err != nil {
		return ToolOutput{}, fmt.Errorf("marshal agent status: %w", err)
	}
	return ToolOutput{Output: encoded}, nil
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
	encoded, err := marshalDisplayAgentResult(result)
	if err != nil {
		return ToolOutput{}, fmt.Errorf("marshal agent stop result: %w", err)
	}
	return ToolOutput{Output: encoded}, nil
}
