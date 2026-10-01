package tools

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	workflowpkg "github.com/channyeintun/nami/internal/workflow"
)

// Workflow run statuses, as workflow_status and the TUI report them.
const (
	WorkflowStatusRunning   = "running"
	WorkflowStatusCompleted = "completed"
	WorkflowStatusFailed    = "failed"
	WorkflowStatusStopped   = "stopped"
)

const (
	// MaxWorkflowWaitMs caps how long workflow_status and workflow_stop
	// block, so a forgotten wait cannot hold the turn for an hour.
	MaxWorkflowWaitMs = 600_000
	// defaultWorkflowStopWaitMs gives a stopped run time to wind its agents
	// down, so the snapshot workflow_stop returns is usually the final one.
	defaultWorkflowStopWaitMs = 5000

	// MaxWorkflowDisplayAgents caps the agents one snapshot lists. Counts
	// still cover every agent; the full outcomes stay in the journal and the
	// agents' transcripts.
	MaxWorkflowDisplayAgents = 200
	// WorkflowAgentPreviewRunes caps an agent's output preview and error in
	// workflow_status.
	WorkflowAgentPreviewRunes    = 1000
	maxWorkflowDisplayLogs       = 20
	maxWorkflowDisplayResult     = 20_000
	maxWorkflowDisplayErrorRunes = 4000
)

// WorkflowLaunchRequest asks the engine to start a parsed script in the
// background.
type WorkflowLaunchRequest struct {
	Script workflowpkg.Script
	// Args is the args input exactly as the model wrote it, or nil when it
	// was left out.
	Args            json.RawMessage
	ResumeFromRunID string
	TokenBudget     int
}

// WorkflowLaunchResult is what the workflow tool returns: the run has started
// and goes on after the call returns.
type WorkflowLaunchResult struct {
	Status      string `json:"status"`
	RunID       string `json:"run_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// ScriptPath is the run's own copy of its script. Editing it and passing
	// it back as script_path with resume_from_run_id reruns only what changed.
	ScriptPath  string `json:"script_path,omitempty"`
	JournalPath string `json:"journal_path,omitempty"`
	ResumedFrom string `json:"resumed_from,omitempty"`
	// Warnings are problems that did not stop the launch, such as a resume
	// journal that could not be read.
	Warnings []string `json:"warnings,omitempty"`
	Message  string   `json:"message"`
}

// WorkflowPhase is one phase of a run, in the order phases were first seen.
type WorkflowPhase struct {
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	// Workflow names the nested workflow the phase belongs to, or is empty
	// for the top-level script.
	Workflow string `json:"workflow,omitempty"`
}

// WorkflowAgentCounts tallies a run's agent() calls by status.
type WorkflowAgentCounts struct {
	Agents    int `json:"agents"`
	Running   int `json:"running"`
	Queued    int `json:"queued"`
	Succeeded int `json:"succeeded"`
	Cached    int `json:"cached"`
	Failed    int `json:"failed"`
	Stopped   int `json:"stopped"`
}

// WorkflowAgentSnapshot is one agent() call of a run.
type WorkflowAgentSnapshot struct {
	Index          int    `json:"index"`
	Label          string `json:"label"`
	Phase          string `json:"phase,omitempty"`
	Workflow       string `json:"workflow,omitempty"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	DurationMs     int64  `json:"duration_ms,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	// OutputPreview is the start of the agent's text, or of its structured
	// output as JSON. The whole output is in the agent's transcript.
	OutputPreview string `json:"output_preview,omitempty"`
}

// WorkflowRunSnapshot is a run's state. The engine keeps every agent and the
// whole result in it, and writes it to result_path when the run ends;
// DisplaySafeWorkflowSnapshot trims it for the transcript.
type WorkflowRunSnapshot struct {
	Status       string                  `json:"status"`
	RunID        string                  `json:"run_id"`
	Name         string                  `json:"name"`
	Description  string                  `json:"description,omitempty"`
	CurrentPhase string                  `json:"current_phase,omitempty"`
	Phases       []WorkflowPhase         `json:"phases,omitempty"`
	Counts       WorkflowAgentCounts     `json:"counts"`
	Agents       []WorkflowAgentSnapshot `json:"agents,omitempty"`
	Logs         []string                `json:"logs,omitempty"`
	// Result is the script's return value. In a display-safe snapshot a
	// value too large to show becomes a string holding the start of its
	// JSON, and ResultTruncated is set; the whole value is in result_path.
	Result          json.RawMessage `json:"result,omitempty"`
	ResultTruncated bool            `json:"result_truncated,omitempty"`
	ResultPath      string          `json:"result_path,omitempty"`
	Error           string          `json:"error,omitempty"`
	// Warnings are problems that did not stop the run, such as a journal
	// that could not be written.
	Warnings     []string  `json:"warnings,omitempty"`
	ScriptPath   string    `json:"script_path,omitempty"`
	JournalPath  string    `json:"journal_path,omitempty"`
	ResumedFrom  string    `json:"resumed_from,omitempty"`
	StartedAt    time.Time `json:"started_at,omitzero"`
	CompletedAt  time.Time `json:"completed_at,omitzero"`
	DurationMs   int64     `json:"duration_ms,omitempty"`
	TotalCostUSD float64   `json:"total_cost_usd,omitempty"`
	InputTokens  int       `json:"input_tokens,omitempty"`
	OutputTokens int       `json:"output_tokens,omitempty"`
}

// WorkflowStatusRequest asks for a run's state, waiting up to WaitMs for it
// to finish.
type WorkflowStatusRequest struct {
	RunID  string
	WaitMs int
}

// WorkflowStopRequest asks to stop a run, waiting up to WaitMs for it to
// settle.
type WorkflowStopRequest struct {
	RunID  string
	WaitMs int
}

// WorkflowLauncher starts a run and returns as soon as it is under way.
type WorkflowLauncher func(context.Context, WorkflowLaunchRequest) (WorkflowLaunchResult, error)

// WorkflowScriptLoader resolves a saved workflow's name or a script path to
// a parsed script.
type WorkflowScriptLoader func(workflowpkg.Ref) (workflowpkg.Script, error)

// SavedWorkflowLister lists the saved workflows the model can run by name.
// Like workflowpkg.LoadSaved, it returns the good ones alongside an error
// naming any that could not be loaded.
type SavedWorkflowLister func() ([]workflowpkg.Saved, error)

// WorkflowStatusLookup reports on a run by id.
type WorkflowStatusLookup func(context.Context, WorkflowStatusRequest) (WorkflowRunSnapshot, error)

// WorkflowStopper stops a run by id.
type WorkflowStopper func(context.Context, WorkflowStopRequest) (WorkflowRunSnapshot, error)

// ValidWorkflowRunID reports whether id can name a run. A run id names a
// directory in the session's workflows directory, so one with path parts
// would reach outside it.
func ValidWorkflowRunID(id string) bool {
	id = strings.TrimSpace(id)
	return id != "" && id != "." && id != ".." && id == filepath.Base(id)
}

// WorkflowTool runs a workflow script in the background.
type WorkflowTool struct {
	launch    WorkflowLauncher
	load      WorkflowScriptLoader
	listSaved SavedWorkflowLister
}

// WorkflowStatusTool reports on a workflow run.
type WorkflowStatusTool struct {
	lookup WorkflowStatusLookup
}

// WorkflowStopTool stops a workflow run.
type WorkflowStopTool struct {
	stop WorkflowStopper
}

// NewWorkflowTool builds the workflow tool. load resolves script_path and
// name inputs; listSaved feeds the saved workflows section of the
// description and may be nil.
func NewWorkflowTool(launch WorkflowLauncher, load WorkflowScriptLoader, listSaved SavedWorkflowLister) *WorkflowTool {
	return &WorkflowTool{launch: launch, load: load, listSaved: listSaved}
}

func NewWorkflowStatusTool(lookup WorkflowStatusLookup) *WorkflowStatusTool {
	return &WorkflowStatusTool{lookup: lookup}
}

func NewWorkflowStopTool(stop WorkflowStopper) *WorkflowStopTool {
	return &WorkflowStopTool{stop: stop}
}

func (t *WorkflowTool) Name() string { return "workflow" }

const workflowToolDescription = `Run a JavaScript workflow that orchestrates child agents. The script runs in the background in a sandbox with no filesystem, network, or process access: this call returns a run_id at once, and a <task-notification> arrives when the run finishes. Check on it with workflow_status; stop it with workflow_stop.

Use a workflow when the control flow should be deterministic code instead of your own turn-by-turn decisions: fanning out over items you discovered, verifying each finding independently, looping until a check passes. For one delegated task use agent; for a few independent ones use agent_team.

Every script begins with a pure-literal meta export (no variables, calls, spreads, or interpolation):
  export const meta = {
    name: 'review-changes',
    description: 'Review the diff and verify each finding',
    phases: [{ title: 'Find' }, { title: 'Verify' }],
  }
The body runs inside an async function: await at top level and return a JSON-serializable value. Plain JavaScript only; TypeScript annotations fail to parse.

Hooks:
- agent(prompt, opts?) resolves to the child's final text, or, with opts.schema (a JSON Schema whose root is {type: 'object', properties: {...}}), to the validated object. It resolves to null when the agent fails, so filter results with .filter(Boolean). opts: label (display name), phase (progress group; use it inside pipeline/parallel stages), schema, model ('provider/model'; omit to use the session's subagent model), effort ('low'|'medium'|'high'|'xhigh'|'max'; only OpenAI reasoning models use it), agentType ('general-purpose' by default; 'Explore' for read-only search; 'verification' to run builds and tests without editing).
- pipeline(items, stage1, stage2, ...) runs each item through every stage on its own, with no barrier between stages; a stage gets (previousResult, item, index). A stage that throws turns its item into null. Prefer this for multi-stage work.
- parallel(tasks) runs functions concurrently and waits for all of them: a barrier. A task that throws becomes null. Use it only when the next step needs every result at once, such as deduplicating all findings or stopping early when there are none.
- phase(title) starts a progress group; log(message) prints a progress line.
- args is the args input, verbatim. Pass arrays and objects as JSON values, not as JSON-encoded strings.
- budget is {total, spent(), remaining()} in output tokens, from token_budget. total is null without one. Once spent() reaches it, agent() throws and agents still queued resolve to null without starting; agents already running finish.
- workflow(name | {scriptPath}, args) runs a saved workflow or a script file inline and returns its value; nesting is one level deep.

Rules: Date.now(), Math.random(), and new Date() without arguments throw, because resume matches agent() calls by their prompts. Agents beyond the concurrency limit queue; a run makes at most 1000 agent() calls; parallel() and pipeline() take at most 4096 items. Child agents cannot ask for approval, so in the default permission mode they cannot edit files or run commands that are not read-only. isolation: 'worktree' is not supported. Children see the project instructions but nothing else of this conversation: put everything a step needs, including earlier results, in its prompt. A child's final answer is returned to the script as data.

Resume: every run journals each successful agent() result. Pass resume_from_run_id (a run of this session) with the same or an edited script: unchanged calls replay instantly; a changed call runs live, and so does every call made after a re-run agent has returned.

Example:
  export const meta = { name: 'verify-bugs', description: 'Find bugs per package and verify each' }
  const BUGS = { type: 'object', properties: { bugs: { type: 'array', items: { type: 'object', properties: { file: { type: 'string' }, claim: { type: 'string' } }, required: ['file', 'claim'] } } }, required: ['bugs'] }
  const VERDICT = { type: 'object', properties: { real: { type: 'boolean' }, reason: { type: 'string' } }, required: ['real', 'reason'] }
  const perPackage = await pipeline(args.packages,
    pkg => agent(` + "`List likely bugs in ${pkg}.`" + `, { label: ` + "`find ${pkg}`" + `, phase: 'Find', schema: BUGS, agentType: 'Explore' }),
    found => parallel((found?.bugs ?? []).map(bug => () =>
      agent(` + "`Try to refute: ${bug.claim} (${bug.file}). Answer real=false if unsure.`" + `, { phase: 'Verify', schema: VERDICT })
        .then(verdict => (verdict?.real ? bug : null)))))
  return perPackage.flat().filter(Boolean)`

// These keep the saved workflows section of the description short, since the
// description is sent with every request.
const (
	maxSavedWorkflowSummaryRunes = 200
	maxSavedWorkflowErrorRunes   = 500
)

func (t *WorkflowTool) Description() string {
	if t == nil || t.listSaved == nil {
		return workflowToolDescription
	}
	saved, err := t.listSaved()
	if len(saved) == 0 && err == nil {
		return workflowToolDescription
	}
	var description strings.Builder
	description.WriteString(workflowToolDescription)
	if len(saved) > 0 {
		description.WriteString("\n\nSaved workflows (run one with name, or from a script with workflow(name, args)):")
		for _, entry := range saved {
			summary := cmp.Or(entry.Meta.WhenToUse, entry.Meta.Description)
			fmt.Fprintf(&description, "\n- %s — %s", entry.Name, TruncateWorkflowText(summary, maxSavedWorkflowSummaryRunes))
		}
	}
	if err != nil {
		// Said here so the model can tell the user why a workflow they saved
		// is missing, rather than the file failing without a word.
		fmt.Fprintf(&description, "\n\nSome saved workflows could not be loaded: %s", TruncateWorkflowText(err.Error(), maxSavedWorkflowErrorRunes))
	}
	return description.String()
}

func (t *WorkflowTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"script": map[string]any{
				"type":        "string",
				"description": "The workflow script's JavaScript source, starting with export const meta = {...}. Give exactly one of script, script_path, or name.",
			},
			"script_path": map[string]any{
				"type":        "string",
				"description": "Path to a .js workflow script; a relative path resolves against the working directory.",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Name of a saved workflow, from .nami/workflows in the project or the workflows directory of the user config.",
			},
			"args": map[string]any{
				"description": "Any JSON value, handed to the script verbatim as args. Pass arrays and objects as JSON values, not as JSON-encoded strings.",
			},
			"resume_from_run_id": map[string]any{
				"type":        "string",
				"description": "run_id of an earlier run in this session. Unchanged agent() calls replay from its journal instead of running again.",
			},
			"token_budget": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"description": "Ceiling on the output tokens the run's agents may spend: once spent, agent() throws and queued agents never start, while agents already running finish. 0 or omitted means no ceiling.",
			},
		},
	}
}

func (t *WorkflowTool) Permission() PermissionLevel                     { return PermissionExecute }
func (t *WorkflowTool) Concurrency(input ToolInput) ConcurrencyDecision { return ConcurrencySerial }

// Validate loads and parses the script, so a syntax or meta error reaches
// the model as a message it can fix instead of as a run that failed.
func (t *WorkflowTool) Validate(input ToolInput) error {
	if t == nil || t.launch == nil {
		return fmt.Errorf("workflow launcher is not configured")
	}
	source, err := workflowSourceFromParams(input.Params)
	if err != nil {
		return err
	}
	if _, err := t.loadScript(source); err != nil {
		return err
	}
	if raw, present := input.Params["resume_from_run_id"]; present {
		runID, _ := raw.(string)
		if !ValidWorkflowRunID(runID) {
			return fmt.Errorf("resume_from_run_id %q is not a workflow run id such as wf_0123456789ab; omit it to start fresh", runID)
		}
	}
	if budget, ok := intParam(input.Params, "token_budget"); ok && budget < 0 {
		return fmt.Errorf("token_budget must be >= 0")
	}
	return nil
}

func (t *WorkflowTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	if t == nil || t.launch == nil {
		return ToolOutput{}, fmt.Errorf("workflow launcher is not configured")
	}
	source, err := workflowSourceFromParams(input.Params)
	if err != nil {
		return ToolOutput{}, err
	}
	script, err := t.loadScript(source)
	if err != nil {
		return ToolOutput{}, err
	}
	args, err := workflowArgs(input)
	if err != nil {
		return ToolOutput{}, err
	}
	resumeFrom, _ := stringParam(input.Params, "resume_from_run_id")
	result, err := t.launch(ctx, WorkflowLaunchRequest{
		Script:          script,
		Args:            args,
		ResumeFromRunID: strings.TrimSpace(resumeFrom),
		TokenBudget:     intOrDefault(input.Params, "token_budget", 0),
	})
	if err != nil {
		return ToolOutput{}, err
	}
	return marshalWorkflowOutput(result, "marshal workflow launch")
}

// PermissionTarget shows what the approval is for: the script's own name and
// description, or where the script comes from when it cannot be read.
func (t *WorkflowTool) PermissionTarget(input ToolInput) PermissionTarget {
	source, err := workflowSourceFromParams(input.Params)
	if err != nil {
		return PermissionTarget{Kind: "workflow"}
	}
	if script, err := t.loadScript(source); err == nil {
		return PermissionTarget{Kind: "workflow", Value: script.Meta.Name + ": " + script.Meta.Description}
	}
	return PermissionTarget{Kind: "workflow", Value: source.String()}
}

// workflowSource is where a workflow call's script comes from. Exactly one
// field is set.
type workflowSource struct {
	script     string
	scriptPath string
	name       string
}

func (s workflowSource) String() string {
	switch {
	case s.name != "":
		return "saved workflow " + s.name
	case s.scriptPath != "":
		return s.scriptPath
	default:
		return TruncateWorkflowText(s.script, maxSavedWorkflowSummaryRunes)
	}
}

func workflowSourceFromParams(params map[string]any) (workflowSource, error) {
	var source workflowSource
	given := 0
	if script, ok := stringParam(params, "script"); ok && strings.TrimSpace(script) != "" {
		// Kept as written, so error line numbers match the script.
		source.script = script
		given++
	}
	if path, ok := stringParam(params, "script_path"); ok && strings.TrimSpace(path) != "" {
		source.scriptPath = strings.TrimSpace(path)
		given++
	}
	if name, ok := stringParam(params, "name"); ok && strings.TrimSpace(name) != "" {
		source.name = strings.TrimSpace(name)
		given++
	}
	switch given {
	case 0:
		return workflowSource{}, fmt.Errorf("workflow requires one of script, script_path, or name")
	case 1:
		return source, nil
	default:
		return workflowSource{}, fmt.Errorf("workflow takes exactly one of script, script_path, or name, but %d were given", given)
	}
}

func (t *WorkflowTool) loadScript(source workflowSource) (workflowpkg.Script, error) {
	if source.script != "" {
		return workflowpkg.ParseScript("workflow.js", source.script)
	}
	if t.load == nil {
		return workflowpkg.Script{}, fmt.Errorf("workflow script loader is not configured")
	}
	return t.load(workflowpkg.Ref{Name: source.name, ScriptPath: source.scriptPath})
}

// workflowArgs returns the args input exactly as the model wrote it. The
// decoded params hold every number as a float64, which would round a large
// integer, so args is read from the raw input when there is one.
func workflowArgs(input ToolInput) (json.RawMessage, error) {
	value, present := input.Params["args"]
	if !present {
		return nil, nil
	}
	if strings.TrimSpace(input.Raw) != "" {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(input.Raw), &raw); err == nil && len(raw["args"]) > 0 {
			return raw["args"], nil
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode workflow args: %w", err)
	}
	return encoded, nil
}

func (t *WorkflowStatusTool) Name() string { return "workflow_status" }

func (t *WorkflowStatusTool) Description() string {
	return "Report a workflow run's state: status, phases, agent counts, each agent's outcome, recent log lines, and the script's result once it has finished. Set wait_ms to wait up to that long for the run to finish before reporting."
}

func (t *WorkflowStatusTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"run_id": map[string]any{"type": "string", "description": "Run identifier returned by workflow."},
			"wait_ms": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"maximum":     MaxWorkflowWaitMs,
				"description": "Optional time to wait for the run to finish before returning. Capped at 600000.",
			},
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
	return validateWorkflowRunRequest("workflow_status", input.Params)
}

func (t *WorkflowStatusTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	runID, _ := stringParam(input.Params, "run_id")
	snapshot, err := t.lookup(ctx, WorkflowStatusRequest{
		RunID:  strings.TrimSpace(runID),
		WaitMs: min(intOrDefault(input.Params, "wait_ms", 0), MaxWorkflowWaitMs),
	})
	if err != nil {
		return ToolOutput{}, err
	}
	return marshalWorkflowOutput(DisplaySafeWorkflowSnapshot(snapshot), "marshal workflow status")
}

func (t *WorkflowStopTool) Name() string { return "workflow_stop" }

func (t *WorkflowStopTool) Description() string {
	return "Stop a workflow run: its agents are cancelled, and the call waits up to wait_ms (default 5000) for the run to settle before returning its state. Stopping a run that has already finished just returns its state."
}

func (t *WorkflowStopTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"run_id": map[string]any{"type": "string", "description": "Run identifier returned by workflow."},
			"wait_ms": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"maximum":     MaxWorkflowWaitMs,
				"description": "Optional time to wait for the run to settle. Defaults to 5000.",
			},
		},
		"required": []string{"run_id"},
	}
}

func (t *WorkflowStopTool) Permission() PermissionLevel                     { return PermissionExecute }
func (t *WorkflowStopTool) Concurrency(input ToolInput) ConcurrencyDecision { return ConcurrencySerial }

func (t *WorkflowStopTool) Validate(input ToolInput) error {
	if t == nil || t.stop == nil {
		return fmt.Errorf("workflow stopper is not configured")
	}
	return validateWorkflowRunRequest("workflow_stop", input.Params)
}

func (t *WorkflowStopTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	runID, _ := stringParam(input.Params, "run_id")
	snapshot, err := t.stop(ctx, WorkflowStopRequest{
		RunID:  strings.TrimSpace(runID),
		WaitMs: min(intOrDefault(input.Params, "wait_ms", defaultWorkflowStopWaitMs), MaxWorkflowWaitMs),
	})
	if err != nil {
		return ToolOutput{}, err
	}
	return marshalWorkflowOutput(DisplaySafeWorkflowSnapshot(snapshot), "marshal workflow stop result")
}

func validateWorkflowRunRequest(toolName string, params map[string]any) error {
	if runID, ok := stringParam(params, "run_id"); !ok || strings.TrimSpace(runID) == "" {
		return fmt.Errorf("%s requires run_id", toolName)
	}
	if value, ok := intParam(params, "wait_ms"); ok && value < 0 {
		return fmt.Errorf("wait_ms must be >= 0")
	}
	return nil
}

func marshalWorkflowOutput(value any, context string) (ToolOutput, error) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return ToolOutput{}, fmt.Errorf("%s: %w", context, err)
	}
	return ToolOutput{Output: string(encoded)}, nil
}

// DisplaySafeWorkflowSnapshot trims a snapshot for the transcript, so a run
// with a thousand agents or a huge result cannot flood the context: at most
// 200 agents, short previews, the last 20 log lines, and the result cut to
// about 20000 characters. What it drops stays in result_path, the journal,
// and the agents' transcripts.
func DisplaySafeWorkflowSnapshot(snapshot WorkflowRunSnapshot) WorkflowRunSnapshot {
	display := snapshot
	display.Phases = slices.Clone(snapshot.Phases)
	display.Warnings = slices.Clone(snapshot.Warnings)
	display.Error = TruncateWorkflowText(snapshot.Error, maxWorkflowDisplayErrorRunes)

	agents := SelectWorkflowAgents(snapshot.Agents, MaxWorkflowDisplayAgents)
	display.Agents = make([]WorkflowAgentSnapshot, 0, len(agents))
	for _, agent := range agents {
		agent.Error = TruncateWorkflowText(agent.Error, WorkflowAgentPreviewRunes)
		agent.OutputPreview = TruncateWorkflowText(agent.OutputPreview, WorkflowAgentPreviewRunes)
		display.Agents = append(display.Agents, agent)
	}
	display.Logs = slices.Clone(snapshot.Logs[max(len(snapshot.Logs)-maxWorkflowDisplayLogs, 0):])

	if len(snapshot.Result) > maxWorkflowDisplayResult {
		// Cut JSON is no longer JSON, so the start of it is shown as a
		// string, with result_truncated saying the value is not whole.
		preview, err := json.Marshal(TruncateWorkflowText(string(snapshot.Result), maxWorkflowDisplayResult))
		if err == nil {
			display.Result = preview
			display.ResultTruncated = true
		}
	}
	return display
}

// SelectWorkflowAgents picks up to limit agents to show, in the order a
// reader wants them: running agents first, then failed ones, then the rest,
// most recent first within each group.
func SelectWorkflowAgents(agents []WorkflowAgentSnapshot, limit int) []WorkflowAgentSnapshot {
	ordered := slices.Clone(agents)
	slices.SortStableFunc(ordered, func(a, b WorkflowAgentSnapshot) int {
		return cmp.Or(
			cmp.Compare(workflowAgentDisplayRank(a.Status), workflowAgentDisplayRank(b.Status)),
			cmp.Compare(b.Index, a.Index),
		)
	})
	return ordered[:min(len(ordered), max(limit, 0))]
}

func workflowAgentDisplayRank(status string) int {
	switch status {
	case string(workflowpkg.AgentRunning):
		return 0
	case string(workflowpkg.AgentFailed):
		return 1
	default:
		return 2
	}
}

// TruncateWorkflowText cuts value to at most maxRunes runes, marking a cut
// with an ellipsis. It reads no further than the cut, so previewing a huge
// output costs no more than the preview.
func TruncateWorkflowText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if maxRunes <= 0 {
		return ""
	}
	seen := 0
	keep := 0
	for index := range value {
		if seen == maxRunes-1 {
			// Where the cut goes if the text turns out to be too long:
			// one rune short, to leave room for the ellipsis.
			keep = index
		}
		if seen == maxRunes {
			return value[:keep] + "…"
		}
		seen++
	}
	return value
}
