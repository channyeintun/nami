package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testMeta = "export const meta = { name: 'test', description: 'test' }\n"

func mustParse(t *testing.T, body string) Script {
	t.Helper()
	script, err := ParseScript("test.js", testMeta+body)
	if err != nil {
		t.Fatalf("ParseScript() error = %v", err)
	}
	return script
}

// fakeRunner records every call and answers with respond, which defaults to
// echoing the prompt.
type fakeRunner struct {
	mu      sync.Mutex
	calls   []AgentCall
	respond func(context.Context, AgentCall) (AgentResult, error)
}

func (f *fakeRunner) run(ctx context.Context, call AgentCall) (AgentResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
	if f.respond == nil {
		return AgentResult{Text: "done: " + call.Prompt, OutputTokens: 1}, nil
	}
	return f.respond(ctx, call)
}

func (f *fakeRunner) prompts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	prompts := make([]string, 0, len(f.calls))
	for _, call := range f.calls {
		prompts = append(prompts, call.Prompt)
	}
	return prompts
}

func runBody(t *testing.T, body string, runner *fakeRunner, configure ...func(*Options)) (Result, error) {
	t.Helper()
	opts := Options{Script: mustParse(t, body), Run: runner.run}
	for _, apply := range configure {
		apply(&opts)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return Run(ctx, opts)
}

func wantValue(t *testing.T, result Result, want string) {
	t.Helper()
	if string(result.Value) != want {
		t.Fatalf("value = %s, want %s", result.Value, want)
	}
}

func TestRunReturnsAnAgentsText(t *testing.T) {
	result, err := runBody(t, "return await agent('hello')", &fakeRunner{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantValue(t, result, `"done: hello"`)
	if len(result.Agents) != 1 || result.Agents[0].Status != AgentSucceeded || result.Agents[0].Text != "done: hello" {
		t.Fatalf("agents = %+v", result.Agents)
	}
}

func TestRunReturnsNullWhenTheScriptReturnsNothing(t *testing.T) {
	result, err := runBody(t, "await agent('x')", &fakeRunner{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantValue(t, result, "null")
}

func TestRunHandsStructuredOutputToTheScript(t *testing.T) {
	runner := &fakeRunner{respond: func(_ context.Context, call AgentCall) (AgentResult, error) {
		return AgentResult{Structured: json.RawMessage(`{"findings":["a","b"]}`)}, nil
	}}
	result, err := runBody(t, `
const SCHEMA = { type: 'object', required: ['findings'], properties: { findings: { type: 'array', items: { type: 'string' } } } }
const review = await agent('review', { schema: SCHEMA })
return review.findings.length
`, runner)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantValue(t, result, "2")
	// Canonical: keys sorted, so equal schemas key the same journal entries.
	want := `{"properties":{"findings":{"items":{"type":"string"},"type":"array"}},"required":["findings"],"type":"object"}`
	if got := string(runner.calls[0].Schema); got != want {
		t.Fatalf("schema = %s, want %s", got, want)
	}
}

// An empty array satisfies a required array property in JSON Schema. "No
// findings" is the most common honest answer, so it must not be rejected.
func TestRunAcceptsAnEmptyRequiredArray(t *testing.T) {
	runner := &fakeRunner{respond: func(context.Context, AgentCall) (AgentResult, error) {
		return AgentResult{Structured: json.RawMessage(`{"findings":[]}`)}, nil
	}}
	result, err := runBody(t, `
const r = await agent('review', { schema: { type: 'object', required: ['findings'], properties: { findings: { type: 'array' } } } })
return r.findings
`, runner)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantValue(t, result, "[]")
}

func TestRunFailsAnAgentWhoseOutputMissesItsSchema(t *testing.T) {
	runner := &fakeRunner{respond: func(context.Context, AgentCall) (AgentResult, error) {
		return AgentResult{Structured: json.RawMessage(`{"wrong":1}`)}, nil
	}}
	result, err := runBody(t, `
const r = await agent('review', { schema: { type: 'object', required: ['findings'], properties: { findings: { type: 'array' } } } })
return r === null
`, runner)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantValue(t, result, "true")
	if result.Agents[0].Status != AgentFailed || !strings.Contains(result.Agents[0].Error, "schema") {
		t.Fatalf("agent = %+v, want a schema failure", result.Agents[0])
	}
}

func TestRunTurnsAFailedAgentIntoNull(t *testing.T) {
	runner := &fakeRunner{respond: func(context.Context, AgentCall) (AgentResult, error) {
		return AgentResult{}, errors.New("provider exploded")
	}}
	result, err := runBody(t, "const r = await agent('x')\nreturn r === null", runner)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantValue(t, result, "true")
	if result.Agents[0].Status != AgentFailed || result.Agents[0].Error != "provider exploded" {
		t.Fatalf("agent = %+v", result.Agents[0])
	}
}

func TestParallelNeverRunsMoreAgentsThanItHasSlots(t *testing.T) {
	var running, peak atomic.Int64
	runner := &fakeRunner{respond: func(_ context.Context, call AgentCall) (AgentResult, error) {
		now := running.Add(1)
		for {
			seen := peak.Load()
			if now <= seen || peak.CompareAndSwap(seen, now) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		running.Add(-1)
		return AgentResult{Text: call.Prompt}, nil
	}}
	result, err := runBody(t, "return (await parallel([1,2,3,4,5,6].map(n => () => agent('task ' + n)))).length", runner, func(opts *Options) {
		opts.Slots = make(chan struct{}, 2)
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantValue(t, result, "6")
	if peak.Load() != 2 {
		t.Fatalf("peak concurrency = %d, want 2", peak.Load())
	}
}

// pipeline must stream each item through its stages: a fast item reaches
// stage two while a slow one is still in stage one.
func TestPipelineHasNoBarrierBetweenStages(t *testing.T) {
	fastReachedStageTwo := make(chan struct{})
	runner := &fakeRunner{respond: func(_ context.Context, call AgentCall) (AgentResult, error) {
		switch call.Prompt {
		case "one slow":
			select {
			case <-fastReachedStageTwo:
			case <-time.After(5 * time.Second):
				return AgentResult{}, errors.New("stage two never started for the fast item")
			}
		case "two fast":
			close(fastReachedStageTwo)
		}
		return AgentResult{Text: call.Prompt}, nil
	}}
	result, err := runBody(t, `
return await pipeline(['slow', 'fast'],
  item => agent('one ' + item),
  (previous, item) => agent('two ' + item))
`, runner)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantValue(t, result, `["two slow","two fast"]`)
}

func TestParallelTurnsAThrowingTaskIntoNullAndSaysSo(t *testing.T) {
	var logs []string
	result, err := runBody(t, "return await parallel([() => { throw new Error('boom') }, () => agent('ok')])", &fakeRunner{}, func(opts *Options) {
		opts.OnEvent = func(event Event) {
			if event.Kind == EventLog {
				logs = append(logs, event.Message)
			}
		}
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantValue(t, result, `[null,"done: ok"]`)
	if len(logs) != 1 || !strings.Contains(logs[0], "boom") {
		t.Fatalf("logs = %q, want one line naming the error", logs)
	}
}

func TestPipelineDropsAnItemWhoseStageThrows(t *testing.T) {
	result, err := runBody(t, `
return await pipeline([1, 2],
  n => { if (n === 1) throw new Error('bad item'); return n },
  n => n * 10)
`, &fakeRunner{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantValue(t, result, "[null,20]")
}

// Resume matches calls by their prompts, so anything that lets a script vary
// between runs on its own would make every replay miss.
func TestScriptCannotReadTheClockOrRandomness(t *testing.T) {
	for _, expression := range []string{"Date.now()", "Math.random()", "new Date()", "Date()"} {
		t.Run(expression, func(t *testing.T) {
			_, err := runBody(t, "return "+expression, &fakeRunner{})
			if err == nil || !strings.Contains(err.Error(), "unavailable in workflow scripts") {
				t.Fatalf("Run() error = %v, want the expression to be unavailable", err)
			}
		})
	}
	result, err := runBody(t, "const d = new Date(0)\nreturn [d.getTime(), d instanceof Date, Date.UTC(1970, 0, 1)]", &fakeRunner{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantValue(t, result, "[0,true,0]")
}

func TestRunReportsWhereAScriptThrew(t *testing.T) {
	_, err := runBody(t, "const x = 1\nthrow new Error('broke here')", &fakeRunner{})
	if err == nil {
		t.Fatal("Run() succeeded, want the thrown error")
	}
	if !strings.Contains(err.Error(), "broke here") || !strings.Contains(err.Error(), "test.js:3") {
		t.Fatalf("error = %q, want the message and test.js:3", err)
	}
}

func TestRunFailsAScriptWaitingOnAPromiseThatNeverSettles(t *testing.T) {
	_, err := runBody(t, "await new Promise(() => {})", &fakeRunner{})
	if err == nil || !strings.Contains(err.Error(), "nothing will settle") {
		t.Fatalf("Run() error = %v, want a stuck-promise error", err)
	}
}

func TestCancellingARunStopsItsRunningAgents(t *testing.T) {
	started := make(chan struct{})
	sawCancel := make(chan struct{})
	runner := &fakeRunner{respond: func(ctx context.Context, call AgentCall) (AgentResult, error) {
		close(started)
		<-ctx.Done()
		close(sawCancel)
		return AgentResult{}, ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	result, err := Run(ctx, Options{Script: mustParse(t, "return await agent('long')"), Run: runner.run})
	if !errors.Is(err, ErrStopped) {
		t.Fatalf("Run() error = %v, want ErrStopped", err)
	}
	select {
	case <-sawCancel:
	default:
		t.Fatal("Run returned before its agent saw the cancellation")
	}
	if result.Agents[0].Status != AgentStopped {
		t.Fatalf("agent status = %s, want stopped", result.Agents[0].Status)
	}
}

func TestCancellingARunInterruptsAScriptThatNeverAwaits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, Options{Script: mustParse(t, "while (true) {}"), Run: (&fakeRunner{}).run})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrStopped) {
			t.Fatalf("Run() error = %v, want ErrStopped", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop a script stuck in a loop")
	}
}

// A script that returns without awaiting an agent must not leave it running.
func TestRunStopsAgentsTheScriptDidNotAwait(t *testing.T) {
	forgottenStarted := make(chan struct{})
	var finished atomic.Bool
	runner := &fakeRunner{respond: func(ctx context.Context, call AgentCall) (AgentResult, error) {
		if call.Prompt == "quick" {
			<-forgottenStarted
			return AgentResult{Text: "quick"}, nil
		}
		close(forgottenStarted)
		<-ctx.Done()
		finished.Store(true)
		return AgentResult{}, ctx.Err()
	}}
	result, err := runBody(t, "agent('forgotten')\nawait agent('quick')\nreturn 1", runner)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !finished.Load() {
		t.Fatal("Run returned while an agent it started was still running")
	}
	if result.Agents[0].Status != AgentStopped {
		t.Fatalf("agent status = %s, want stopped", result.Agents[0].Status)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "still running") {
		t.Fatalf("warnings = %q", result.Warnings)
	}
}

func TestAgentRejectsBadOptions(t *testing.T) {
	cases := map[string]string{
		"unknown option":   "agent('x', { schemas: {} })",
		"bad isolation":    "agent('x', { isolation: 'container' })",
		"non-object root":  "agent('x', { schema: { type: 'array' } })",
		"undeclared field": "agent('x', { schema: { type: 'object', properties: {}, required: ['a'] } })",
		"empty prompt":     "agent('  ')",
		"non-string label": "agent('x', { label: 3 })",
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			runner := &fakeRunner{}
			_, err := runBody(t, "await "+call, runner)
			if err == nil {
				t.Fatal("Run() succeeded, want the bad call to throw")
			}
			if len(runner.calls) != 0 {
				t.Fatalf("runner ran %d agents for a rejected call", len(runner.calls))
			}
		})
	}
}

func TestCheckCallNormalizesOptionsAndRejectsCalls(t *testing.T) {
	runner := &fakeRunner{}
	check := func(call *AgentCall) error {
		if call.AgentType == "missing" {
			return fmt.Errorf("unknown agent type %q", call.AgentType)
		}
		if call.AgentType == "" {
			call.AgentType = "general-purpose"
		}
		return nil
	}
	_, err := runBody(t, "await agent('x')", runner, func(opts *Options) { opts.CheckCall = check })
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if runner.calls[0].AgentType != "general-purpose" {
		t.Fatalf("agent type = %q, want the normalized default", runner.calls[0].AgentType)
	}
	_, err = runBody(t, "await agent('x', { agentType: 'missing' })", runner, func(opts *Options) { opts.CheckCall = check })
	if err == nil || !strings.Contains(err.Error(), `unknown agent type "missing"`) {
		t.Fatalf("Run() error = %v, want CheckCall's error", err)
	}
}

func TestAgentsAreLabelledAndGroupedByPhase(t *testing.T) {
	var phases []string
	result, err := runBody(t, `
phase('Find')
await agent('first line\nsecond line')
await agent('x', { label: 'custom', phase: 'Other' })
`, &fakeRunner{}, func(opts *Options) {
		opts.OnEvent = func(event Event) {
			if event.Kind == EventPhase {
				phases = append(phases, event.Phase)
			}
		}
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := result.Agents[0]; got.Label != "first line" || got.Phase != "Find" {
		t.Fatalf("first agent = %+v", got)
	}
	if got := result.Agents[1]; got.Label != "custom" || got.Phase != "Other" {
		t.Fatalf("second agent = %+v", got)
	}
	if len(phases) != 1 || phases[0] != "Find" {
		t.Fatalf("phase events = %q", phases)
	}
}

func TestNestedWorkflowGetsItsOwnPhaseAndArgs(t *testing.T) {
	child, err := ParseScript("child.js", "export const meta = { name: 'child', description: 'c' }\nphase('Inner')\nreturn await agent('child ' + args.n)")
	if err != nil {
		t.Fatalf("ParseScript() error = %v", err)
	}
	load := func(ref Ref) (Script, error) {
		if ref.Name != "child" {
			return Script{}, fmt.Errorf("no workflow named %q", ref.Name)
		}
		return child, nil
	}
	result, err := runBody(t, `
phase('Outer')
const fromChild = await workflow('child', { n: 7 })
await agent('parent')
return fromChild
`, &fakeRunner{}, func(opts *Options) { opts.Load = load })
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantValue(t, result, `"done: child 7"`)
	if got := result.Agents[0]; got.Workflow != "child" || got.Phase != "Inner" {
		t.Fatalf("child agent = %+v", got)
	}
	if got := result.Agents[1]; got.Workflow != "" || got.Phase != "Outer" {
		t.Fatalf("parent agent = %+v, want the parent's phase untouched", got)
	}
}

func TestNestedWorkflowsCannotNestFurther(t *testing.T) {
	load := func(Ref) (Script, error) {
		return ParseScript("again.js", "export const meta = { name: 'again', description: 'a' }\nreturn await workflow('again')")
	}
	_, err := runBody(t, "return await workflow('again')", &fakeRunner{}, func(opts *Options) { opts.Load = load })
	if err == nil || !strings.Contains(err.Error(), "one level") {
		t.Fatalf("Run() error = %v, want the nesting limit", err)
	}
}

func TestBudgetStopsNewAgentsOnceSpent(t *testing.T) {
	runner := &fakeRunner{respond: func(context.Context, AgentCall) (AgentResult, error) {
		return AgentResult{Text: "x", OutputTokens: 6}, nil
	}}
	result, err := runBody(t, `
let n = 0
while (budget.total && budget.remaining() > 0) { await agent('round ' + n++) }
let threw = ''
try { await agent('one more') } catch (e) { threw = e.message }
return [n, budget.spent(), threw]
`, runner, func(opts *Options) { opts.TokenBudget = 10 })
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantValue(t, result, `[2,12,"workflow token budget of 10 is spent"]`)
}

func TestBudgetIsUnlimitedWithoutATarget(t *testing.T) {
	result, err := runBody(t, "return [budget.total, budget.remaining() === Infinity]", &fakeRunner{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantValue(t, result, "[null,true]")
}

func TestArgsReachTheScriptAsJavaScriptValues(t *testing.T) {
	result, err := runBody(t, "return args.files.map(f => f.toUpperCase())", &fakeRunner{}, func(opts *Options) {
		opts.Args = json.RawMessage(`{"files":["a.go","b.go"]}`)
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantValue(t, result, `["A.GO","B.GO"]`)
}

func TestParallelRejectsMoreItemsThanItAccepts(t *testing.T) {
	_, err := runBody(t, "await parallel(new Array(4097).fill(() => 1))", &fakeRunner{})
	if err == nil || !strings.Contains(err.Error(), "4096") {
		t.Fatalf("Run() error = %v, want the item limit", err)
	}
}

func TestRunCapsAgentCalls(t *testing.T) {
	_, err := runBody(t, "for (let i = 0; i <= 1000; i++) { await agent('n' + i) }", &fakeRunner{})
	if err == nil || !strings.Contains(err.Error(), "limit of 1000") {
		t.Fatalf("Run() error = %v, want the agent cap", err)
	}
}

// Deep recursion has to surface as a script error, not a Go stack overflow
// that kills the engine.
func TestRunSurvivesRunawayRecursion(t *testing.T) {
	_, err := runBody(t, "const f = n => f(n + 1)\nf(0)", &fakeRunner{})
	if err == nil {
		t.Fatal("Run() succeeded, want a stack error")
	}
}

func TestScriptCannotReachTheHost(t *testing.T) {
	result, err := runBody(t, "return [typeof require, typeof process, typeof fetch, typeof setTimeout]", &fakeRunner{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	wantValue(t, result, `["undefined","undefined","undefined","undefined"]`)
}

func TestConsoleLogBecomesALogEvent(t *testing.T) {
	var logs []string
	_, err := runBody(t, "console.log('hello', 3)\nlog('direct')", &fakeRunner{}, func(opts *Options) {
		opts.OnEvent = func(event Event) {
			if event.Kind == EventLog {
				logs = append(logs, event.Message)
			}
		}
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if strings.Join(logs, "|") != "hello 3|direct" {
		t.Fatalf("logs = %q", logs)
	}
}

// The validator follows $ref without a cycle check, and a Go stack overflow
// kills the engine, so a schema that refers to itself must be refused before
// anything resolves or validates it.
func TestAgentRefusesSchemasWithReferences(t *testing.T) {
	cases := map[string]string{
		"defs cycle": "{ type: 'object', properties: { items: { type: 'array', items: { $ref: '#/$defs/f' } } }, $defs: { f: { anyOf: [{ $ref: '#/$defs/f' }] } } }",
		"root allOf": "{ type: 'object', properties: {}, allOf: [{ $ref: '#' }] }",
		"double not": "{ type: 'object', properties: {}, not: { not: { $ref: '#' } } }",
	}
	for name, schema := range cases {
		t.Run(name, func(t *testing.T) {
			runner := &fakeRunner{}
			_, err := runBody(t, "await agent('x', { schema: "+schema+" })", runner)
			if err == nil || !strings.Contains(err.Error(), "$") {
				t.Fatalf("Run() error = %v, want the reference refused", err)
			}
			if len(runner.calls) != 0 {
				t.Fatal("the runner ran an agent with a self-referencing schema")
			}
		})
	}
}

// goja's Export and String recurse in Go without a limit, so a cyclic or
// absurdly deep value handed to a hook has to be refused, not exported.
func TestHooksRefuseValuesTheyCannotSafelyRead(t *testing.T) {
	_, err := runBody(t, "const s = { type: 'object', properties: {} }\ns.properties.self = s\nawait agent('x', { schema: s })", &fakeRunner{})
	if err == nil || !strings.Contains(err.Error(), "nested more than") {
		t.Fatalf("Run() error = %v, want a cyclic schema refused", err)
	}

	var logs []string
	_, err = runBody(t, "let a = []\nfor (let i = 0; i < 200; i++) a = [a]\nconst o = {}\no.self = o\nlog('deep', a)\nlog(o)", &fakeRunner{}, func(opts *Options) {
		opts.OnEvent = func(event Event) {
			if event.Kind == EventLog {
				logs = append(logs, event.Message)
			}
		}
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(logs) != 2 || !strings.HasPrefix(logs[0], "deep [unprintable") || !strings.HasPrefix(logs[1], "[unprintable") {
		t.Fatalf("logs = %q, want both values reported as unprintable", logs)
	}
}

func TestLogLinesAreCapped(t *testing.T) {
	var logs []string
	_, err := runBody(t, "log('x'.repeat(100000))", &fakeRunner{}, func(opts *Options) {
		opts.OnEvent = func(event Event) {
			if event.Kind == EventLog {
				logs = append(logs, event.Message)
			}
		}
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(logs) != 1 || len([]rune(logs[0])) != maxLogRunes {
		t.Fatalf("log line has %d runes, want %d", len([]rune(logs[0])), maxLogRunes)
	}
}

// A fan-out admits every call before any agent has spent anything. The
// budget still has to stop the calls that are queued when it runs out.
func TestQueuedAgentsDoNotStartOnceTheBudgetIsSpent(t *testing.T) {
	var runs atomic.Int64
	runner := &fakeRunner{respond: func(context.Context, AgentCall) (AgentResult, error) {
		runs.Add(1)
		return AgentResult{Text: "x", OutputTokens: 100}, nil
	}}
	result, err := runBody(t, "const out = await parallel(Array.from({ length: 20 }, (_, i) => () => agent('task ' + i)))\nreturn out.filter(Boolean).length", runner, func(opts *Options) {
		opts.TokenBudget = 150
		opts.Slots = make(chan struct{}, 2)
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// Two agents start before anything is spent, and at most one more can
	// start while the spend is still under the budget.
	if got := runs.Load(); got > 3 {
		t.Fatalf("%d agents ran with a budget that two of them exhaust", got)
	}
	if string(result.Value) != fmt.Sprint(runs.Load()) {
		t.Fatalf("value = %s, want one result per agent that ran (%d)", result.Value, runs.Load())
	}
	for _, agent := range result.Agents {
		if agent.Status == AgentFailed && !strings.Contains(agent.Error, "token budget of 150 was spent") {
			t.Fatalf("agent %d error = %q, want the budget named", agent.Index, agent.Error)
		}
	}
}

// Slots are shared by every run in the engine. A script busy in its own code
// must not keep them held by agents that have already finished.
func TestABusyScriptDoesNotHoldSharedSlots(t *testing.T) {
	slots := make(chan struct{}, 2)
	busyCtx, stopBusy := context.WithCancel(context.Background())
	defer stopBusy()
	busyDone := make(chan struct{})
	go func() {
		defer close(busyDone)
		_, _ = Run(busyCtx, Options{Script: mustParse(t, "agent('a1')\nagent('a2')\nwhile (true) {}"), Run: (&fakeRunner{}).run, Slots: slots})
	}()

	done := make(chan error, 1)
	go func() {
		_, err := Run(context.Background(), Options{Script: mustParse(t, "return await agent('b')"), Run: (&fakeRunner{}).run, Slots: slots})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a second run never got a slot while the first script was busy")
	}
	stopBusy()
	<-busyDone
}
