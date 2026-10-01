package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	workflowpkg "github.com/channyeintun/nami/internal/workflow"
)

const testWorkflowScript = "export const meta = { name: 'review', description: 'Review the diff' }\nreturn await agent('look')\n"

func noopWorkflowLauncher(context.Context, WorkflowLaunchRequest) (WorkflowLaunchResult, error) {
	return WorkflowLaunchResult{Status: "launched", RunID: "wf_test"}, nil
}

// fakeWorkflowLoader serves saved workflows and script paths from memory.
func fakeWorkflowLoader(scripts map[string]string) WorkflowScriptLoader {
	return func(ref workflowpkg.Ref) (workflowpkg.Script, error) {
		key := ref.Name + ref.ScriptPath
		source, ok := scripts[key]
		if !ok {
			return workflowpkg.Script{}, fmt.Errorf("no workflow %q", key)
		}
		return workflowpkg.ParseScript(key, source)
	}
}

// The model must hear about a missing or ambiguous source before anything
// launches, and a script that does not parse must fail validation with the
// parser's message instead of failing as a run in the background.
func TestWorkflowToolValidateRejectsBadSourcesBeforeLaunch(t *testing.T) {
	tool := NewWorkflowTool(noopWorkflowLauncher, fakeWorkflowLoader(map[string]string{"saved": testWorkflowScript}), nil)
	cases := map[string]struct {
		params map[string]any
		want   string
	}{
		"no source":    {params: map[string]any{"args": []any{1}}, want: "requires one of script, script_path, or name"},
		"blank source": {params: map[string]any{"script": "  ", "name": ""}, want: "requires one of script, script_path, or name"},
		"two sources":  {params: map[string]any{"script": testWorkflowScript, "name": "saved"}, want: "exactly one of script, script_path, or name"},
		"syntax error": {params: map[string]any{"script": "export const meta = { name: 'x', description: 'd' }\nreturn (\n"}, want: "syntax error"},
		"missing meta": {params: map[string]any{"script": "return 1"}, want: "export const meta"},
		"unknown name": {params: map[string]any{"name": "absent"}, want: `no workflow "absent"`},
		"path-like resume id": {
			params: map[string]any{"script": testWorkflowScript, "resume_from_run_id": "../wf_other"},
			want:   "not a workflow run id",
		},
		"dot-dot resume id": {
			params: map[string]any{"script": testWorkflowScript, "resume_from_run_id": ".."},
			want:   "not a workflow run id",
		},
		"blank resume id": {
			params: map[string]any{"script": testWorkflowScript, "resume_from_run_id": " "},
			want:   "not a workflow run id",
		},
		"negative budget": {params: map[string]any{"script": testWorkflowScript, "token_budget": -1.0}, want: "token_budget must be >= 0"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateToolCall(tool, ToolInput{Params: tc.params})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}

	for _, params := range []map[string]any{
		{"script": testWorkflowScript, "resume_from_run_id": "wf_0123456789ab", "token_budget": 100.0},
		{"name": "saved"},
	} {
		if err := ValidateToolCall(tool, ToolInput{Params: params}); err != nil {
			t.Fatalf("ValidateToolCall(%v) = %v, want nil", params, err)
		}
	}
}

// args reaches the script exactly as the model wrote it. Decoding the params
// turned numbers into float64s, so re-encoding them would round a large id.
func TestWorkflowToolPassesArgsVerbatimToTheLauncher(t *testing.T) {
	var received WorkflowLaunchRequest
	tool := NewWorkflowTool(func(_ context.Context, req WorkflowLaunchRequest) (WorkflowLaunchResult, error) {
		received = req
		return WorkflowLaunchResult{Status: "launched", RunID: "wf_1", Name: req.Script.Meta.Name, Message: "running"}, nil
	}, nil, nil)
	raw := `{"script":` + mustJSON(t, testWorkflowScript) + `,"args":{"id":12345678901234567890,"tags":["a"]},"resume_from_run_id":"wf_0","token_budget":500}`
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}

	output, err := tool.Execute(t.Context(), ToolInput{Params: params, Raw: raw})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if string(received.Args) != `{"id":12345678901234567890,"tags":["a"]}` {
		t.Fatalf("Args = %s", received.Args)
	}
	if received.ResumeFromRunID != "wf_0" || received.TokenBudget != 500 || received.Script.Meta.Name != "review" {
		t.Fatalf("request = %+v", received)
	}
	var decoded WorkflowLaunchResult
	if err := json.Unmarshal([]byte(output.Output), &decoded); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if decoded.RunID != "wf_1" || decoded.Name != "review" || decoded.Status != "launched" {
		t.Fatalf("output = %+v", decoded)
	}

	// Without raw input, as when another tool calls this one, the decoded
	// value is encoded again; leaving args out leaves it nil.
	received = WorkflowLaunchRequest{}
	if _, err := tool.Execute(t.Context(), ToolInput{Params: map[string]any{"script": testWorkflowScript, "args": []any{"x"}}}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if string(received.Args) != `["x"]` {
		t.Fatalf("Args without raw input = %s", received.Args)
	}
	if _, err := tool.Execute(t.Context(), ToolInput{Params: map[string]any{"script": testWorkflowScript}}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if received.Args != nil {
		t.Fatalf("Args with none given = %s, want nil", received.Args)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(encoded)
}

// Saved workflows are only useful if the model knows they exist, but an empty
// heading on every request is noise.
func TestWorkflowToolDescriptionListsSavedWorkflowsOnlyWhenThereAreSome(t *testing.T) {
	var saved []workflowpkg.Saved
	var loadErr error
	tool := NewWorkflowTool(noopWorkflowLauncher, nil, func() ([]workflowpkg.Saved, error) { return saved, loadErr })
	if strings.Contains(tool.Description(), "Saved workflows") || strings.Contains(tool.Description(), "could not be loaded") {
		t.Fatal("the description has a saved workflows section with none saved")
	}

	saved = []workflowpkg.Saved{
		{Name: "review", Meta: workflowpkg.Meta{Description: "Review the diff", WhenToUse: "after a change"}},
		{Name: "triage", Meta: workflowpkg.Meta{Description: "Triage open issues"}},
	}
	loadErr = errors.New("saved workflow /work/.nami/workflows/broken.js: syntax error")
	description := tool.Description()
	for _, want := range []string{"Saved workflows", "- review — after a change", "- triage — Triage open issues", "could not be loaded: saved workflow /work/.nami/workflows/broken.js"} {
		if !strings.Contains(description, want) {
			t.Fatalf("description lacks %q:\n%s", want, description)
		}
	}
}

func TestWorkflowToolPermissionTargetNamesTheScript(t *testing.T) {
	tool := NewWorkflowTool(noopWorkflowLauncher, fakeWorkflowLoader(nil), nil)
	target := tool.PermissionTarget(ToolInput{Params: map[string]any{"script": testWorkflowScript}})
	if target.Kind != "workflow" || target.Value != "review: Review the diff" {
		t.Fatalf("target = %+v", target)
	}
	// A script that cannot be loaded still says where it would come from.
	target = tool.PermissionTarget(ToolInput{Params: map[string]any{"script_path": "/tmp/missing.js"}})
	if target.Kind != "workflow" || target.Value != "/tmp/missing.js" {
		t.Fatalf("fallback target = %+v", target)
	}
}

// A long wait must not hold the turn indefinitely, and a stop without a wait
// still gives the run a moment to settle so the returned state is final.
func TestWorkflowStatusAndStopToolsBoundTheirWaits(t *testing.T) {
	var statusWait, stopWait int
	status := NewWorkflowStatusTool(func(_ context.Context, req WorkflowStatusRequest) (WorkflowRunSnapshot, error) {
		statusWait = req.WaitMs
		return WorkflowRunSnapshot{RunID: req.RunID, Status: WorkflowStatusRunning}, nil
	})
	stop := NewWorkflowStopTool(func(_ context.Context, req WorkflowStopRequest) (WorkflowRunSnapshot, error) {
		stopWait = req.WaitMs
		return WorkflowRunSnapshot{RunID: req.RunID, Status: WorkflowStatusStopped}, nil
	})

	if _, err := status.Execute(t.Context(), ToolInput{Params: map[string]any{"run_id": "wf_1", "wait_ms": 5_000_000.0}}); err != nil {
		t.Fatalf("status Execute: %v", err)
	}
	if statusWait != MaxWorkflowWaitMs {
		t.Fatalf("status wait = %d, want it capped at %d", statusWait, MaxWorkflowWaitMs)
	}
	if _, err := stop.Execute(t.Context(), ToolInput{Params: map[string]any{"run_id": "wf_1"}}); err != nil {
		t.Fatalf("stop Execute: %v", err)
	}
	if stopWait != defaultWorkflowStopWaitMs {
		t.Fatalf("stop wait = %d, want the default %d", stopWait, defaultWorkflowStopWaitMs)
	}
	for _, tool := range []Tool{status, stop} {
		if err := ValidateToolCall(tool, ToolInput{Params: map[string]any{"run_id": " "}}); err == nil {
			t.Fatalf("%s accepted a blank run_id", tool.Name())
		}
		if err := ValidateToolCall(tool, ToolInput{Params: map[string]any{"run_id": "wf_1", "wait_ms": -1.0}}); err == nil {
			t.Fatalf("%s accepted a negative wait_ms", tool.Name())
		}
	}
}

// A run with a thousand agents, chatty logs, and a large result must not
// flood the transcript; and the agents a reader needs first, the running
// and failed ones, must survive the cap.
func TestDisplaySafeWorkflowSnapshotCapsWhatTheTranscriptSees(t *testing.T) {
	snapshot := WorkflowRunSnapshot{RunID: "wf_big", Status: WorkflowStatusRunning}
	for index := 1; index <= 1000; index++ {
		status := string(workflowpkg.AgentSucceeded)
		switch index {
		case 3:
			status = string(workflowpkg.AgentRunning)
		case 5:
			status = string(workflowpkg.AgentFailed)
		}
		snapshot.Agents = append(snapshot.Agents, WorkflowAgentSnapshot{
			Index:         index,
			Status:        status,
			OutputPreview: strings.Repeat("o", 5000),
			Error:         strings.Repeat("e", 5000),
		})
	}
	for index := range 50 {
		snapshot.Logs = append(snapshot.Logs, fmt.Sprintf("log %d", index))
	}
	snapshot.Result = json.RawMessage(mustJSON(t, strings.Repeat("r", 50_000)))

	display := DisplaySafeWorkflowSnapshot(snapshot)
	if len(display.Agents) != MaxWorkflowDisplayAgents {
		t.Fatalf("agents shown = %d, want %d", len(display.Agents), MaxWorkflowDisplayAgents)
	}
	if display.Agents[0].Index != 3 || display.Agents[1].Index != 5 || display.Agents[2].Index != 1000 {
		t.Fatalf("first agents = %d, %d, %d; want running 3, failed 5, then the most recent", display.Agents[0].Index, display.Agents[1].Index, display.Agents[2].Index)
	}
	for _, agent := range display.Agents {
		if utf8.RuneCountInString(agent.OutputPreview) > WorkflowAgentPreviewRunes || utf8.RuneCountInString(agent.Error) > WorkflowAgentPreviewRunes {
			t.Fatalf("agent %d keeps %d preview runes", agent.Index, utf8.RuneCountInString(agent.OutputPreview))
		}
	}
	if len(display.Logs) != maxWorkflowDisplayLogs || display.Logs[len(display.Logs)-1] != "log 49" {
		t.Fatalf("logs = %v, want the last %d", display.Logs, maxWorkflowDisplayLogs)
	}
	var preview string
	if err := json.Unmarshal(display.Result, &preview); err != nil {
		t.Fatalf("a truncated result must still be valid JSON: %v", err)
	}
	if !display.ResultTruncated || utf8.RuneCountInString(preview) > maxWorkflowDisplayResult {
		t.Fatalf("result truncated = %v with %d runes", display.ResultTruncated, utf8.RuneCountInString(preview))
	}
	// The original keeps everything, since it is what result.json holds.
	if len(snapshot.Agents) != 1000 || len(snapshot.Logs) != 50 {
		t.Fatal("DisplaySafeWorkflowSnapshot changed the snapshot it was given")
	}

	small := DisplaySafeWorkflowSnapshot(WorkflowRunSnapshot{Result: json.RawMessage(`{"ok":true}`)})
	if string(small.Result) != `{"ok":true}` || small.ResultTruncated {
		t.Fatalf("a small result = %s (truncated %v), want it whole", small.Result, small.ResultTruncated)
	}
}

func TestTruncateWorkflowTextCountsRunesNotBytes(t *testing.T) {
	cases := []struct {
		value string
		max   int
		want  string
	}{
		{value: "short", max: 10, want: "short"},
		{value: "exact", max: 5, want: "exact"},
		{value: "toolong", max: 4, want: "too…"},
		{value: "héllo wörld", max: 6, want: "héllo…"},
		{value: "  padded  ", max: 6, want: "padded"},
		{value: "anything", max: 0, want: ""},
	}
	for _, tc := range cases {
		if got := TruncateWorkflowText(tc.value, tc.max); got != tc.want {
			t.Errorf("TruncateWorkflowText(%q, %d) = %q, want %q", tc.value, tc.max, got, tc.want)
		}
	}
}

func TestWorkflowToolsRequireTheirEngineHooks(t *testing.T) {
	params := map[string]any{"script": testWorkflowScript, "run_id": "wf_1"}
	for _, tool := range []Tool{NewWorkflowTool(nil, nil, nil), NewWorkflowStatusTool(nil), NewWorkflowStopTool(nil)} {
		err := ValidateToolCall(tool, ToolInput{Params: params})
		if err == nil || !strings.Contains(err.Error(), "not configured") {
			t.Fatalf("%s: err = %v, want a not configured error", tool.Name(), err)
		}
	}
	_, err := NewWorkflowTool(noopWorkflowLauncher, nil, nil).Execute(t.Context(), ToolInput{Params: map[string]any{"name": "saved"}})
	if err == nil || !strings.Contains(err.Error(), "loader is not configured") {
		t.Fatalf("err = %v, want a missing loader to fail the call", err)
	}
}
