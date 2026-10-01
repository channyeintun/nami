package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/channyeintun/nami/internal/ipc"
	"github.com/channyeintun/nami/internal/session"
	toolpkg "github.com/channyeintun/nami/internal/tools"
	workflowpkg "github.com/channyeintun/nami/internal/workflow"
)

const testWorkflowMeta = "export const meta = { name: 'test-run', description: 'a test run', phases: [{ title: 'Find', detail: 'look around' }] }\n"

func parseTestWorkflow(t *testing.T, body string) workflowpkg.Script {
	t.Helper()
	script, err := workflowpkg.ParseScript("test.js", testWorkflowMeta+body)
	if err != nil {
		t.Fatalf("ParseScript: %v", err)
	}
	return script
}

// fakeChildRunner stands in for the engine's child agents. It records every
// request and answers with respond, which defaults to echoing the prompt.
type fakeChildRunner struct {
	mu       sync.Mutex
	requests []toolpkg.AgentRunRequest
	respond  func(context.Context, toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error)
}

func (f *fakeChildRunner) run(ctx context.Context, req toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	if f.respond != nil {
		return f.respond(ctx, req)
	}
	return toolpkg.AgentRunResult{
		Status:         "completed",
		Summary:        "done: " + req.Prompt,
		AgentID:        "agent-" + req.Prompt,
		TranscriptPath: "/transcripts/" + req.Prompt,
		InputTokens:    10,
		OutputTokens:   5,
		TotalCostUSD:   0.25,
	}, nil
}

func (f *fakeChildRunner) calls() []toolpkg.AgentRunRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]toolpkg.AgentRunRequest(nil), f.requests...)
}

func testWorkflowDeps(runner *fakeChildRunner, sessionDir string) workflowLaunchDeps {
	return workflowLaunchDeps{runner: runner.run, sessionDir: sessionDir, cwd: sessionDir}
}

// launchTestWorkflow starts a run and makes sure it has ended before the test
// does, so no run outlives the temporary directory it writes into.
func launchTestWorkflow(t *testing.T, deps workflowLaunchDeps, req toolpkg.WorkflowLaunchRequest) toolpkg.WorkflowLaunchResult {
	t.Helper()
	launched, err := launchWorkflowRun(deps, req)
	if err != nil {
		t.Fatalf("launchWorkflowRun: %v", err)
	}
	t.Cleanup(func() {
		if run, ok := findWorkflowRun(launched.RunID); ok {
			run.stop()
			<-run.done
		}
	})
	return launched
}

// waitForTestWorkflow waits for a run to finish and returns its final state.
func waitForTestWorkflow(t *testing.T, sessionDir string, runID string) toolpkg.WorkflowRunSnapshot {
	t.Helper()
	snapshot, err := lookupWorkflowRunStatus(t.Context(), sessionDir, toolpkg.WorkflowStatusRequest{RunID: runID, WaitMs: 10_000})
	if err != nil {
		t.Fatalf("lookupWorkflowRunStatus: %v", err)
	}
	if snapshot.Status == toolpkg.WorkflowStatusRunning {
		t.Fatalf("run %s still running after 10s: %+v", runID, snapshot)
	}
	return snapshot
}

// The workflow tool has to hand the turn back while the script runs: a run
// can take hours, and the old graph tool held the whole turn until it ended.
func TestWorkflowLaunchReturnsWhileTheScriptIsStillRunning(t *testing.T) {
	sessionDir := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})
	runner := &fakeChildRunner{respond: func(ctx context.Context, req toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return toolpkg.AgentRunResult{}, ctx.Err()
		}
		return toolpkg.AgentRunResult{Status: "completed", Summary: "late answer"}, nil
	}}
	script := parseTestWorkflow(t, "return await agent('slow work')")

	launched := launchTestWorkflow(t, testWorkflowDeps(runner, sessionDir), toolpkg.WorkflowLaunchRequest{Script: script})
	if launched.Status != "launched" || !strings.HasPrefix(launched.RunID, "wf_") || launched.Name != "test-run" {
		t.Fatalf("launch = %+v", launched)
	}
	if !strings.Contains(launched.Message, "task-notification") || !strings.Contains(launched.Message, "workflow_status") {
		t.Fatalf("message = %q, want it to explain how the run reports back", launched.Message)
	}
	saved, err := os.ReadFile(launched.ScriptPath)
	if err != nil || string(saved) != script.Source {
		t.Fatalf("script copy = %q, %v; want the script as launched", saved, err)
	}

	<-started
	running, err := lookupWorkflowRunStatus(t.Context(), sessionDir, toolpkg.WorkflowStatusRequest{RunID: launched.RunID})
	if err != nil {
		t.Fatalf("lookupWorkflowRunStatus: %v", err)
	}
	// The child has started, but the run may not have recorded that yet, so
	// the agent reads as queued or running; either way it is unfinished.
	if running.Status != toolpkg.WorkflowStatusRunning || running.Counts.Running+running.Counts.Queued != 1 || running.ResultPath != "" {
		t.Fatalf("status while the agent runs = %+v", running)
	}

	close(release)
	final := waitForTestWorkflow(t, sessionDir, launched.RunID)
	if final.Status != toolpkg.WorkflowStatusCompleted || string(final.Result) != `"late answer"` {
		t.Fatalf("final = %+v", final)
	}
}

// workflow_status is how the model reads a run, so it has to carry what the
// script did: each agent's outcome, the counts, logs, phases, and the value
// the script returned. A failed agent does not fail the run; the script
// decides what a failure means.
func TestWorkflowStatusWaitsForTheRunAndReportsAgentsResultAndCounts(t *testing.T) {
	sessionDir := t.TempDir()
	runner := &fakeChildRunner{}
	runner.respond = func(ctx context.Context, req toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		if req.Prompt == "break" {
			return toolpkg.AgentRunResult{Status: "failed", Error: "compile error", AgentID: "agent-broken", TranscriptPath: "/transcripts/broken"}, nil
		}
		return toolpkg.AgentRunResult{Status: "completed", Summary: "done: " + req.Prompt, AgentID: "agent-" + req.Prompt, InputTokens: 10, OutputTokens: 5, TotalCostUSD: 0.25}, nil
	}
	script := parseTestWorkflow(t, `
phase('Find')
const found = await parallel([() => agent('one', { label: 'first' }), () => agent('two'), () => agent('break')])
phase('Verify')
log('found', found.filter(Boolean).length)
return found
`)

	launched := launchTestWorkflow(t, testWorkflowDeps(runner, sessionDir), toolpkg.WorkflowLaunchRequest{Script: script})
	final := waitForTestWorkflow(t, sessionDir, launched.RunID)

	if final.Status != toolpkg.WorkflowStatusCompleted || final.Error != "" {
		t.Fatalf("status = %q (error %q), want completed despite a failed agent", final.Status, final.Error)
	}
	if string(final.Result) != `["done: one","done: two",null]` {
		t.Fatalf("result = %s", final.Result)
	}
	want := toolpkg.WorkflowAgentCounts{Agents: 3, Succeeded: 2, Failed: 1}
	if final.Counts != want {
		t.Fatalf("counts = %+v, want %+v", final.Counts, want)
	}
	if len(final.Agents) != 3 {
		t.Fatalf("agents = %+v", final.Agents)
	}
	first := final.Agents[0]
	if first.Label != "first" || first.Phase != "Find" || first.OutputPreview != "done: one" || first.AgentID != "agent-one" {
		t.Fatalf("first agent = %+v", first)
	}
	broken := final.Agents[2]
	if broken.Status != string(workflowpkg.AgentFailed) || broken.Error != "compile error" || broken.TranscriptPath != "/transcripts/broken" {
		t.Fatalf("failed agent = %+v; it must keep its error and transcript", broken)
	}
	if final.InputTokens != 20 || final.OutputTokens != 10 || final.TotalCostUSD != 0.5 {
		t.Fatalf("usage = %d in, %d out, $%v", final.InputTokens, final.OutputTokens, final.TotalCostUSD)
	}
	if final.CurrentPhase != "Verify" || len(final.Phases) != 2 || final.Phases[0].Detail != "look around" || final.Phases[1].Title != "Verify" {
		t.Fatalf("phases = %+v, current %q", final.Phases, final.CurrentPhase)
	}
	if len(final.Logs) != 1 || final.Logs[0] != "found 2" {
		t.Fatalf("logs = %q", final.Logs)
	}
	if final.ResultPath == "" || final.DurationMs < 0 || final.CompletedAt.IsZero() {
		t.Fatalf("final = %+v, want a result path and a completion time", final)
	}

	// Every child runs synchronously as one workflow step, as a
	// general-purpose agent unless the script says otherwise.
	for _, req := range runner.calls() {
		if req.Background || !req.ForWorkflow || req.SubagentType != generalPurposeSubagentType {
			t.Fatalf("request = %+v", req)
		}
	}
}

// Stopping has to end a run whose agents would otherwise run on, and report
// it as stopped rather than failed: the user asked for it.
func TestWorkflowStopEndsARunningRun(t *testing.T) {
	sessionDir := t.TempDir()
	started := make(chan struct{}, 1)
	runner := &fakeChildRunner{respond: func(ctx context.Context, req toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		started <- struct{}{}
		<-ctx.Done()
		return toolpkg.AgentRunResult{}, ctx.Err()
	}}
	script := parseTestWorkflow(t, "return await agent('forever')")
	launched := launchTestWorkflow(t, testWorkflowDeps(runner, sessionDir), toolpkg.WorkflowLaunchRequest{Script: script})
	<-started

	stopped, err := stopWorkflowRun(t.Context(), sessionDir, toolpkg.WorkflowStopRequest{RunID: launched.RunID, WaitMs: 10_000})
	if err != nil {
		t.Fatalf("stopWorkflowRun: %v", err)
	}
	if stopped.Status != toolpkg.WorkflowStatusStopped || stopped.Counts.Stopped != 1 || stopped.Error != "" {
		t.Fatalf("stopped = %+v", stopped)
	}

	// Stopping a run that has already ended just reports it.
	again, err := stopWorkflowRun(t.Context(), sessionDir, toolpkg.WorkflowStopRequest{RunID: launched.RunID})
	if err != nil || again.Status != toolpkg.WorkflowStatusStopped {
		t.Fatalf("second stop = %+v, %v", again, err)
	}
}

// The registry forgets a run five minutes after it ends, but the model may
// ask about it much later, and its result must still be there.
func TestAFinishedWorkflowIsReadableFromItsResultFileAfterEviction(t *testing.T) {
	sessionDir := t.TempDir()
	runner := &fakeChildRunner{}
	script := parseTestWorkflow(t, "return { answer: await agent('question') }")
	launched := launchTestWorkflow(t, testWorkflowDeps(runner, sessionDir), toolpkg.WorkflowLaunchRequest{Script: script})
	final := waitForTestWorkflow(t, sessionDir, launched.RunID)

	run, ok := findWorkflowRun(launched.RunID)
	if !ok {
		t.Fatal("a just-finished run left the registry early")
	}
	evictWorkflowRun(run)
	if _, ok := findWorkflowRun(launched.RunID); ok {
		t.Fatal("evictWorkflowRun kept a finished run")
	}

	for _, read := range []func() (toolpkg.WorkflowRunSnapshot, error){
		func() (toolpkg.WorkflowRunSnapshot, error) {
			return lookupWorkflowRunStatus(t.Context(), sessionDir, toolpkg.WorkflowStatusRequest{RunID: launched.RunID, WaitMs: 10_000})
		},
		func() (toolpkg.WorkflowRunSnapshot, error) {
			return stopWorkflowRun(t.Context(), sessionDir, toolpkg.WorkflowStopRequest{RunID: launched.RunID})
		},
	} {
		evicted, err := read()
		if err != nil {
			t.Fatalf("read evicted run: %v", err)
		}
		if evicted.Status != toolpkg.WorkflowStatusCompleted || string(evicted.Result) != string(final.Result) || evicted.Counts != final.Counts {
			t.Fatalf("evicted = %+v, want the final state %+v", evicted, final)
		}
	}

	for _, runID := range []string{"wf_000000000000", "../" + launched.RunID} {
		if _, err := lookupWorkflowRunStatus(t.Context(), sessionDir, toolpkg.WorkflowStatusRequest{RunID: runID}); err == nil || !strings.Contains(err.Error(), "unknown workflow run") {
			t.Fatalf("lookup %q: err = %v, want an unknown run", runID, err)
		}
	}
}

// Eviction must take only the run it was scheduled for, and only once it has
// ended: a status call must never lose a run that is still going.
func TestEvictWorkflowRunRemovesOnlyThatFinishedRun(t *testing.T) {
	runID := newWorkflowRunID()
	finishedRun := &workflowRun{id: runID, done: make(chan struct{})}
	close(finishedRun.done)
	runningRun := &workflowRun{id: runID, done: make(chan struct{})}
	registerWorkflowRun(runningRun)
	t.Cleanup(func() {
		workflowRunsMu.Lock()
		delete(workflowRuns, runID)
		workflowRunsMu.Unlock()
	})

	evictWorkflowRun(finishedRun)
	if current, ok := findWorkflowRun(runID); !ok || current != runningRun {
		t.Fatal("evicting one run removed another registered under its id")
	}
	evictWorkflowRun(runningRun)
	if _, ok := findWorkflowRun(runID); !ok {
		t.Fatal("a run that has not finished was evicted")
	}
}

// Resuming is what makes a long run affordable to fix: unchanged agent()
// calls replay from the journal instead of running again.
func TestWorkflowResumeFromARunOfThisSessionReplaysWithoutRunningAgents(t *testing.T) {
	sessionDir := t.TempDir()
	runner := &fakeChildRunner{}
	script := parseTestWorkflow(t, "return await parallel([() => agent('a'), () => agent('b')])")
	first := launchTestWorkflow(t, testWorkflowDeps(runner, sessionDir), toolpkg.WorkflowLaunchRequest{Script: script})
	firstFinal := waitForTestWorkflow(t, sessionDir, first.RunID)
	if len(runner.calls()) != 2 {
		t.Fatalf("first run made %d calls", len(runner.calls()))
	}

	replayer := &fakeChildRunner{respond: func(context.Context, toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		return toolpkg.AgentRunResult{}, errors.New("a resumed run must not run an unchanged agent")
	}}
	second := launchTestWorkflow(t, testWorkflowDeps(replayer, sessionDir), toolpkg.WorkflowLaunchRequest{Script: script, ResumeFromRunID: first.RunID})
	if second.ResumedFrom != first.RunID || len(second.Warnings) != 0 {
		t.Fatalf("second launch = %+v", second)
	}
	final := waitForTestWorkflow(t, sessionDir, second.RunID)
	if len(replayer.calls()) != 0 {
		t.Fatalf("resume ran %d agents again", len(replayer.calls()))
	}
	if final.Counts.Cached != 2 || string(final.Result) != string(firstFinal.Result) || final.ResumedFrom != first.RunID {
		t.Fatalf("resumed = %+v", final)
	}
}

// A resume has to come from a journal of this session's own runs. For an id
// with no journal here, the run used to start cold without a word, and the
// model took it for a resume; an id with path parts read a journal from
// outside the session's workflows directory.
func TestWorkflowResumeRefusesRunIDsThatAreNotThisSessionsRuns(t *testing.T) {
	sessionDir := t.TempDir()
	runner := &fakeChildRunner{}
	script := parseTestWorkflow(t, "return await agent('a')")
	first := launchTestWorkflow(t, testWorkflowDeps(runner, sessionDir), toolpkg.WorkflowLaunchRequest{Script: script})
	waitForTestWorkflow(t, sessionDir, first.RunID)

	// A copy of that journal outside the workflows directory, where
	// "../elsewhere" would find it.
	journal, err := os.ReadFile(first.JournalPath)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	elsewhere := filepath.Join(sessionDir, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, workflowJournalFileName), journal, 0o600); err != nil {
		t.Fatalf("copy journal: %v", err)
	}

	cases := map[string]string{
		"../elsewhere":    "is not a workflow run id",
		"..":              "is not a workflow run id",
		"wf_000000000000": "has no journal in this session",
	}
	for runID, wantWarning := range cases {
		t.Run(runID, func(t *testing.T) {
			before := len(runner.calls())
			launched := launchTestWorkflow(t, testWorkflowDeps(runner, sessionDir), toolpkg.WorkflowLaunchRequest{Script: script, ResumeFromRunID: runID})
			if len(launched.Warnings) != 1 || !strings.Contains(launched.Warnings[0], wantWarning) || !strings.Contains(launched.Warnings[0], "nothing was replayed") {
				t.Fatalf("warnings = %q, want one saying %q and that nothing was replayed", launched.Warnings, wantWarning)
			}
			final := waitForTestWorkflow(t, sessionDir, launched.RunID)
			if len(runner.calls()) != before+1 || final.Counts.Cached != 0 {
				t.Fatalf("ran %d agents with %d cached, want the agent run live", len(runner.calls())-before, final.Counts.Cached)
			}
		})
	}
}

// A resume journal that cannot be read must not cost the new run its own
// journal: the run still has to be resumable itself.
func TestWorkflowKeepsItsJournalWhenTheResumeJournalCannotBeRead(t *testing.T) {
	sessionDir := t.TempDir()
	// A directory where the journal should be fails the read.
	if err := os.MkdirAll(filepath.Join(workflowRunDir(sessionDir, "wf_unreadable"), workflowJournalFileName), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runner := &fakeChildRunner{}
	script := parseTestWorkflow(t, "return await agent('a')")

	launched := launchTestWorkflow(t, testWorkflowDeps(runner, sessionDir), toolpkg.WorkflowLaunchRequest{Script: script, ResumeFromRunID: "wf_unreadable"})
	if len(launched.Warnings) != 1 || !strings.Contains(launched.Warnings[0], "wf_unreadable") {
		t.Fatalf("warnings = %q, want one naming the unreadable journal of wf_unreadable", launched.Warnings)
	}
	if final := waitForTestWorkflow(t, sessionDir, launched.RunID); final.Status != toolpkg.WorkflowStatusCompleted || len(runner.calls()) != 1 {
		t.Fatalf("final = %+v after %d calls, want the agent run", final, len(runner.calls()))
	}

	replayer := &fakeChildRunner{respond: func(context.Context, toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		return toolpkg.AgentRunResult{}, errors.New("the run kept no journal")
	}}
	resumed := launchTestWorkflow(t, testWorkflowDeps(replayer, sessionDir), toolpkg.WorkflowLaunchRequest{Script: script, ResumeFromRunID: launched.RunID})
	final := waitForTestWorkflow(t, sessionDir, resumed.RunID)
	if len(replayer.calls()) != 0 || final.Counts.Cached != 1 || len(resumed.Warnings) != 0 {
		t.Fatalf("resuming re-ran %d agents (cached %d, warnings %q); the run kept no journal", len(replayer.calls()), final.Counts.Cached, resumed.Warnings)
	}
}

// A run whose files cannot be written still runs, but the model has to learn
// that it cannot be resumed rather than find out when a resume replays
// nothing.
func TestWorkflowWithoutWritableFilesStillRunsAndSaysItCannotBeResumed(t *testing.T) {
	sessionDir := t.TempDir()
	// A file where the workflows directory belongs.
	if err := os.WriteFile(filepath.Join(sessionDir, workflowRunsDirName), nil, 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	runner := &fakeChildRunner{}
	launched := launchTestWorkflow(t, testWorkflowDeps(runner, sessionDir), toolpkg.WorkflowLaunchRequest{Script: parseTestWorkflow(t, "return await agent('a')")})
	if len(launched.Warnings) != 1 || !strings.Contains(launched.Warnings[0], "cannot be resumed") {
		t.Fatalf("warnings = %q, want one saying the run cannot be resumed", launched.Warnings)
	}
	final := waitForTestWorkflow(t, sessionDir, launched.RunID)
	if final.Status != toolpkg.WorkflowStatusCompleted || final.ResultPath != "" || final.JournalPath != "" {
		t.Fatalf("final = %+v; a file failure must not fail the run or point at files that do not exist", final)
	}
}

// agent() options are checked before a call runs or is keyed for resume. An
// empty type must become general-purpose, not the agent tool's Explore
// default, and a worktree agent must be refused: it moves the working
// directory of the whole engine while the conversation goes on beside it.
func TestCheckWorkflowAgentCallDefaultsTheTypeAndRefusesWorktrees(t *testing.T) {
	check := checkWorkflowAgentCall(nil)

	call := workflowpkg.AgentCall{Prompt: "p"}
	if err := check(&call); err != nil || call.AgentType != generalPurposeSubagentType {
		t.Fatalf("empty agentType -> %q, %v; want general-purpose", call.AgentType, err)
	}
	call = workflowpkg.AgentCall{Prompt: "p", AgentType: "explore"}
	if err := check(&call); err != nil || call.AgentType != exploreSubagentType {
		t.Fatalf("agentType explore -> %q, %v; want Explore", call.AgentType, err)
	}
	call = workflowpkg.AgentCall{Prompt: "p", AgentType: "wizard"}
	if err := check(&call); err == nil || !strings.Contains(err.Error(), "general-purpose, Explore, or verification") {
		t.Fatalf("unsupported agentType: err = %v, want one naming the valid types", err)
	}
	call = workflowpkg.AgentCall{Prompt: "p", Isolation: "worktree"}
	if err := check(&call); err == nil || !strings.Contains(err.Error(), "working directory") {
		t.Fatalf("worktree isolation: err = %v, want a refusal explaining why", err)
	}
}

// A script that asks for a worktree agent has to see the refusal, and the
// run has to go on instead of starting a child that moves the engine.
func TestWorkflowScriptSeesTheWorktreeRefusal(t *testing.T) {
	sessionDir := t.TempDir()
	runner := &fakeChildRunner{}
	script := parseTestWorkflow(t, `
try {
  await agent('edit', { isolation: 'worktree' })
  return 'ran'
} catch (error) {
  return String(error.message)
}`)
	launched := launchTestWorkflow(t, testWorkflowDeps(runner, sessionDir), toolpkg.WorkflowLaunchRequest{Script: script})
	final := waitForTestWorkflow(t, sessionDir, launched.RunID)
	if !strings.Contains(string(final.Result), "not supported in workflows") || len(runner.calls()) != 0 {
		t.Fatalf("result = %s after %d calls, want the refusal and no agent", final.Result, len(runner.calls()))
	}
}

// The TUI gets a whole snapshot per update, so a run of a thousand agents
// must be cut down, keeping the agents a person watches: the running ones.
func TestWorkflowUpdatedPayloadCapsAgentsAndPutsRunningAgentsFirst(t *testing.T) {
	snapshot := toolpkg.WorkflowRunSnapshot{RunID: "wf_wide", Status: toolpkg.WorkflowStatusRunning}
	for index := 1; index <= 300; index++ {
		status := string(workflowpkg.AgentSucceeded)
		if index%100 == 0 {
			status = string(workflowpkg.AgentRunning)
		}
		snapshot.Agents = append(snapshot.Agents, toolpkg.WorkflowAgentSnapshot{
			Index:         index,
			Status:        status,
			OutputPreview: strings.Repeat("x", 1000),
		})
	}
	snapshot.Counts.Agents = 300
	for index := range 50 {
		snapshot.Logs = append(snapshot.Logs, fmt.Sprintf("line %d", index))
	}
	snapshot.Result = json.RawMessage(`"` + strings.Repeat("r", 5000) + `"`)

	payload := workflowUpdatedPayload(snapshot)
	if len(payload.Agents) != 200 || payload.AgentCount != 300 {
		t.Fatalf("agents = %d of %d, want 200 of 300", len(payload.Agents), payload.AgentCount)
	}
	for position, want := range []int{300, 200, 100} {
		if got := payload.Agents[position]; got.Index != want || got.Status != string(workflowpkg.AgentRunning) {
			t.Fatalf("agent %d = %+v, want running agent %d", position, got, want)
		}
	}
	if utf8.RuneCountInString(payload.Agents[0].OutputPreview) > workflowUpdatePreviewRunes {
		t.Fatalf("preview has %d runes", utf8.RuneCountInString(payload.Agents[0].OutputPreview))
	}
	if len(payload.Logs) != workflowUpdateLogs || payload.Logs[len(payload.Logs)-1] != "line 49" {
		t.Fatalf("logs = %q, want the last %d", payload.Logs, workflowUpdateLogs)
	}
	if utf8.RuneCountInString(payload.ResultPreview) > workflowUpdateResultRunes {
		t.Fatalf("result preview has %d runes", utf8.RuneCountInString(payload.ResultPreview))
	}
}

// workflowUpdates decodes the workflow_updated events a bridge wrote.
func workflowUpdates(t *testing.T, output *lockedBuffer) []ipc.WorkflowUpdatedPayload {
	t.Helper()
	var updates []ipc.WorkflowUpdatedPayload
	for line := range strings.SplitSeq(strings.TrimSpace(output.String()), "\n") {
		if line == "" {
			continue
		}
		var event ipc.StreamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode event %q: %v", line, err)
		}
		if event.Type != ipc.EventWorkflowUpdated {
			continue
		}
		var payload ipc.WorkflowUpdatedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("decode workflow_updated: %v", err)
		}
		updates = append(updates, payload)
	}
	return updates
}

// A run outlives the session that launched it. Once /clear or /resume moves
// the engine on, its updates must not land in the session that replaced its
// own, where the TUI would file the run, and its completion notice, as that
// session's. While its session is active, the last update it sends is final.
func TestWorkflowUpdatesReachOnlyTheSessionThatLaunchedTheRun(t *testing.T) {
	for _, tc := range []struct {
		name          string
		activeSession string
		wantUpdates   bool
	}{
		{name: "owner active", activeSession: "session-a", wantUpdates: true},
		{name: "owner left", activeSession: "session-b", wantUpdates: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useActiveSession(t, tc.activeSession)
			sessionDir := t.TempDir()
			output := &lockedBuffer{}
			deps := testWorkflowDeps(&fakeChildRunner{}, sessionDir)
			deps.bridge = ipc.NewBridge(strings.NewReader(""), output)
			deps.ownerSessionID = "session-a"

			launched := launchTestWorkflow(t, deps, toolpkg.WorkflowLaunchRequest{Script: parseTestWorkflow(t, "return await agent('a')")})
			waitForTestWorkflow(t, sessionDir, launched.RunID)

			updates := workflowUpdates(t, output)
			if !tc.wantUpdates {
				if len(updates) != 0 {
					t.Fatalf("a run of a session that was left sent %d updates", len(updates))
				}
				return
			}
			if len(updates) == 0 {
				t.Fatal("the run sent no updates to its own session")
			}
			last := updates[len(updates)-1]
			if last.RunID != launched.RunID || last.Status != toolpkg.WorkflowStatusCompleted || last.ResultPreview != `"done: a"` {
				t.Fatalf("last update = %+v, want the completed run", last)
			}
		})
	}
}

// A malformed or stale stop request from the TUI is not worth ending the
// session over: it becomes a notice. A real one stops the run.
func TestWorkflowStopMessageStopsTheRunAndTurnsBadInputIntoNotices(t *testing.T) {
	output := &lockedBuffer{}
	bridge := ipc.NewBridge(strings.NewReader(""), output)
	for _, raw := range []string{`not json`, `{"run_id":" "}`, `{"run_id":"wf_000000000000"}`} {
		if err := handleWorkflowStopMessage(bridge, json.RawMessage(raw)); err != nil {
			t.Fatalf("handleWorkflowStopMessage(%s) = %v, want a notice instead", raw, err)
		}
	}
	if notices := strings.Count(output.String(), `"type":"notice"`); notices != 3 {
		t.Fatalf("sent %d notices for 3 bad requests:\n%s", notices, output.String())
	}

	sessionDir := t.TempDir()
	started := make(chan struct{}, 1)
	runner := &fakeChildRunner{respond: func(ctx context.Context, req toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		started <- struct{}{}
		<-ctx.Done()
		return toolpkg.AgentRunResult{}, ctx.Err()
	}}
	launched := launchTestWorkflow(t, testWorkflowDeps(runner, sessionDir), toolpkg.WorkflowLaunchRequest{Script: parseTestWorkflow(t, "return await agent('forever')")})
	<-started
	if err := handleWorkflowStopMessage(bridge, json.RawMessage(`{"run_id":"`+launched.RunID+`"}`)); err != nil {
		t.Fatalf("handleWorkflowStopMessage: %v", err)
	}
	if final := waitForTestWorkflow(t, sessionDir, launched.RunID); final.Status != toolpkg.WorkflowStatusStopped {
		t.Fatalf("status = %q, want stopped", final.Status)
	}
}

func TestLaunchWorkflowRunRequiresARunner(t *testing.T) {
	_, err := launchWorkflowRun(workflowLaunchDeps{sessionDir: t.TempDir()}, toolpkg.WorkflowLaunchRequest{Script: parseTestWorkflow(t, "return 1")})
	if err == nil {
		t.Fatal("expected an error without an agent runner")
	}
}

// A relative script path and a saved workflow's name both resolve from the
// directory the conversation works in.
func TestLoadWorkflowRefResolvesPathsAndSavedNamesFromTheWorkingDirectory(t *testing.T) {
	isolateUserConfig(t)
	cwd := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd, ".git"), 0o700); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	writeWorkflowFile(t, filepath.Join(cwd, "scripts"), "local.js", "local")
	writeWorkflowFile(t, filepath.Join(cwd, ".nami", "workflows"), "review.js", "review")

	local, err := loadWorkflowRef(workflowpkg.Ref{ScriptPath: filepath.Join("scripts", "local.js")}, cwd)
	if err != nil || local.Meta.Name != "local" {
		t.Fatalf("relative path = %+v, %v", local.Meta, err)
	}
	saved, err := loadWorkflowRef(workflowpkg.Ref{Name: "REVIEW"}, cwd)
	if err != nil || saved.Meta.Name != "review" {
		t.Fatalf("saved name = %+v, %v", saved.Meta, err)
	}
	if _, err := loadWorkflowRef(workflowpkg.Ref{Name: "absent"}, cwd); err == nil || !strings.Contains(err.Error(), "saved workflows are review") {
		t.Fatalf("absent name: err = %v, want one listing the saved workflows", err)
	}
}

func writeWorkflowFile(t *testing.T, dir string, file string, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, file)
	source := "export const meta = { name: '" + name + "', description: 'the " + name + " workflow', whenToUse: 'when " + name + " is needed' }\nreturn 1\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestWorkflowRunIDsAreUnique(t *testing.T) {
	seen := map[string]struct{}{}
	for range 1000 {
		id := newWorkflowRunID()
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate run id %q", id)
		}
		seen[id] = struct{}{}
		if !strings.HasPrefix(id, "wf_") || !toolpkg.ValidWorkflowRunID(id) {
			t.Fatalf("run id %q is not a valid wf_ id", id)
		}
	}
}

// Every run's files are named after its id, and a resumed session's runs
// share its workflows directory with runs from earlier processes. A counter
// that restarts with each process gave the first run of every process the id
// wf_1, so two processes' runs shared an id and a journal.
func TestWorkflowRunIDsDifferAcrossProcesses(t *testing.T) {
	if os.Getenv("NAMI_TEST_PRINT_WORKFLOW_RUN_ID") == "1" {
		fmt.Println("run-id:" + newWorkflowRunID())
		return
	}
	first := firstWorkflowRunIDOfANewProcess(t)
	second := firstWorkflowRunIDOfANewProcess(t)
	if first == second {
		t.Fatalf("two processes both started with run id %q", first)
	}
	for _, id := range []string{first, second} {
		if !strings.HasPrefix(id, "wf_") || len(id) > len("wf_")+16 {
			t.Fatalf("run id %q should keep the wf_ prefix and stay short enough to type", id)
		}
	}
}

// firstWorkflowRunIDOfANewProcess runs this test binary again and returns the
// first run id the new process hands out.
func firstWorkflowRunIDOfANewProcess(t *testing.T) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestWorkflowRunIDsDifferAcrossProcesses$", "-test.count=1")
	cmd.Env = append(os.Environ(), "NAMI_TEST_PRINT_WORKFLOW_RUN_ID=1")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("run the test binary: %v\n%s", err, output)
	}
	for line := range strings.Lines(string(output)) {
		if id, found := strings.CutPrefix(strings.TrimSpace(line), "run-id:"); found {
			return id
		}
	}
	t.Fatalf("the new process printed no run id:\n%s", output)
	return ""
}

// A finished run must answer a waiting status call at once rather than sit
// out the whole wait.
func TestWorkflowStatusWaitReturnsAtOnceForAFinishedRun(t *testing.T) {
	sessionDir := t.TempDir()
	launched := launchTestWorkflow(t, testWorkflowDeps(&fakeChildRunner{}, sessionDir), toolpkg.WorkflowLaunchRequest{Script: parseTestWorkflow(t, "return await agent('a')")})
	waitForTestWorkflow(t, sessionDir, launched.RunID)

	started := time.Now()
	if _, err := lookupWorkflowRunStatus(t.Context(), sessionDir, toolpkg.WorkflowStatusRequest{RunID: launched.RunID, WaitMs: 30_000}); err != nil {
		t.Fatalf("lookupWorkflowRunStatus: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("waited %v on a finished run", elapsed)
	}
}

// A run that ends while a status call is waiting must wake that call, not
// leave it sitting out the full wait.
func TestWorkflowStatusWaitWakesWhenTheRunFinishes(t *testing.T) {
	sessionDir := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})
	runner := &fakeChildRunner{respond: func(ctx context.Context, req toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return toolpkg.AgentRunResult{}, ctx.Err()
		}
		return toolpkg.AgentRunResult{Status: "completed", Summary: "done"}, nil
	}}
	launched := launchTestWorkflow(t, testWorkflowDeps(runner, sessionDir), toolpkg.WorkflowLaunchRequest{Script: parseTestWorkflow(t, "return await agent('held')")})
	<-started

	go func() {
		time.Sleep(50 * time.Millisecond)
		close(release)
	}()
	waitStarted := time.Now()
	status, err := lookupWorkflowRunStatus(t.Context(), sessionDir, toolpkg.WorkflowStatusRequest{RunID: launched.RunID, WaitMs: 30_000})
	if err != nil {
		t.Fatalf("lookupWorkflowRunStatus: %v", err)
	}
	if elapsed := time.Since(waitStarted); elapsed > 5*time.Second {
		t.Fatalf("waited %v; the run's end did not wake the status call", elapsed)
	}
	if status.Status != toolpkg.WorkflowStatusCompleted {
		t.Fatalf("status = %q, want completed", status.Status)
	}
}

// A run that settles while its session is not shown cannot send its final
// state. When the session comes back the engine re-announces it, which is how
// the TUI learns the run ended and tells the model.
func TestARunThatSettledWhileItsSessionWasAwayIsAnnouncedWhenItReturns(t *testing.T) {
	useActiveSession(t, "session-b")
	sessionDir := t.TempDir()
	output := &lockedBuffer{}
	deps := testWorkflowDeps(&fakeChildRunner{}, sessionDir)
	deps.bridge = ipc.NewBridge(strings.NewReader(""), output)
	deps.ownerSessionID = "session-a"

	launched := launchTestWorkflow(t, deps, toolpkg.WorkflowLaunchRequest{Script: parseTestWorkflow(t, "return await agent('a')")})
	waitForTestWorkflow(t, sessionDir, launched.RunID)
	if updates := workflowUpdates(t, output); len(updates) != 0 {
		t.Fatalf("a run of a session that was away sent %d updates", len(updates))
	}

	setActiveSession("session-a")
	announceWorkflowRuns("session-a")
	updates := workflowUpdates(t, output)
	if len(updates) != 1 || updates[0].RunID != launched.RunID || updates[0].Status != toolpkg.WorkflowStatusCompleted {
		t.Fatalf("updates after the session returned = %+v, want the run's final state once", updates)
	}
}

// A workflow's agents resolve relative paths against the process's working
// directory. While a run goes on, nothing may move that directory: not a
// worktree child, not a worktree switch, and not a /resume into a session
// that lives somewhere else.
func TestNothingMovesTheWorkingDirectoryWhileAWorkflowRuns(t *testing.T) {
	release := make(chan struct{})
	runner := &fakeChildRunner{respond: func(ctx context.Context, req toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return toolpkg.AgentRunResult{Status: "completed", Summary: "done"}, nil
	}}
	sessionDir := t.TempDir()
	launched := launchTestWorkflow(t, testWorkflowDeps(runner, sessionDir), toolpkg.WorkflowLaunchRequest{Script: parseTestWorkflow(t, "return await agent('hold')")})

	if !hasRunningWorkflows() {
		t.Fatal("hasRunningWorkflows() = false while a run is going")
	}
	_, err := prepareDelegatedWorkspace(t.Context(), toolpkg.AgentRunRequest{Description: "edit", WorkspaceStrategy: "worktree"}, "invocation", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "workflows") {
		t.Fatalf("worktree child error = %v, want it refused while a workflow runs", err)
	}
	if _, err := (&sessionControlRuntime{}).EnterWorktree(t.Context(), toolpkg.WorktreeControlRequest{}); !errors.Is(err, errWorkflowsHoldTheDirectory) {
		t.Fatalf("EnterWorktree error = %v, want it refused while a workflow runs", err)
	}
	if _, err := (&sessionControlRuntime{}).ExitWorktree(t.Context()); !errors.Is(err, errWorkflowsHoldTheDirectory) {
		t.Fatalf("ExitWorktree error = %v, want it refused while a workflow runs", err)
	}

	store := session.NewStore(t.TempDir())
	elsewhere := t.TempDir()
	if err := store.SaveMetadata(session.Metadata{SessionID: "elsewhere", CWD: elsewhere}); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}
	here, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := store.SaveMetadata(session.Metadata{SessionID: "here", CWD: here}); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}
	if reason := workflowsBlockDirectoryMove(store, "elsewhere"); !strings.Contains(reason, elsewhere) {
		t.Fatalf("resume into another directory: reason = %q, want it refused", reason)
	}
	if reason := workflowsBlockDirectoryMove(store, "here"); reason != "" {
		t.Fatalf("resume into this directory: reason = %q, want it allowed", reason)
	}

	close(release)
	waitForTestWorkflow(t, sessionDir, launched.RunID)
	if reason := workflowsBlockDirectoryMove(store, "elsewhere"); reason != "" {
		t.Fatalf("resume after the run ended: reason = %q, want it allowed", reason)
	}
}
