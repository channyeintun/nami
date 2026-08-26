package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	workflowpkg "github.com/channyeintun/nami/internal/workflow"
)

// WorkflowLaunchRequest asks the engine to run a workflow graph.
type WorkflowLaunchRequest struct {
	Spec            workflowpkg.Spec
	ResumeFromRunID string
}

// WorkflowRunResult is the engine's view of a run, launched or finished.
type WorkflowRunResult struct {
	Status         string                 `json:"status"`
	RunID          string                 `json:"run_id"`
	Description    string                 `json:"description,omitempty"`
	JournalPath    string                 `json:"journal_path,omitempty"`
	ResumedFrom    string                 `json:"resumed_from,omitempty"`
	NodeCount      int                    `json:"node_count"`
	Succeeded      int                    `json:"succeeded"`
	Failed         int                    `json:"failed"`
	Skipped        int                    `json:"skipped"`
	Cached         int                    `json:"cached"`
	DurationMillis int64                  `json:"duration_ms,omitempty"`
	Nodes          []WorkflowNodeSnapshot `json:"nodes,omitempty"`
	Error          string                 `json:"error,omitempty"`
}

// WorkflowNodeSnapshot is one node's outcome, trimmed for the transcript.
type WorkflowNodeSnapshot struct {
	ID             string   `json:"id"`
	Description    string   `json:"description,omitempty"`
	Status         string   `json:"status"`
	DependsOn      []string `json:"depends_on,omitempty"`
	Output         string   `json:"output,omitempty"`
	Error          string   `json:"error,omitempty"`
	AgentID        string   `json:"agent_id,omitempty"`
	SessionID      string   `json:"session_id,omitempty"`
	TranscriptPath string   `json:"transcript_path,omitempty"`
	DurationMillis int64    `json:"duration_ms,omitempty"`
}

// WorkflowStatusRequest asks for a run's current state.
type WorkflowStatusRequest struct {
	RunID  string
	WaitMs int
}

// WorkflowLauncher starts a run. It returns once the run finishes; the engine
// owns the run's own concurrency.
type WorkflowLauncher func(context.Context, WorkflowLaunchRequest) (WorkflowRunResult, error)

// WorkflowStatusLookup reports on a run by id.
type WorkflowStatusLookup func(context.Context, WorkflowStatusRequest) (WorkflowRunResult, error)

// WorkflowTool runs a dependency graph of child agents.
type WorkflowTool struct {
	launcher WorkflowLauncher
}

// WorkflowStatusTool reports on a workflow run.
type WorkflowStatusTool struct {
	lookup WorkflowStatusLookup
}

func NewWorkflowTool(launcher WorkflowLauncher) *WorkflowTool {
	return &WorkflowTool{launcher: launcher}
}

func NewWorkflowStatusTool(lookup WorkflowStatusLookup) *WorkflowStatusTool {
	return &WorkflowStatusTool{lookup: lookup}
}

func (t *WorkflowTool) Name() string { return "workflow" }

func (t *WorkflowTool) Description() string {
	return strings.TrimSpace(`Run a dependency graph of child agents. Each node is one delegated task; depends_on names the nodes whose results it needs, and a node starts as soon as those finish, so independent branches never wait on each other.

Use this instead of agent_team when the work has structure — some tasks must read what earlier ones produced. Use agent_team when the tasks are genuinely independent and you only need them all done.

Pass a dependency's result into a later node's prompt with ${outputs.<node_id>}; the node must also list that id in depends_on. By default a failed node skips only what depended on it and other branches finish; set on_node_failure to "abort" to stop the whole run instead.

Every run writes a journal. Passing its run_id back as resume_from_run_id replays the unchanged prefix and re-executes from the first node whose prompt or settings changed.`)
}

func (t *WorkflowTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"description": map[string]any{
				"type":        "string",
				"description": "Short summary of what the whole workflow is for.",
			},
			"nodes": map[string]any{
				"type":        "array",
				"minItems":    1,
				"maxItems":    workflowpkg.MaxNodes,
				"description": "The graph's nodes. Order does not matter; depends_on defines execution order.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id": map[string]any{
							"type":        "string",
							"pattern":     "^[a-z0-9][a-z0-9_-]*$",
							"description": "Unique lowercase identifier other nodes reference.",
						},
						"description": map[string]any{"type": "string", "description": "Short label shown in progress output."},
						"prompt": map[string]any{
							"type":        "string",
							"description": "The delegated task. May interpolate a dependency's result with ${outputs.<node_id>}.",
						},
						"depends_on": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Ids of nodes that must finish before this one starts.",
						},
						"agent": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"subagent_type":      map[string]any{"type": "string", "enum": []string{subagentTypeExplore, subagentTypeGeneralPurpose, subagentTypeVerification}},
								"role":               map[string]any{"type": "string"},
								"workspace_strategy": map[string]any{"type": "string", "enum": []string{"shared", "worktree"}},
							},
						},
					},
					"required": []string{"id", "description", "prompt"},
				},
			},
			"max_parallel": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "Cap on nodes running at once. Defaults to a value derived from the machine.",
			},
			"on_node_failure": map[string]any{
				"type":        "string",
				"enum":        []string{string(workflowpkg.FailureSkipDependents), string(workflowpkg.FailureAbort)},
				"description": "skip_dependents (default) lets independent branches finish; abort stops the run.",
			},
			"resume_from_run_id": map[string]any{
				"type":        "string",
				"description": "Replay a previous run's unchanged prefix instead of re-running it from scratch.",
			},
		},
		"required": []string{"description", "nodes"},
	}
}

func (t *WorkflowTool) Permission() PermissionLevel                     { return PermissionExecute }
func (t *WorkflowTool) Concurrency(input ToolInput) ConcurrencyDecision { return ConcurrencySerial }

func (t *WorkflowTool) Validate(input ToolInput) error {
	if t == nil || t.launcher == nil {
		return fmt.Errorf("workflow launcher is not configured")
	}
	spec, err := workflowSpecFromParams(input.Params)
	if err != nil {
		return err
	}
	// Resolving here rather than at launch turns a malformed graph into a
	// validation message the model can act on, instead of a failed run.
	if _, err := spec.Resolve(); err != nil {
		return err
	}
	for _, node := range spec.Nodes {
		if subagentType := strings.TrimSpace(node.Agent.SubagentType); subagentType != "" && !IsSupportedSubagentType(subagentType) {
			return fmt.Errorf("workflow node %q subagent_type %q is not supported", node.ID, subagentType)
		}
		if strategy := strings.ToLower(strings.TrimSpace(node.Agent.WorkspaceStrategy)); strategy != "" && strategy != "shared" && strategy != "worktree" {
			return fmt.Errorf("workflow node %q workspace_strategy %q must be shared or worktree", node.ID, node.Agent.WorkspaceStrategy)
		}
	}
	return nil
}

func (t *WorkflowTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	spec, err := workflowSpecFromParams(input.Params)
	if err != nil {
		return ToolOutput{}, err
	}
	result, err := t.launcher(ctx, WorkflowLaunchRequest{
		Spec:            spec,
		ResumeFromRunID: strings.TrimSpace(firstStringOrEmpty(input.Params, "resume_from_run_id")),
	})
	if err != nil {
		return ToolOutput{}, err
	}
	return marshalWorkflowResult(result, "marshal workflow result")
}

func (t *WorkflowStatusTool) Name() string { return "workflow_status" }

func (t *WorkflowStatusTool) Description() string {
	return "Return the current state of a workflow run, including each node's status and result."
}

func (t *WorkflowStatusTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"run_id":  map[string]any{"type": "string", "description": "Run identifier returned by workflow."},
			"wait_ms": map[string]any{"type": "integer", "minimum": 0, "description": "Optional time to wait for node updates before returning."},
		},
		"required": []string{"run_id"},
	}
}

func (t *WorkflowStatusTool) Permission() PermissionLevel { return PermissionReadOnly }
func (t *WorkflowStatusTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *WorkflowStatusTool) Validate(input ToolInput) error {
	if t == nil || t.lookup == nil {
		return fmt.Errorf("workflow status lookup is not configured")
	}
	if runID, ok := stringParam(input.Params, "run_id"); !ok || strings.TrimSpace(runID) == "" {
		return fmt.Errorf("workflow_status requires run_id")
	}
	if value, ok := intParam(input.Params, "wait_ms"); ok && value < 0 {
		return fmt.Errorf("wait_ms must be >= 0")
	}
	return nil
}

func (t *WorkflowStatusTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	runID, _ := stringParam(input.Params, "run_id")
	result, err := t.lookup(ctx, WorkflowStatusRequest{
		RunID:  strings.TrimSpace(runID),
		WaitMs: intOrDefault(input.Params, "wait_ms", 0),
	})
	if err != nil {
		return ToolOutput{}, err
	}
	return marshalWorkflowResult(result, "marshal workflow status")
}

// workflowSpecFromParams rebuilds the typed spec from the model's raw params.
// It deliberately does not round-trip through JSON: the params map is already
// decoded, and re-encoding would turn a malformed field into an opaque unmarshal
// error instead of a message naming the node.
func workflowSpecFromParams(params map[string]any) (workflowpkg.Spec, error) {
	rawNodes, ok := params["nodes"].([]any)
	if !ok || len(rawNodes) == 0 {
		return workflowpkg.Spec{}, fmt.Errorf("workflow requires at least one node")
	}

	spec := workflowpkg.Spec{
		Description:   strings.TrimSpace(firstStringOrEmpty(params, "description")),
		MaxParallel:   intOrDefault(params, "max_parallel", 0),
		OnNodeFailure: strings.TrimSpace(firstStringOrEmpty(params, "on_node_failure")),
		Nodes:         make([]workflowpkg.NodeSpec, 0, len(rawNodes)),
	}
	for index, rawNode := range rawNodes {
		node, ok := rawNode.(map[string]any)
		if !ok {
			return workflowpkg.Spec{}, fmt.Errorf("workflow node %d is invalid", index+1)
		}
		spec.Nodes = append(spec.Nodes, workflowpkg.NodeSpec{
			ID:          strings.TrimSpace(firstStringOrEmpty(node, "id")),
			Description: strings.TrimSpace(firstStringOrEmpty(node, "description")),
			Prompt:      strings.TrimSpace(firstStringOrEmpty(node, "prompt")),
			DependsOn:   stringListParam(node, "depends_on"),
			Agent:       workflowAgentFromParams(node["agent"]),
		})
	}
	return spec, nil
}

func workflowAgentFromParams(raw any) workflowpkg.AgentSpec {
	agent, ok := raw.(map[string]any)
	if !ok {
		return workflowpkg.AgentSpec{}
	}
	spec := workflowpkg.AgentSpec{
		Role:              strings.TrimSpace(firstStringOrEmpty(agent, "role")),
		WorkspaceStrategy: strings.TrimSpace(firstStringOrEmpty(agent, "workspace_strategy")),
	}
	// An omitted subagent_type has to stay empty so the engine applies its own
	// default; normalizing "" here would silently pin every node to Explore.
	if subagentType := strings.TrimSpace(firstStringOrEmpty(agent, "subagent_type")); subagentType != "" {
		spec.SubagentType = NormalizeSubagentType(subagentType)
	}
	return spec
}

func stringListParam(params map[string]any, key string) []string {
	raw, ok := params[key].([]any)
	if !ok {
		return nil
	}
	values := make([]string, 0, len(raw))
	for _, item := range raw {
		if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
			values = append(values, strings.TrimSpace(value))
		}
	}
	return values
}

func marshalWorkflowResult(result WorkflowRunResult, context string) (ToolOutput, error) {
	encoded, err := json.MarshalIndent(displaySafeWorkflowResult(result), "", "  ")
	if err != nil {
		return ToolOutput{}, fmt.Errorf("%s: %w", context, err)
	}
	return ToolOutput{Output: string(encoded)}, nil
}

// displaySafeWorkflowResult caps per-node text so a wide graph cannot flood the
// transcript. The full result of each node stays on disk in its child agent's
// transcript, which the snapshot points at.
func displaySafeWorkflowResult(result WorkflowRunResult) WorkflowRunResult {
	display := result
	display.Nodes = make([]WorkflowNodeSnapshot, 0, len(result.Nodes))
	for _, node := range result.Nodes {
		node.Output = truncateAgentDisplayText(node.Output, maxAgentDisplaySummaryRunes)
		node.Error = truncateAgentDisplayText(node.Error, maxAgentDisplayErrorRunes)
		display.Nodes = append(display.Nodes, node)
	}
	return display
}
