package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/dop251/goja"
	"github.com/google/jsonschema-go/jsonschema"
)

const (
	// MaxAgents caps agent() calls over a run's lifetime. It is a backstop for
	// a runaway loop, set far above what a real workflow needs.
	MaxAgents = 1000
	// MaxItems caps the items one parallel() or pipeline() call accepts.
	// Passing more is an error rather than a silent truncation.
	MaxItems = 4096
	// maxCallStackSize turns runaway JavaScript recursion into a catchable
	// RangeError instead of a Go stack overflow, which would take the engine
	// down. It does not bound goja's own Go recursion over a deeply nested or
	// cyclic value, so the hooks check a value's shape with checkValueShape
	// before exporting or printing it.
	maxCallStackSize = 4096
	maxLabelRunes    = 60
	// maxLogRunes caps one log line, so a script that logs whole agent outputs
	// cannot flood the progress display or the run's retained state.
	maxLogRunes = 2000
)

// ErrStopped is returned when a run ends because its context was cancelled.
var ErrStopped = errors.New("workflow stopped")

// errScriptFinished cancels agents still running when the script returns.
var errScriptFinished = errors.New("workflow script finished before this agent")

// DefaultConcurrency is how many agents a run executes at once when the caller
// does not share a slot pool across runs.
func DefaultConcurrency() int {
	return min(max(runtime.NumCPU()-2, 2), 16)
}

// AgentCall is one agent() call, handed to the AgentRunner.
type AgentCall struct {
	// Index is 1-based, in the order the script made its calls.
	Index int
	Label string
	Phase string
	// Workflow names the nested workflow that made the call, or is empty for
	// the top-level script.
	Workflow string
	Prompt   string
	// Schema is the call's JSON Schema in canonical form, or nil when the call
	// returns text.
	Schema    json.RawMessage
	Model     string
	Effort    string
	Isolation string
	AgentType string
}

// AgentResult is what a finished child agent produced.
type AgentResult struct {
	// Text is the agent's final text, for a call without a schema.
	Text string
	// Structured is the agent's output, for a call with a schema. The run
	// validates it against the schema again before the script sees it.
	Structured     json.RawMessage
	AgentID        string
	SessionID      string
	TranscriptPath string
	InputTokens    int
	OutputTokens   int
	CostUSD        float64
}

// AgentRunner runs one child agent to completion. A returned error fails the
// call: the script sees null, and the run records why.
type AgentRunner func(context.Context, AgentCall) (AgentResult, error)

// Ref names a workflow for workflow(): a saved workflow by name, or a script
// file by path.
type Ref struct {
	Name       string
	ScriptPath string
}

// Options configures a run.
type Options struct {
	Script Script
	// Args is the JSON value the script reads as `args`. Nil leaves args
	// undefined.
	Args json.RawMessage
	Run  AgentRunner
	// CheckCall validates a call's options against what the runner supports
	// and may normalize them, for example an agent type's spelling. It runs
	// before the call is keyed for resume, so two spellings of one option
	// replay the same result. A returned error is thrown into the script.
	CheckCall func(*AgentCall) error
	// Load resolves workflow() calls. Nil makes workflow() throw.
	Load func(Ref) (Script, error)
	// Journal records results and replays them from an earlier run. Nil runs
	// every call live and records nothing.
	Journal *Journal
	// Slots bounds how many agents run at once. Runs that share a channel
	// share its capacity. Nil gives the run its own DefaultConcurrency slots.
	Slots chan struct{}
	// TokenBudget caps the output tokens the run's agents may spend. Once it
	// is spent, agent() throws and queued agents fail without starting;
	// agents already running finish, so the total can overshoot by what they
	// spend. Zero means no ceiling.
	TokenBudget int
	// OnEvent is called on every phase change, log line, and agent status
	// change, always from the goroutine running the script.
	OnEvent func(Event)
}

// AgentStatus is where one agent() call stands.
type AgentStatus string

const (
	AgentQueued    AgentStatus = "queued"
	AgentRunning   AgentStatus = "running"
	AgentSucceeded AgentStatus = "succeeded"
	// AgentCached marks a result replayed from an earlier run's journal.
	AgentCached  AgentStatus = "cached"
	AgentFailed  AgentStatus = "failed"
	AgentStopped AgentStatus = "stopped"
)

// AgentRecord is one agent() call and, once it settles, its outcome.
type AgentRecord struct {
	Index          int             `json:"index"`
	Label          string          `json:"label"`
	Phase          string          `json:"phase,omitempty"`
	Workflow       string          `json:"workflow,omitempty"`
	Status         AgentStatus     `json:"status"`
	Error          string          `json:"error,omitempty"`
	Text           string          `json:"text,omitempty"`
	Structured     json.RawMessage `json:"structured,omitempty"`
	AgentID        string          `json:"agent_id,omitempty"`
	SessionID      string          `json:"session_id,omitempty"`
	TranscriptPath string          `json:"transcript_path,omitempty"`
	InputTokens    int             `json:"input_tokens,omitempty"`
	OutputTokens   int             `json:"output_tokens,omitempty"`
	CostUSD        float64         `json:"cost_usd,omitempty"`
	QueuedAt       time.Time       `json:"queued_at,omitzero"`
	StartedAt      time.Time       `json:"started_at,omitzero"`
	CompletedAt    time.Time       `json:"completed_at,omitzero"`
}

// EventKind says what an Event reports.
type EventKind string

const (
	EventPhase EventKind = "phase"
	EventLog   EventKind = "log"
	EventAgent EventKind = "agent"
)

// Event is one progress report from a run.
type Event struct {
	Kind EventKind
	// Phase is the phase that started, for EventPhase.
	Phase    string
	Workflow string
	// Message is the log line, for EventLog.
	Message string
	// Agent is the call whose status changed, for EventAgent.
	Agent AgentRecord
}

// Result is what a run produced. Run returns it even when the run fails, so a
// caller can still report the agents that ran.
type Result struct {
	// Value is the script's return value as JSON, or null when it returned
	// nothing.
	Value    json.RawMessage
	Agents   []AgentRecord
	Warnings []string
}

// Run executes a parsed script until it settles, the context is cancelled, or
// the script fails. It returns only once every agent it started has stopped.
func Run(ctx context.Context, opts Options) (Result, error) {
	if opts.Run == nil {
		return Result{}, errors.New("workflow run requires an agent runner")
	}
	if opts.Script.program == nil {
		return Result{}, errors.New("workflow script has not been parsed")
	}
	r, err := newRun(ctx, opts)
	if err != nil {
		return Result{}, err
	}
	value, runErr := r.execute()
	r.stopRemaining(runErr == nil)
	return Result{Value: value, Agents: r.agents, Warnings: r.warnings}, runErr
}

// run holds one execution's state. Everything except the completions channel,
// the slots, and the wait group is touched only by the goroutine running the
// script, so a call's status changes in exactly one place.
type run struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	opts   Options
	vm     *goja.Runtime
	slots  chan struct{}

	jsonParse     goja.Callable
	jsonStringify goja.Callable
	parallel      goja.Value
	pipeline      goja.Value
	budget        goja.Value

	completions chan completion
	wg          sync.WaitGroup
	// inFlight counts calls whose final completion has not been applied. With
	// none in flight, nothing can settle a promise the script still awaits.
	inFlight int
	pending  map[int]pendingCall

	agents      []AgentRecord
	occurrences map[string]int
	// replayEnded is set once a live result reaches the script. A call made
	// after that may depend on what a re-run agent did, so it runs live too.
	replayEnded bool
	// spentTokens is read by agent goroutines, which must not start once the
	// budget is spent, as well as by the script goroutine.
	spentTokens atomic.Int64
	scriptDone  bool
	warnings    []string
}

type pendingCall struct {
	resolve func(any) error
	schema  *jsonschema.Resolved
	key     string
}

// completion carries a running agent's news back to the script goroutine.
type completion struct {
	index   int
	started bool
	result  AgentResult
	err     error
}

// scope is what one script body sees: the top-level script, or a workflow()
// it nested.
type scope struct {
	workflow string
	depth    int
	phase    string
}

func newRun(ctx context.Context, opts Options) (*run, error) {
	runCtx, cancel := context.WithCancelCause(ctx)
	slots := opts.Slots
	if slots == nil {
		slots = make(chan struct{}, DefaultConcurrency())
	}
	r := &run{
		ctx:    runCtx,
		cancel: cancel,
		opts:   opts,
		vm:     goja.New(),
		slots:  slots,
		// Every call sends at most two completions. With room for all of
		// them, an agent goroutine never blocks on a script busy in its own
		// code while holding a slot that other runs are waiting for.
		completions: make(chan completion, 2*MaxAgents),
		pending:     make(map[int]pendingCall),
		occurrences: make(map[string]int),
	}
	r.vm.SetMaxCallStackSize(maxCallStackSize)
	if err := r.installPrelude(); err != nil {
		cancel(err)
		return nil, err
	}
	return r, nil
}

func (r *run) installPrelude() error {
	jsonObject := r.vm.Get("JSON").ToObject(r.vm)
	parse, ok := goja.AssertFunction(jsonObject.Get("parse"))
	if !ok {
		return errors.New("workflow runtime has no JSON.parse")
	}
	stringify, ok := goja.AssertFunction(jsonObject.Get("stringify"))
	if !ok {
		return errors.New("workflow runtime has no JSON.stringify")
	}
	r.jsonParse, r.jsonStringify = parse, stringify

	factoryValue, err := r.vm.RunString(preludeSource)
	if err != nil {
		return fmt.Errorf("install workflow prelude: %w", err)
	}
	factory, ok := goja.AssertFunction(factoryValue)
	if !ok {
		return errors.New("workflow prelude did not evaluate to a function")
	}
	topLevel := &scope{}
	hooks, err := factory(goja.Undefined(),
		r.vm.ToValue(r.checkItems),
		r.vm.ToValue(r.dropped),
		r.vm.ToValue(r.logHook(topLevel)),
		r.vm.ToValue(MaxItems),
	)
	if err != nil {
		return fmt.Errorf("install workflow prelude: %w", err)
	}
	hookObject := hooks.ToObject(r.vm)
	r.parallel = hookObject.Get("parallel")
	r.pipeline = hookObject.Get("pipeline")
	r.budget = r.newBudget()
	return nil
}

func (r *run) newBudget() goja.Value {
	budget := r.vm.NewObject()
	total := goja.Null()
	if r.opts.TokenBudget > 0 {
		total = r.vm.ToValue(r.opts.TokenBudget)
	}
	_ = budget.Set("total", total)
	_ = budget.Set("spent", func() int64 { return r.spentTokens.Load() })
	_ = budget.Set("remaining", func() float64 {
		if r.opts.TokenBudget <= 0 {
			return math.Inf(1)
		}
		return float64(max(0, int64(r.opts.TokenBudget)-r.spentTokens.Load()))
	})
	return budget
}

func (r *run) execute() (value json.RawMessage, err error) {
	// A bug in a hook must fail this run, not the engine hosting it.
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("workflow runtime failed: %v", recovered)
		}
	}()
	// Interrupt breaks out of a script stuck in a loop that never awaits,
	// which the select below would otherwise never get a chance to see.
	stopInterrupt := context.AfterFunc(r.ctx, func() { r.vm.Interrupt(ErrStopped) })
	defer stopInterrupt()

	args := goja.Undefined()
	if len(r.opts.Args) > 0 {
		args, err = r.parseJSON(r.opts.Args)
		if err != nil {
			return nil, fmt.Errorf("workflow args are not valid JSON: %w", err)
		}
	}
	promise, err := r.start(r.opts.Script, args, &scope{})
	if err != nil {
		return nil, r.scriptFailure(err)
	}
	for promise.State() == goja.PromiseStatePending {
		if r.inFlight == 0 {
			return nil, errors.New("workflow script is waiting on a promise that nothing will settle")
		}
		select {
		case c := <-r.completions:
			if err := r.apply(c); err != nil {
				return nil, r.scriptFailure(err)
			}
		case <-r.ctx.Done():
			return nil, r.stopError()
		}
	}
	if promise.State() == goja.PromiseStateRejected {
		return nil, fmt.Errorf("workflow script failed: %s", describeThrown(r.vm, promise.Result()))
	}
	return r.encodeValue(promise.Result())
}

// start runs a script body with its own hooks and returns the promise the
// body's async function produced.
func (r *run) start(script Script, args goja.Value, s *scope) (*goja.Promise, error) {
	bodyValue, err := r.vm.RunProgram(script.program)
	if err != nil {
		return nil, err
	}
	body, ok := goja.AssertFunction(bodyValue)
	if !ok {
		return nil, errors.New("workflow script did not compile to a function")
	}
	result, err := body(goja.Undefined(),
		r.vm.ToValue(r.agentHook(s)),
		r.parallel,
		r.pipeline,
		r.vm.ToValue(r.phaseHook(s)),
		r.vm.ToValue(r.logHook(s)),
		r.vm.ToValue(r.workflowHook(s)),
		args,
		r.budget,
	)
	if err != nil {
		return nil, err
	}
	promise, ok := result.Export().(*goja.Promise)
	if !ok {
		return nil, errors.New("workflow script body did not return a promise")
	}
	return promise, nil
}

func (r *run) scriptFailure(err error) error {
	if _, ok := errors.AsType[*goja.InterruptedError](err); ok {
		return r.stopError()
	}
	if r.ctx.Err() != nil {
		return r.stopError()
	}
	return fmt.Errorf("workflow script failed: %w", err)
}

// stopReason says why an agent was stopped, in words a reader of the run can
// act on rather than a bare "context canceled".
func (r *run) stopReason() string {
	cause := context.Cause(r.ctx)
	switch {
	case cause == nil, errors.Is(cause, context.Canceled):
		return ErrStopped.Error()
	case errors.Is(cause, context.DeadlineExceeded):
		return "workflow ran past its deadline"
	default:
		return cause.Error()
	}
}

func (r *run) stopError() error {
	cause := context.Cause(r.ctx)
	if cause == nil || errors.Is(cause, context.Canceled) {
		return ErrStopped
	}
	return fmt.Errorf("%w: %w", ErrStopped, cause)
}

func (r *run) encodeValue(value goja.Value) (json.RawMessage, error) {
	if value == nil || goja.IsUndefined(value) {
		return json.RawMessage("null"), nil
	}
	encoded, err := r.jsonStringify(goja.Undefined(), value)
	if err != nil {
		return nil, fmt.Errorf("workflow script returned a value that cannot be encoded as JSON: %w", err)
	}
	if goja.IsUndefined(encoded) {
		return json.RawMessage("null"), nil
	}
	return json.RawMessage(encoded.String()), nil
}

func (r *run) parseJSON(raw []byte) (goja.Value, error) {
	return r.jsonParse(goja.Undefined(), r.vm.ToValue(string(raw)))
}

// agentHook is agent(prompt, opts).
func (r *run) agentHook(s *scope) func(goja.FunctionCall) goja.Value {
	return func(fc goja.FunctionCall) goja.Value {
		call, schema, err := r.buildCall(fc, s)
		if err != nil {
			panic(r.vm.NewTypeError(err.Error()))
		}
		if r.opts.CheckCall != nil {
			if err := r.opts.CheckCall(&call); err != nil {
				panic(r.vm.NewTypeError(fmt.Sprintf("agent(): %v", err)))
			}
		}
		if err := r.admit(); err != nil {
			r.throw(err.Error())
		}
		return r.vm.ToValue(r.launch(call, schema))
	}
}

var agentOptionNames = []string{"label", "phase", "schema", "model", "effort", "isolation", "agentType"}

func (r *run) buildCall(fc goja.FunctionCall, s *scope) (AgentCall, *jsonschema.Resolved, error) {
	prompt, ok := fc.Argument(0).Export().(string)
	if !ok || strings.TrimSpace(prompt) == "" {
		return AgentCall{}, nil, errors.New("agent() needs a non-empty prompt string as its first argument")
	}
	call := AgentCall{Prompt: prompt, Phase: s.phase, Workflow: s.workflow}

	var schema *jsonschema.Resolved
	opts := fc.Argument(1)
	if !goja.IsUndefined(opts) && !goja.IsNull(opts) {
		object, ok := opts.(*goja.Object)
		if !ok {
			return AgentCall{}, nil, errors.New("agent() options must be an object")
		}
		for _, key := range object.Keys() {
			value := object.Get(key)
			if goja.IsUndefined(value) {
				continue
			}
			var err error
			switch key {
			case "label":
				call.Label, err = optionString(key, value)
			case "phase":
				call.Phase, err = optionString(key, value)
			case "model":
				call.Model, err = optionString(key, value)
			case "effort":
				call.Effort, err = optionString(key, value)
			case "agentType":
				call.AgentType, err = optionString(key, value)
			case "isolation":
				call.Isolation, err = optionString(key, value)
				if err == nil && call.Isolation != "worktree" {
					err = fmt.Errorf("agent() option isolation must be 'worktree', got %q", call.Isolation)
				}
			case "schema":
				// Exporting recurses in Go, so the schema's shape is checked
				// first: a cyclic or absurdly deep object would overflow the
				// engine's stack, which no recover can catch.
				if err = checkValueShape(value); err == nil {
					call.Schema, schema, err = compileSchema(value.Export())
				}
			default:
				err = fmt.Errorf("agent() has no option %q; use %s", key, strings.Join(agentOptionNames, ", "))
			}
			if err != nil {
				return AgentCall{}, nil, err
			}
		}
	}
	if call.Label == "" {
		call.Label = labelFromPrompt(prompt)
	}
	return call, schema, nil
}

func optionString(name string, value goja.Value) (string, error) {
	text, ok := value.Export().(string)
	if !ok {
		return "", fmt.Errorf("agent() option %s must be a string", name)
	}
	return strings.TrimSpace(text), nil
}

// labelFromPrompt gives an unlabelled call the start of its prompt's first
// line, which is usually what the call is for.
func labelFromPrompt(prompt string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(prompt), "\n")
	line = strings.Join(strings.Fields(line), " ")
	if utf8.RuneCountInString(line) <= maxLabelRunes {
		return line
	}
	runes := []rune(line)
	return string(runes[:maxLabelRunes-1]) + "…"
}

// admit rejects a call the run can no longer take on.
func (r *run) admit() error {
	switch {
	case r.ctx.Err() != nil:
		return r.stopError()
	case len(r.agents) >= MaxAgents:
		return fmt.Errorf("workflow reached its limit of %d agent() calls", MaxAgents)
	case r.budgetSpent():
		return fmt.Errorf("workflow token budget of %d is spent", r.opts.TokenBudget)
	}
	return nil
}

func (r *run) budgetSpent() bool {
	return r.opts.TokenBudget > 0 && r.spentTokens.Load() >= int64(r.opts.TokenBudget)
}

func (r *run) launch(call AgentCall, schema *jsonschema.Resolved) *goja.Promise {
	call.Index = len(r.agents) + 1
	digest := callDigest(call)
	key := fmt.Sprintf("%s:%d", digest, r.occurrences[digest])
	r.occurrences[digest]++

	r.agents = append(r.agents, AgentRecord{
		Index:    call.Index,
		Label:    call.Label,
		Phase:    call.Phase,
		Workflow: call.Workflow,
		Status:   AgentQueued,
		QueuedAt: time.Now(),
	})
	promise, resolve, _ := r.vm.NewPromise()

	if record, ok := r.replay(key, schema); ok {
		r.settleFromReplay(call.Index, key, record, schema, resolve)
		return promise
	}

	r.pending[call.Index] = pendingCall{resolve: resolve, schema: schema, key: key}
	r.inFlight++
	r.wg.Add(1)
	go r.runAgent(call)
	r.emitAgent(call.Index)
	return promise
}

// replay looks a call up in the journal. Once a live result has reached the
// script, nothing more is replayed.
func (r *run) replay(key string, schema *jsonschema.Resolved) (journalRecord, bool) {
	if r.replayEnded || r.opts.Journal == nil {
		return journalRecord{}, false
	}
	record, ok := r.opts.Journal.Replay(key)
	if !ok {
		return journalRecord{}, false
	}
	// A record that no longer fits the call's schema is a miss, not a result
	// the script would then trip over.
	if schema != nil && validateStructured(schema, record.Structured) != nil {
		return journalRecord{}, false
	}
	return record, true
}

func (r *run) settleFromReplay(index int, key string, record journalRecord, schema *jsonschema.Resolved, resolve func(any) error) {
	agent := &r.agents[index-1]
	agent.Status = AgentCached
	agent.Text = record.Text
	agent.Structured = record.Structured
	agent.AgentID = record.AgentID
	agent.SessionID = record.SessionID
	agent.TranscriptPath = record.TranscriptPath
	agent.CompletedAt = time.Now()
	r.recordJournal(key, record)
	r.emitAgent(index)

	value, err := r.resultValue(record.Text, record.Structured, schema != nil)
	if err != nil {
		panic(r.vm.NewGoError(err))
	}
	if err := resolve(value); err != nil {
		panic(err)
	}
}

func (r *run) runAgent(call AgentCall) {
	defer r.wg.Done()
	select {
	case r.slots <- struct{}{}:
	case <-r.ctx.Done():
		r.completions <- completion{index: call.Index, err: context.Cause(r.ctx)}
		return
	}
	// Both cases of the select can be ready at once; a stopped run must not
	// start an agent just because a slot freed up at the same moment.
	if r.ctx.Err() != nil {
		<-r.slots
		r.completions <- completion{index: call.Index, err: context.Cause(r.ctx)}
		return
	}
	// The budget is checked again here, not only when agent() was called: a
	// fan-out admits every call before any agent has spent anything, and the
	// ones still queued must not start once the budget is gone.
	if r.budgetSpent() {
		<-r.slots
		r.completions <- completion{index: call.Index, err: fmt.Errorf("workflow token budget of %d was spent before this agent started", r.opts.TokenBudget)}
		return
	}
	r.completions <- completion{index: call.Index, started: true}
	result, err := r.opts.Run(r.ctx, call)
	// Counted before the slot is released, so the agent that takes the slot
	// next sees this spend when it checks the budget.
	r.spentTokens.Add(int64(result.OutputTokens))
	<-r.slots
	r.completions <- completion{index: call.Index, result: result, err: err}
}

// apply records a completion and, while the script is running, settles the
// promise its agent() call returned.
func (r *run) apply(c completion) error {
	agent := &r.agents[c.index-1]
	if c.started {
		agent.Status = AgentRunning
		agent.StartedAt = time.Now()
		r.emitAgent(c.index)
		return nil
	}

	r.inFlight--
	pending := r.pending[c.index]
	delete(r.pending, c.index)

	agent.CompletedAt = time.Now()
	agent.AgentID = c.result.AgentID
	agent.SessionID = c.result.SessionID
	agent.TranscriptPath = c.result.TranscriptPath
	agent.InputTokens = c.result.InputTokens
	agent.OutputTokens = c.result.OutputTokens
	agent.CostUSD = c.result.CostUSD

	switch {
	case c.err != nil && r.ctx.Err() != nil:
		agent.Status = AgentStopped
		agent.Error = r.stopReason()
	case c.err != nil:
		agent.Status = AgentFailed
		agent.Error = c.err.Error()
	default:
		r.acceptResult(agent, pending, c.result)
	}
	r.emitAgent(c.index)

	if r.scriptDone {
		return nil
	}
	r.replayEnded = true
	value := goja.Null()
	if agent.Status == AgentSucceeded {
		var err error
		value, err = r.resultValue(agent.Text, agent.Structured, pending.schema != nil)
		if err != nil {
			// The record already says succeeded; the script still gets null,
			// and the reason is kept where a reader of the run will see it.
			r.warn(fmt.Sprintf("agent %d (%s) returned output the script could not read: %v", agent.Index, agent.Label, err))
			value = goja.Null()
		}
	}
	return pending.resolve(value)
}

// acceptResult checks a finished agent's output against its call's schema and
// journals it when it is usable. Failures are never journaled, so a resumed
// run tries them again.
func (r *run) acceptResult(agent *AgentRecord, pending pendingCall, result AgentResult) {
	if pending.schema != nil {
		if err := validateStructured(pending.schema, result.Structured); err != nil {
			agent.Status = AgentFailed
			agent.Error = fmt.Sprintf("agent output does not match its schema: %v", err)
			return
		}
		agent.Structured = result.Structured
	} else {
		agent.Text = result.Text
	}
	agent.Status = AgentSucceeded
	r.recordJournal(pending.key, journalRecord{
		Label:          agent.Label,
		Text:           agent.Text,
		Structured:     agent.Structured,
		AgentID:        agent.AgentID,
		SessionID:      agent.SessionID,
		TranscriptPath: agent.TranscriptPath,
	})
}

func (r *run) resultValue(text string, structured json.RawMessage, hasSchema bool) (goja.Value, error) {
	if !hasSchema {
		return r.vm.ToValue(text), nil
	}
	value, err := r.parseJSON(structured)
	if err != nil {
		return nil, fmt.Errorf("decode structured agent output: %w", err)
	}
	return value, nil
}

func (r *run) recordJournal(key string, record journalRecord) {
	if r.opts.Journal == nil {
		return
	}
	record.Key = key
	if err := r.opts.Journal.Record(record); err != nil {
		r.warn(fmt.Sprintf("The journal is missing a result, so resuming this run would run that agent again: %v", err))
	}
}

// stopRemaining cancels agents still running once the script is done and
// waits for them, so a run never leaves orphaned children behind.
func (r *run) stopRemaining(scriptSucceeded bool) {
	r.scriptDone = true
	if scriptSucceeded && r.inFlight > 0 {
		r.warn(fmt.Sprintf("%d agent() call(s) were still running when the script returned and were stopped; await every call whose result matters.", r.inFlight))
	}
	r.cancel(errScriptFinished)

	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	for {
		select {
		case c := <-r.completions:
			_ = r.apply(c)
		case <-done:
			// Every sender has finished, but completions is buffered, so
			// some may still be waiting to be applied.
			for {
				select {
				case c := <-r.completions:
					_ = r.apply(c)
				default:
					return
				}
			}
		}
	}
}

func (r *run) emitAgent(index int) {
	if r.opts.OnEvent == nil {
		return
	}
	r.opts.OnEvent(Event{Kind: EventAgent, Workflow: r.agents[index-1].Workflow, Agent: r.agents[index-1]})
}

func (r *run) warn(message string) {
	for _, existing := range r.warnings {
		if existing == message {
			return
		}
	}
	r.warnings = append(r.warnings, message)
}

// phaseHook is phase(title).
func (r *run) phaseHook(s *scope) func(goja.FunctionCall) goja.Value {
	return func(fc goja.FunctionCall) goja.Value {
		title, ok := fc.Argument(0).Export().(string)
		if !ok || strings.TrimSpace(title) == "" {
			panic(r.vm.NewTypeError("phase() needs a non-empty title string"))
		}
		s.phase = strings.TrimSpace(title)
		if r.opts.OnEvent != nil {
			r.opts.OnEvent(Event{Kind: EventPhase, Phase: s.phase, Workflow: s.workflow})
		}
		return goja.Undefined()
	}
}

// logHook is log(...values), and console.log for the top-level script.
func (r *run) logHook(s *scope) func(goja.FunctionCall) goja.Value {
	return func(fc goja.FunctionCall) goja.Value {
		parts := make([]string, 0, len(fc.Arguments))
		for _, argument := range fc.Arguments {
			// Printing an object recurses in Go; see checkValueShape.
			if err := checkValueShape(argument); err != nil {
				parts = append(parts, "[unprintable: "+err.Error()+"]")
				continue
			}
			parts = append(parts, argument.String())
		}
		if r.opts.OnEvent != nil {
			r.opts.OnEvent(Event{Kind: EventLog, Message: truncateRunes(strings.Join(parts, " "), maxLogRunes), Workflow: s.workflow})
		}
		return goja.Undefined()
	}
}

// workflowHook is workflow(nameOrRef, args). The nested script shares this
// run's slots, agent cap, journal, budget, and stop signal, but gets its own
// phase and args.
func (r *run) workflowHook(s *scope) func(goja.FunctionCall) goja.Value {
	return func(fc goja.FunctionCall) goja.Value {
		if s.depth >= 1 {
			r.throw("workflow() can only nest one level: a nested workflow cannot call workflow()")
		}
		if r.opts.Load == nil {
			r.throw("workflow() is not available in this run")
		}
		ref, err := refFromValue(fc.Argument(0))
		if err != nil {
			panic(r.vm.NewTypeError(err.Error()))
		}
		script, err := r.opts.Load(ref)
		if err != nil {
			r.throw(fmt.Sprintf("workflow(): %v", err))
		}
		child := &scope{workflow: script.Meta.Name, depth: s.depth + 1}
		promise, err := r.start(script, fc.Argument(1), child)
		if err != nil {
			if _, ok := errors.AsType[*goja.InterruptedError](err); ok {
				panic(err)
			}
			r.throw(fmt.Sprintf("workflow %q failed to start: %v", script.Meta.Name, err))
		}
		return r.vm.ToValue(promise)
	}
}

func refFromValue(value goja.Value) (Ref, error) {
	if name, ok := value.Export().(string); ok && strings.TrimSpace(name) != "" {
		return Ref{Name: strings.TrimSpace(name)}, nil
	}
	if object, ok := value.(*goja.Object); ok {
		if path, ok := object.Get("scriptPath").Export().(string); ok && strings.TrimSpace(path) != "" {
			return Ref{ScriptPath: strings.TrimSpace(path)}, nil
		}
	}
	return Ref{}, errors.New("workflow() needs a saved workflow name or {scriptPath: '...'}")
}

// checkItems guards parallel() and pipeline() against oversized inputs.
func (r *run) checkItems(name string, count int) {
	if count > MaxItems {
		panic(r.vm.NewTypeError(fmt.Sprintf("%s() accepts at most %d items, got %d", name, MaxItems, count)))
	}
}

// dropped reports an item that parallel() or pipeline() turned into null
// because its callback threw. An agent that fails resolves to null without
// throwing and is already visible as a failed agent, so this only fires for
// errors in the script's own code.
func (r *run) dropped(name string, index int, thrown goja.Value) {
	if r.opts.OnEvent == nil {
		return
	}
	r.opts.OnEvent(Event{Kind: EventLog, Message: fmt.Sprintf("%s(): item %d threw and became null: %s", name, index, describeThrown(r.vm, thrown))})
}

// throw raises a plain JavaScript Error, which the script can catch.
func (r *run) throw(message string) {
	errorConstructor := r.vm.Get("Error")
	object, err := r.vm.New(errorConstructor, r.vm.ToValue(message))
	if err != nil {
		panic(r.vm.NewGoError(errors.New(message)))
	}
	panic(object)
}

// describeThrown renders a thrown JavaScript value, preferring its stack so a
// failure names the line that caused it.
func describeThrown(vm *goja.Runtime, thrown goja.Value) string {
	if thrown == nil || goja.IsUndefined(thrown) || goja.IsNull(thrown) {
		return "the script threw " + fmt.Sprint(thrown)
	}
	if object, ok := thrown.(*goja.Object); ok {
		if stack := object.Get("stack"); stack != nil && !goja.IsUndefined(stack) {
			if text := strings.TrimSpace(stack.String()); text != "" {
				return text
			}
		}
	}
	return thrown.String()
}

const (
	// maxValueDepth and maxValueNodes bound the values a hook exports or
	// prints. Real schemas and log arguments are far smaller.
	maxValueDepth = 64
	maxValueNodes = 100_000
)

// checkValueShape walks a value without recursing, and rejects one that is
// nested deeper than maxValueDepth (which includes any cycle) or holds more
// than maxValueNodes values. goja's Export and String recurse in Go with no
// limit of their own, and a Go stack overflow is fatal to the whole engine.
func checkValueShape(value goja.Value) error {
	type entry struct {
		object *goja.Object
		depth  int
	}
	root, ok := value.(*goja.Object)
	if !ok {
		return nil
	}
	stack := []entry{{object: root, depth: 1}}
	nodes := 0
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current.depth > maxValueDepth {
			return fmt.Errorf("value is nested more than %d levels deep (or refers to itself)", maxValueDepth)
		}
		for _, key := range current.object.Keys() {
			nodes++
			if nodes > maxValueNodes {
				return fmt.Errorf("value holds more than %d entries", maxValueNodes)
			}
			if child, ok := current.object.Get(key).(*goja.Object); ok {
				stack = append(stack, entry{object: child, depth: current.depth + 1})
			}
		}
	}
	return nil
}

func truncateRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit-1]) + "…"
}
