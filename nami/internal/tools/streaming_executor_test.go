package tools

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// stubTool is a Tool whose behavior each test supplies.
type stubTool struct {
	name     string
	parallel bool
	run      func(context.Context) (ToolOutput, error)
}

func (s *stubTool) Name() string                { return s.name }
func (s *stubTool) Description() string         { return "test tool" }
func (s *stubTool) InputSchema() any            { return map[string]any{"type": "object"} }
func (s *stubTool) Permission() PermissionLevel { return PermissionReadOnly }

func (s *stubTool) Concurrency(ToolInput) ConcurrencyDecision {
	if s.parallel {
		return ConcurrencyParallel
	}
	return ConcurrencySerial
}

func (s *stubTool) Execute(ctx context.Context, _ ToolInput) (ToolOutput, error) {
	return s.run(ctx)
}

func panickingTool(name string, parallel bool) *stubTool {
	return &stubTool{name: name, parallel: parallel, run: func(context.Context) (ToolOutput, error) {
		var values []int
		_ = values[3] // index out of range
		return ToolOutput{}, nil
	}}
}

func succeedingTool(name string, parallel bool) *stubTool {
	return &stubTool{name: name, parallel: parallel, run: func(context.Context) (ToolOutput, error) {
		return ToolOutput{Output: name + " ran"}, nil
	}}
}

// runStreamingExecutor queues calls in order and collects every result by
// call index, failing if the executor stops making progress.
func runStreamingExecutor(t *testing.T, tools ...Tool) map[int]IndexedResult {
	t.Helper()
	executor := NewStreamingExecutor(t.Context())
	defer executor.Cancel()
	for index, tool := range tools {
		if err := executor.Add(PendingCall{Index: index, Tool: tool}); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	executor.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	results := make(map[int]IndexedResult)
	for !executor.Done() {
		ready, err := executor.Wait(ctx)
		if err != nil {
			t.Fatalf("Wait: %v (results so far: %v)", err, results)
		}
		for _, result := range ready {
			results[result.Index] = result
		}
	}
	return results
}

func assertPanicResult(t *testing.T, result IndexedResult, toolName string) {
	t.Helper()
	if result.Err == nil {
		t.Fatalf("result = %+v, want an error for the panicking tool", result)
	}
	message := result.Err.Error()
	for _, want := range []string{toolName, "panicked", "index out of range", "goroutine"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q does not mention %q", message, want)
		}
	}
}

// A panic inside one tool has to fail that call, not crash the engine and lose
// the session. The serial slot the call held must also be released, or every
// later call would wait forever.
func TestStreamingExecutorTurnsAToolPanicIntoAFailedCall(t *testing.T) {
	results := runStreamingExecutor(t, panickingTool("boom", false), succeedingTool("after", false))

	assertPanicResult(t, results[0], "boom")
	if got := results[1]; got.Err != nil || got.Output.Output != "after ran" {
		t.Fatalf("call after the panic = %+v, want it to run normally", got)
	}
}

// Once the turn is cancelled, calls still waiting their turn must not start.
// Not every tool checks its context first (exit_plan_mode switches the session
// mode without one), and a queued call that ran anyway would carry out an
// action the user had just cancelled.
func TestStreamingExecutorDoesNotStartQueuedCallsAfterCancel(t *testing.T) {
	release := make(chan struct{})
	running := &stubTool{name: "running", run: func(context.Context) (ToolOutput, error) {
		<-release // ignores its context, as a tool blocked in a syscall would
		return ToolOutput{Output: "running finished"}, nil
	}}
	var queuedStarted atomic.Bool
	queued := &stubTool{name: "queued", run: func(context.Context) (ToolOutput, error) {
		queuedStarted.Store(true)
		return ToolOutput{Output: "queued ran"}, nil
	}}

	ctx, cancel := context.WithCancel(t.Context())
	executor := NewStreamingExecutor(ctx)
	defer executor.Cancel()
	for index, tool := range []Tool{running, queued} {
		if err := executor.Add(PendingCall{Index: index, Tool: tool}); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	executor.Close()
	cancel()
	close(release)

	// Completed never aborts anything, so polling it lets the running call
	// finish and hand its slot to the queue exactly as it would mid-turn.
	deadline := time.Now().Add(5 * time.Second)
	results := make(map[int]IndexedResult)
	for !executor.Done() {
		if time.Now().After(deadline) {
			t.Fatalf("executor never finished; results so far: %v", results)
		}
		ready := executor.Completed()
		if _, finished := results[0]; finished && len(ready) == 0 {
			ready, _ = executor.Wait(context.Background())
		}
		for _, result := range ready {
			results[result.Index] = result
		}
		time.Sleep(10 * time.Millisecond)
	}

	if queuedStarted.Load() {
		t.Fatal("a queued call started after the turn was cancelled")
	}
	if got := results[1]; !errors.Is(got.Err, context.Canceled) || !got.Output.IsError {
		t.Fatalf("queued call result = %+v, want it reported as cancelled", got)
	}
}

func TestExecuteBatchDoesNotStartCallsAfterCancel(t *testing.T) {
	var started atomic.Bool
	tool := &stubTool{name: "late", run: func(context.Context) (ToolOutput, error) {
		started.Store(true)
		return ToolOutput{Output: "late ran"}, nil
	}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	results := ExecuteBatchWithOptions(ctx, Batch{Calls: []PendingCall{{Index: 0, Tool: tool}}}, ExecuteOptions{})

	if started.Load() {
		t.Fatal("a call started after its context was cancelled")
	}
	if got := results[0]; !errors.Is(got.Err, context.Canceled) || !got.Output.IsError {
		t.Fatalf("result = %+v, want it reported as cancelled", got)
	}
}

func TestStreamingExecutorTurnsAParallelToolPanicIntoAFailedCall(t *testing.T) {
	results := runStreamingExecutor(t, panickingTool("boom", true), succeedingTool("beside", true))

	assertPanicResult(t, results[0], "boom")
	if got := results[1]; got.Err != nil || got.Output.Output != "beside ran" {
		t.Fatalf("call beside the panic = %+v, want it to run normally", got)
	}
}
