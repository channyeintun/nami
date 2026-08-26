package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	toolpkg "github.com/channyeintun/nami/internal/tools"
	workflowpkg "github.com/channyeintun/nami/internal/workflow"
)

func workflowNode(id string, deps ...string) workflowpkg.NodeSpec {
	return workflowpkg.NodeSpec{
		ID:          id,
		Description: "run " + id,
		Prompt:      "do " + id,
		DependsOn:   deps,
	}
}

func nodeSnapshot(result toolpkg.WorkflowRunResult, id string) toolpkg.WorkflowNodeSnapshot {
	for _, node := range result.Nodes {
		if node.ID == id {
			return node
		}
	}
	return toolpkg.WorkflowNodeSnapshot{}
}

func TestLaunchWorkflowRunsEachNodeAsOneChildAgent(t *testing.T) {
	var mu sync.Mutex
	var requests []toolpkg.AgentRunRequest
	runner := func(_ context.Context, req toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		mu.Lock()
		requests = append(requests, req)
		mu.Unlock()
		return toolpkg.AgentRunResult{
			Status:         "completed",
			AgentID:        "agent-" + req.Description,
			SessionID:      "session-" + req.Description,
			TranscriptPath: "/transcripts/" + req.Description,
			Summary:        "summary of " + req.Description,
		}, nil
	}

	result, err := launchWorkflow(t.Context(), runner, nil, t.TempDir(), toolpkg.WorkflowLaunchRequest{
		Spec: workflowpkg.Spec{
			Description: "two step",
			Nodes:       []workflowpkg.NodeSpec{workflowNode("build"), workflowNode("test", "build")},
		},
	})
	if err != nil {
		t.Fatalf("launchWorkflow: %v", err)
	}
	if result.Status != "completed" || result.Succeeded != 2 || result.NodeCount != 2 {
		t.Fatalf("result = %+v", result)
	}
	if len(requests) != 2 {
		t.Fatalf("dispatched %d agents, want 2", len(requests))
	}
	// Nodes must never run in background mode: the graph owns scheduling, and
	// background dispatch would schedule the same work a second time.
	for _, req := range requests {
		if req.Background {
			t.Fatalf("node %q dispatched in background mode", req.Description)
		}
	}
	build := nodeSnapshot(result, "build")
	if build.AgentID != "agent-run build" || build.TranscriptPath != "/transcripts/run build" {
		t.Fatalf("build snapshot = %+v", build)
	}
	if build.Output != "summary of run build" {
		t.Fatalf("build output = %q", build.Output)
	}
}

func TestLaunchWorkflowInterpolatesUpstreamOutput(t *testing.T) {
	var mu sync.Mutex
	prompts := map[string]string{}
	runner := func(_ context.Context, req toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		mu.Lock()
		prompts[req.Description] = req.Prompt
		mu.Unlock()
		return toolpkg.AgentRunResult{Status: "completed", Summary: "FINDINGS"}, nil
	}

	if _, err := launchWorkflow(t.Context(), runner, nil, t.TempDir(), toolpkg.WorkflowLaunchRequest{
		Spec: workflowpkg.Spec{
			Description: "scan then fix",
			Nodes: []workflowpkg.NodeSpec{
				workflowNode("scan"),
				{ID: "fix", Description: "fix", Prompt: "address: ${outputs.scan}", DependsOn: []string{"scan"}},
			},
		},
	}); err != nil {
		t.Fatalf("launchWorkflow: %v", err)
	}
	if got := prompts["fix"]; got != "address: FINDINGS" {
		t.Fatalf("fix prompt = %q", got)
	}
}

// A child agent that reports "failed" without returning an error still has to
// fail its node, or the graph would feed an error message downstream as if it
// were a result.
func TestLaunchWorkflowTreatsAFailedChildStatusAsNodeFailure(t *testing.T) {
	runner := func(_ context.Context, req toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		if strings.Contains(req.Description, "broken") {
			return toolpkg.AgentRunResult{Status: "failed", Error: "compile error"}, nil
		}
		return toolpkg.AgentRunResult{Status: "completed", Summary: "ok"}, nil
	}

	result, err := launchWorkflow(t.Context(), runner, nil, t.TempDir(), toolpkg.WorkflowLaunchRequest{
		Spec: workflowpkg.Spec{
			Description: "one broken branch",
			Nodes: []workflowpkg.NodeSpec{
				workflowNode("broken"),
				workflowNode("downstream", "broken"),
				workflowNode("unrelated"),
			},
		},
	})
	if err != nil {
		t.Fatalf("launchWorkflow: %v", err)
	}
	if got := nodeSnapshot(result, "broken"); got.Status != string(workflowpkg.StatusFailed) || !strings.Contains(got.Error, "compile error") {
		t.Fatalf("broken = %+v", got)
	}
	if got := nodeSnapshot(result, "downstream").Status; got != string(workflowpkg.StatusSkipped) {
		t.Fatalf("downstream status = %q", got)
	}
	if got := nodeSnapshot(result, "unrelated").Status; got != string(workflowpkg.StatusSucceeded) {
		t.Fatalf("unrelated status = %q", got)
	}
	if result.Status != "failed" || result.Failed != 1 || result.Skipped != 1 || result.Succeeded != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestLaunchWorkflowRejectsAnInvalidGraphWithoutRunningAnything(t *testing.T) {
	called := false
	runner := func(context.Context, toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		called = true
		return toolpkg.AgentRunResult{}, nil
	}
	_, err := launchWorkflow(t.Context(), runner, nil, t.TempDir(), toolpkg.WorkflowLaunchRequest{
		Spec: workflowpkg.Spec{Nodes: []workflowpkg.NodeSpec{workflowNode("a", "b")}},
	})
	if _, ok := errors.AsType[*workflowpkg.ValidationError](err); !ok {
		t.Fatalf("err = %v, want a ValidationError", err)
	}
	if called {
		t.Fatal("dispatched an agent for an invalid graph")
	}
}

func TestLaunchWorkflowRequiresARunner(t *testing.T) {
	_, err := launchWorkflow(t.Context(), nil, nil, t.TempDir(), toolpkg.WorkflowLaunchRequest{
		Spec: workflowpkg.Spec{Nodes: []workflowpkg.NodeSpec{workflowNode("a")}},
	})
	if err == nil {
		t.Fatal("expected an error without a runner")
	}
}

func TestWorkflowStatusIsQueryableAfterTheRun(t *testing.T) {
	runner := func(_ context.Context, req toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		return toolpkg.AgentRunResult{Status: "completed", Summary: "done"}, nil
	}
	result, err := launchWorkflow(t.Context(), runner, nil, t.TempDir(), toolpkg.WorkflowLaunchRequest{
		Spec: workflowpkg.Spec{Description: "d", Nodes: []workflowpkg.NodeSpec{workflowNode("a")}},
	})
	if err != nil {
		t.Fatalf("launchWorkflow: %v", err)
	}

	status, err := lookupWorkflowStatus(t.Context(), toolpkg.WorkflowStatusRequest{RunID: result.RunID})
	if err != nil {
		t.Fatalf("lookupWorkflowStatus: %v", err)
	}
	if status.RunID != result.RunID || status.Status != "completed" || status.Succeeded != 1 {
		t.Fatalf("status = %+v", status)
	}
	if _, err := lookupWorkflowStatus(t.Context(), toolpkg.WorkflowStatusRequest{RunID: "wf_nope"}); err == nil {
		t.Fatal("expected an error for an unknown run")
	}
}

// A finished run must return immediately even when the caller asked to wait,
// rather than sitting out the whole timeout.
func TestWorkflowStatusWaitReturnsImmediatelyForAFinishedRun(t *testing.T) {
	runner := func(context.Context, toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		return toolpkg.AgentRunResult{Status: "completed", Summary: "done"}, nil
	}
	result, err := launchWorkflow(t.Context(), runner, nil, t.TempDir(), toolpkg.WorkflowLaunchRequest{
		Spec: workflowpkg.Spec{Nodes: []workflowpkg.NodeSpec{workflowNode("a")}},
	})
	if err != nil {
		t.Fatalf("launchWorkflow: %v", err)
	}

	started := time.Now()
	if _, err := lookupWorkflowStatus(t.Context(), toolpkg.WorkflowStatusRequest{RunID: result.RunID, WaitMs: 5000}); err != nil {
		t.Fatalf("lookupWorkflowStatus: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("waited %v for an already finished run", elapsed)
	}
}

func TestWorkflowStatusWaitWakesOnNodeProgress(t *testing.T) {
	// The run registry is process-global, so a repeated run of this test would
	// otherwise match the previous iteration's finished run.
	description := "wait-wakes-on-progress-" + newWorkflowRunID()
	release := make(chan struct{})
	firstRunning := make(chan struct{})
	runner := func(ctx context.Context, req toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		if strings.Contains(req.Description, "held") {
			close(firstRunning)
			select {
			case <-release:
			case <-ctx.Done():
				return toolpkg.AgentRunResult{}, ctx.Err()
			}
		}
		return toolpkg.AgentRunResult{Status: "completed", Summary: "done"}, nil
	}

	type launchOutcome struct {
		result toolpkg.WorkflowRunResult
		err    error
	}
	outcomes := make(chan launchOutcome, 1)
	go func() {
		result, err := launchWorkflow(context.Background(), runner, nil, t.TempDir(), toolpkg.WorkflowLaunchRequest{
			Spec: workflowpkg.Spec{
				MaxParallel: 1,
				Description: description,
				Nodes:       []workflowpkg.NodeSpec{workflowNode("held"), workflowNode("after", "held")},
			},
		})
		outcomes <- launchOutcome{result: result, err: err}
	}()

	<-firstRunning
	run := findWorkflowRunByDescription(description)
	if run == nil {
		close(release)
		<-outcomes
		t.Fatal("run was never registered")
	}

	// The held node has started but not finished, so the run must report as
	// running rather than waiting out the full timeout or claiming completion.
	status, err := lookupWorkflowStatus(t.Context(), toolpkg.WorkflowStatusRequest{RunID: run.id, WaitMs: 50})
	if err != nil {
		t.Fatalf("lookupWorkflowStatus: %v", err)
	}
	if status.Status != "running" {
		t.Fatalf("status = %q, want running", status.Status)
	}

	close(release)
	outcome := <-outcomes
	if outcome.err != nil {
		t.Fatalf("launchWorkflow: %v", outcome.err)
	}
	final, err := lookupWorkflowStatus(t.Context(), toolpkg.WorkflowStatusRequest{RunID: outcome.result.RunID})
	if err != nil {
		t.Fatalf("lookupWorkflowStatus: %v", err)
	}
	if final.Status != "completed" || final.Succeeded != 2 {
		t.Fatalf("final = %+v", final)
	}
}

// findWorkflowRunByDescription looks up an in-flight run by its description,
// which is the only handle a test has while launchWorkflow has not returned.
func findWorkflowRunByDescription(description string) *workflowRun {
	workflowRunsMu.RLock()
	defer workflowRunsMu.RUnlock()
	for _, run := range workflowRuns {
		if run.description == description {
			return run
		}
	}
	return nil
}

func TestLaunchWorkflowReportsProgressForEveryNode(t *testing.T) {
	runner := func(context.Context, toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		return toolpkg.AgentRunResult{Status: "completed", Summary: "done"}, nil
	}
	result, err := launchWorkflow(t.Context(), runner, nil, t.TempDir(), toolpkg.WorkflowLaunchRequest{
		Spec: workflowpkg.Spec{Nodes: []workflowpkg.NodeSpec{workflowNode("a"), workflowNode("b", "a")}},
	})
	if err != nil {
		t.Fatalf("launchWorkflow: %v", err)
	}
	ids := make([]string, 0, len(result.Nodes))
	for _, node := range result.Nodes {
		ids = append(ids, node.ID)
		if node.DurationMillis < 0 {
			t.Fatalf("node %q duration = %d", node.ID, node.DurationMillis)
		}
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"a", "b"}) {
		t.Fatalf("nodes = %v", ids)
	}
}

func TestLaunchWorkflowResumeReplaysCachedNodes(t *testing.T) {
	sessionDir := t.TempDir()
	var executed []string
	runner := func(_ context.Context, req toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		executed = append(executed, req.Description)
		return toolpkg.AgentRunResult{Status: "completed", Summary: "out:" + req.Description}, nil
	}

	spec := workflowpkg.Spec{
		MaxParallel: 1,
		Description: "resumable",
		Nodes:       []workflowpkg.NodeSpec{workflowNode("a"), workflowNode("b", "a")},
	}
	first, err := launchWorkflow(t.Context(), runner, nil, sessionDir, toolpkg.WorkflowLaunchRequest{Spec: spec})
	if err != nil {
		t.Fatalf("launchWorkflow: %v", err)
	}
	if len(executed) != 2 {
		t.Fatalf("first run executed %v", executed)
	}

	executed = nil
	second, err := launchWorkflow(t.Context(), runner, nil, sessionDir, toolpkg.WorkflowLaunchRequest{
		Spec:            spec,
		ResumeFromRunID: first.RunID,
	})
	if err != nil {
		t.Fatalf("launchWorkflow: %v", err)
	}
	if len(executed) != 0 {
		t.Fatalf("resume re-executed %v, want nothing", executed)
	}
	if second.Cached != 2 || second.Succeeded != 2 {
		t.Fatalf("second = %+v", second)
	}
	if second.ResumedFrom != first.RunID {
		t.Fatalf("ResumedFrom = %q, want %q", second.ResumedFrom, first.RunID)
	}
	if got := nodeSnapshot(second, "a").Output; got != "out:run a" {
		t.Fatalf("cached output = %q", got)
	}
}

func TestJournalPathIsScopedToTheSessionDir(t *testing.T) {
	if got := journalPathForRun("", "wf_1"); got != "" {
		t.Fatalf("journalPathForRun with no session dir = %q", got)
	}
	got := journalPathForRun("/sessions/abc", "wf_1")
	if !strings.HasPrefix(got, "/sessions/abc") || !strings.HasSuffix(got, "wf_1.ndjson") {
		t.Fatalf("journalPathForRun = %q", got)
	}
}

func TestWorkflowRunIDsAreUnique(t *testing.T) {
	seen := map[string]struct{}{}
	for range 100 {
		id := newWorkflowRunID()
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate run id %q", id)
		}
		seen[id] = struct{}{}
		if !strings.HasPrefix(id, "wf_") {
			t.Fatalf("run id %q lacks the wf_ prefix", id)
		}
	}
	_ = fmt.Sprint(len(seen))
}

// A run that finishes while a status call is waiting must wake it, not leave it
// sitting out the full timeout.
func TestWorkflowStatusWaitWakesWhenTheRunFinishes(t *testing.T) {
	description := "wait-wakes-on-finish-" + newWorkflowRunID()
	release := make(chan struct{})
	running := make(chan struct{})
	runner := func(ctx context.Context, req toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		close(running)
		select {
		case <-release:
		case <-ctx.Done():
			return toolpkg.AgentRunResult{}, ctx.Err()
		}
		return toolpkg.AgentRunResult{Status: "completed", Summary: "done"}, nil
	}

	done := make(chan toolpkg.WorkflowRunResult, 1)
	go func() {
		result, err := launchWorkflow(context.Background(), runner, nil, t.TempDir(), toolpkg.WorkflowLaunchRequest{
			Spec: workflowpkg.Spec{Description: description, Nodes: []workflowpkg.NodeSpec{workflowNode("only")}},
		})
		if err == nil {
			done <- result
		}
		close(done)
	}()

	<-running
	run := findWorkflowRunByDescription(description)
	if run == nil {
		close(release)
		t.Fatal("run was never registered")
	}

	// Let the run finish while the status call is parked on a long wait.
	go func() {
		time.Sleep(20 * time.Millisecond)
		close(release)
	}()

	started := time.Now()
	status, err := lookupWorkflowStatus(t.Context(), toolpkg.WorkflowStatusRequest{RunID: run.id, WaitMs: 10_000})
	if err != nil {
		t.Fatalf("lookupWorkflowStatus: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("waited %v; the finish did not wake the status call", elapsed)
	}
	if status.Status != "completed" {
		t.Fatalf("status = %q, want completed", status.Status)
	}
	<-done
}
