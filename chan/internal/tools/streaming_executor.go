package tools

import (
	"context"
	"fmt"
	"sync"
)

// StreamingExecutor starts tool calls as they arrive, while preserving ordered result delivery.
// Parallel calls may overlap; serial calls execute alone and block later calls.
type StreamingExecutor struct {
	ctx          context.Context
	cancel       context.CancelFunc
	completionCh chan int

	mu             sync.Mutex
	calls          []*streamingCall
	nextYield      int
	closed         bool
	activeParallel int
	activeSerial   bool
	activeCount    int
}

type streamingCall struct {
	seq       int
	pending   PendingCall
	parallel  bool
	status    streamingCallStatus
	result    IndexedResult
	completed bool
	ready     bool
}

type streamingCallStatus string

const (
	streamingCallQueued    streamingCallStatus = "queued"
	streamingCallRunning   streamingCallStatus = "running"
	streamingCallCompleted streamingCallStatus = "completed"
	streamingCallYielded   streamingCallStatus = "yielded"
)

// NewStreamingExecutor constructs a streaming executor bound to ctx.
func NewStreamingExecutor(ctx context.Context) *StreamingExecutor {
	childCtx, cancel := context.WithCancel(ctx)
	return &StreamingExecutor{
		ctx:          childCtx,
		cancel:       cancel,
		completionCh: make(chan int, MaxConcurrency()),
	}
}

// Add enqueues a tool call and starts it immediately if current concurrency rules allow.
func (e *StreamingExecutor) Add(call PendingCall) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return fmt.Errorf("streaming executor is closed")
	}

	tracked := &streamingCall{
		seq:      len(e.calls),
		pending:  call,
		parallel: call.Tool.Concurrency(call.Input) == ConcurrencyParallel,
		status:   streamingCallQueued,
	}
	e.calls = append(e.calls, tracked)
	e.processQueueLocked()
	return nil
}

// Close signals that no more tool calls will be added.
func (e *StreamingExecutor) Close() {
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
}

// Cancel aborts all in-flight work.
func (e *StreamingExecutor) Cancel() {
	e.cancel()
}

// Completed returns any contiguous completed results that are ready to yield in order.
func (e *StreamingExecutor) Completed() []IndexedResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.collectReadyLocked()
}

// Wait blocks until at least one result is ready to yield, all work is complete, or ctx ends.
func (e *StreamingExecutor) Wait(ctx context.Context) ([]IndexedResult, error) {
	for {
		if ready := e.Completed(); len(ready) > 0 {
			return ready, nil
		}
		if e.Done() {
			return nil, nil
		}

		select {
		case <-ctx.Done():
			e.cancel()
			return nil, ctx.Err()
		case <-e.ctx.Done():
			if ready := e.Completed(); len(ready) > 0 {
				return ready, nil
			}
			if errors := e.abortPending(); len(errors) > 0 {
				return errors, nil
			}
			return nil, e.ctx.Err()
		case <-e.completionCh:
		}
	}
}

// Drain waits for all outstanding work and returns results in original order.
func (e *StreamingExecutor) Drain(ctx context.Context) ([]IndexedResult, error) {
	var results []IndexedResult
	for !e.Done() {
		ready, err := e.Wait(ctx)
		if err != nil {
			return results, err
		}
		results = append(results, ready...)
	}
	return results, nil
}

// Done reports whether the executor is closed and all calls have been yielded.
func (e *StreamingExecutor) Done() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.closed {
		return false
	}
	return e.nextYield >= len(e.calls) && e.activeCount == 0
}

func (e *StreamingExecutor) abortPending() []IndexedResult {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, call := range e.calls {
		if call.status != streamingCallQueued {
			continue
		}
		call.status = streamingCallCompleted
		call.completed = true
		call.result = IndexedResult{
			Index: call.pending.Index,
			Output: ToolOutput{
				Output:  "tool execution cancelled",
				IsError: true,
			},
			Err: e.ctx.Err(),
		}
	}
	return e.collectReadyLocked()
}

func (e *StreamingExecutor) collectReadyLocked() []IndexedResult {
	results := make([]IndexedResult, 0)
	for e.nextYield < len(e.calls) {
		call := e.calls[e.nextYield]
		if !call.completed || call.status == streamingCallYielded {
			break
		}
		call.status = streamingCallYielded
		results = append(results, call.result)
		e.nextYield++
	}
	return results
}

func (e *StreamingExecutor) processQueueLocked() {
	for _, call := range e.calls {
		if call.status != streamingCallQueued {
			continue
		}
		if !e.canStartLocked(call.parallel) {
			break
		}
		e.startLocked(call)
	}
}

func (e *StreamingExecutor) canStartLocked(parallel bool) bool {
	if e.activeCount == 0 {
		return true
	}
	if !parallel {
		return false
	}
	return !e.activeSerial
}

func (e *StreamingExecutor) startLocked(call *streamingCall) {
	call.status = streamingCallRunning
	e.activeCount++
	if call.parallel {
		e.activeParallel++
	} else {
		e.activeSerial = true
	}

	go func(tracked *streamingCall) {
		output, err := tracked.pending.Tool.Execute(e.ctx, tracked.pending.Input)

		e.mu.Lock()
		defer e.mu.Unlock()

		tracked.result = IndexedResult{
			Index:  tracked.pending.Index,
			Output: output,
			Err:    err,
		}
		tracked.completed = true
		tracked.status = streamingCallCompleted
		e.activeCount--
		if tracked.parallel {
			e.activeParallel--
		} else {
			e.activeSerial = false
		}
		e.processQueueLocked()

		select {
		case e.completionCh <- tracked.seq:
		default:
		}
	}(call)
}
