package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	workflowpkg "github.com/channyeintun/nami/internal/workflow"
)

func workflowParams(nodes []any, extra map[string]any) map[string]any {
	params := map[string]any{"description": "ship it", "nodes": nodes}
	for key, value := range extra {
		params[key] = value
	}
	return params
}

func nodeParams(id string, deps ...string) map[string]any {
	node := map[string]any{"id": id, "description": "run " + id, "prompt": "do " + id}
	if len(deps) > 0 {
		raw := make([]any, 0, len(deps))
		for _, dep := range deps {
			raw = append(raw, dep)
		}
		node["depends_on"] = raw
	}
	return node
}

func TestWorkflowToolValidateAcceptsAWellFormedGraph(t *testing.T) {
	tool := NewWorkflowTool(func(context.Context, WorkflowLaunchRequest) (WorkflowRunResult, error) {
		return WorkflowRunResult{}, nil
	})
	input := ToolInput{Params: workflowParams([]any{nodeParams("build"), nodeParams("test", "build")}, nil)}
	if err := tool.Validate(input); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// A malformed graph has to fail validation, not launch and then fail: the model
// gets an actionable message instead of a wasted run.
func TestWorkflowToolValidateRejectsCycleBeforeLaunch(t *testing.T) {
	launched := false
	tool := NewWorkflowTool(func(context.Context, WorkflowLaunchRequest) (WorkflowRunResult, error) {
		launched = true
		return WorkflowRunResult{}, nil
	})
	input := ToolInput{Params: workflowParams([]any{nodeParams("a", "b"), nodeParams("b", "a")}, nil)}
	err := tool.Validate(input)
	if err == nil {
		t.Fatal("expected a validation error for a cyclic graph")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error = %q, want a cycle message", err)
	}
	if launched {
		t.Fatal("launcher ran despite failed validation")
	}
}

func TestWorkflowToolValidateRejectsUnsupportedSubagentType(t *testing.T) {
	tool := NewWorkflowTool(func(context.Context, WorkflowLaunchRequest) (WorkflowRunResult, error) {
		return WorkflowRunResult{}, nil
	})
	node := nodeParams("a")
	node["agent"] = map[string]any{"subagent_type": "wizard"}
	err := tool.Validate(ToolInput{Params: workflowParams([]any{node}, nil)})
	if err == nil || !strings.Contains(err.Error(), "subagent_type") {
		t.Fatalf("err = %v, want a subagent_type message", err)
	}
}

func TestWorkflowToolValidateRejectsUnknownWorkspaceStrategy(t *testing.T) {
	tool := NewWorkflowTool(func(context.Context, WorkflowLaunchRequest) (WorkflowRunResult, error) {
		return WorkflowRunResult{}, nil
	})
	node := nodeParams("a")
	node["agent"] = map[string]any{"workspace_strategy": "sandbox"}
	err := tool.Validate(ToolInput{Params: workflowParams([]any{node}, nil)})
	if err == nil || !strings.Contains(err.Error(), "workspace_strategy") {
		t.Fatalf("err = %v, want a workspace_strategy message", err)
	}
}

func TestWorkflowToolValidateRequiresAConfiguredLauncher(t *testing.T) {
	tool := NewWorkflowTool(nil)
	if err := tool.Validate(ToolInput{Params: workflowParams([]any{nodeParams("a")}, nil)}); err == nil {
		t.Fatal("expected an error for a missing launcher")
	}
}

func TestWorkflowToolPassesTheParsedSpecThrough(t *testing.T) {
	var received WorkflowLaunchRequest
	tool := NewWorkflowTool(func(_ context.Context, req WorkflowLaunchRequest) (WorkflowRunResult, error) {
		received = req
		return WorkflowRunResult{Status: "completed", RunID: "wf_1", NodeCount: 2}, nil
	})

	node := nodeParams("test", "build")
	node["agent"] = map[string]any{"subagent_type": "verification", "workspace_strategy": "worktree"}
	input := ToolInput{Params: workflowParams(
		[]any{nodeParams("build"), node},
		map[string]any{"max_parallel": 3, "on_node_failure": "abort", "resume_from_run_id": "wf_0"},
	)}

	output, err := tool.Execute(t.Context(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if received.ResumeFromRunID != "wf_0" {
		t.Fatalf("ResumeFromRunID = %q", received.ResumeFromRunID)
	}
	if received.Spec.MaxParallel != 3 || received.Spec.OnNodeFailure != "abort" {
		t.Fatalf("spec = %+v", received.Spec)
	}
	if len(received.Spec.Nodes) != 2 {
		t.Fatalf("nodes = %d", len(received.Spec.Nodes))
	}
	testNode := received.Spec.Nodes[1]
	if testNode.ID != "test" || len(testNode.DependsOn) != 1 || testNode.DependsOn[0] != "build" {
		t.Fatalf("test node = %+v", testNode)
	}
	if testNode.Agent.SubagentType != subagentTypeVerification || testNode.Agent.WorkspaceStrategy != "worktree" {
		t.Fatalf("agent = %+v", testNode.Agent)
	}

	var decoded WorkflowRunResult
	if err := json.Unmarshal([]byte(output.Output), &decoded); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if decoded.RunID != "wf_1" || decoded.Status != "completed" {
		t.Fatalf("decoded = %+v", decoded)
	}
}

// An omitted subagent_type must stay empty so the engine picks its own default.
// Normalizing an absent value would silently pin every node to Explore.
func TestWorkflowToolLeavesOmittedSubagentTypeEmpty(t *testing.T) {
	var received WorkflowLaunchRequest
	tool := NewWorkflowTool(func(_ context.Context, req WorkflowLaunchRequest) (WorkflowRunResult, error) {
		received = req
		return WorkflowRunResult{}, nil
	})
	if _, err := tool.Execute(t.Context(), ToolInput{Params: workflowParams([]any{nodeParams("a")}, nil)}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := received.Spec.Nodes[0].Agent.SubagentType; got != "" {
		t.Fatalf("SubagentType = %q, want empty", got)
	}
}

func TestWorkflowToolTruncatesNodeOutputForTheTranscript(t *testing.T) {
	long := strings.Repeat("x", maxAgentDisplaySummaryRunes+500)
	tool := NewWorkflowTool(func(context.Context, WorkflowLaunchRequest) (WorkflowRunResult, error) {
		return WorkflowRunResult{
			Status: "completed",
			Nodes:  []WorkflowNodeSnapshot{{ID: "a", Status: "succeeded", Output: long}},
		}, nil
	})
	output, err := tool.Execute(t.Context(), ToolInput{Params: workflowParams([]any{nodeParams("a")}, nil)})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var decoded WorkflowRunResult
	if err := json.Unmarshal([]byte(output.Output), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len([]rune(decoded.Nodes[0].Output)) >= len([]rune(long)) {
		t.Fatal("node output was not truncated")
	}
}

func TestWorkflowStatusToolValidates(t *testing.T) {
	tool := NewWorkflowStatusTool(func(context.Context, WorkflowStatusRequest) (WorkflowRunResult, error) {
		return WorkflowRunResult{}, nil
	})
	if err := tool.Validate(ToolInput{Params: map[string]any{}}); err == nil {
		t.Fatal("expected an error for a missing run_id")
	}
	if err := tool.Validate(ToolInput{Params: map[string]any{"run_id": "wf_1", "wait_ms": -1}}); err == nil {
		t.Fatal("expected an error for a negative wait_ms")
	}
	if err := tool.Validate(ToolInput{Params: map[string]any{"run_id": "wf_1", "wait_ms": 250}}); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestWorkflowStatusToolForwardsTheRequest(t *testing.T) {
	var received WorkflowStatusRequest
	tool := NewWorkflowStatusTool(func(_ context.Context, req WorkflowStatusRequest) (WorkflowRunResult, error) {
		received = req
		return WorkflowRunResult{Status: "running", RunID: req.RunID}, nil
	})
	if _, err := tool.Execute(t.Context(), ToolInput{Params: map[string]any{"run_id": " wf_9 ", "wait_ms": 100}}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if received.RunID != "wf_9" || received.WaitMs != 100 {
		t.Fatalf("received = %+v", received)
	}
}

func TestWorkflowToolSchemaMatchesTheEngineLimits(t *testing.T) {
	schema, ok := NewWorkflowTool(nil).InputSchema().(map[string]any)
	if !ok {
		t.Fatal("schema is not an object")
	}
	properties := schema["properties"].(map[string]any)
	nodes := properties["nodes"].(map[string]any)
	if nodes["maxItems"] != workflowpkg.MaxNodes {
		t.Fatalf("maxItems = %v, want %d", nodes["maxItems"], workflowpkg.MaxNodes)
	}
	policies := properties["on_node_failure"].(map[string]any)["enum"].([]string)
	if len(policies) != 2 || policies[0] != string(workflowpkg.FailureSkipDependents) {
		t.Fatalf("on_node_failure enum = %v", policies)
	}
}
