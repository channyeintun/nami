package tools

import (
	"context"
	"os"
	"strconv"
	"sync"
)

// DefaultMaxConcurrency is the default max parallel tool executions.
const DefaultMaxConcurrency = 10

// MaxConcurrency returns the configured max concurrency from env or default.
func MaxConcurrency() int {
	if v := os.Getenv("GOCLI_MAX_TOOL_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return DefaultMaxConcurrency
}

// Batch represents a group of tool calls to execute together.
type Batch struct {
	Calls      []PendingCall
	Concurrent bool
}

// PendingCall is a tool call waiting to be executed.
type PendingCall struct {
	Index int       // original position in the model's tool_calls array
	Tool  Tool
	Input ToolInput
}

// PartitionBatches splits tool calls into serial/concurrent batches
// based on per-call concurrency classification.
func PartitionBatches(calls []PendingCall) []Batch {
	if len(calls) == 0 {
		return nil
	}

	var batches []Batch
	var currentSafe []PendingCall

	flush := func() {
		if len(currentSafe) > 0 {
			batches = append(batches, Batch{Calls: currentSafe, Concurrent: true})
			currentSafe = nil
		}
	}

	for _, call := range calls {
		if call.Tool.IsConcurrencySafe(call.Input) {
			currentSafe = append(currentSafe, call)
		} else {
			flush()
			batches = append(batches, Batch{Calls: []PendingCall{call}, Concurrent: false})
		}
	}
	flush()
	return batches
}

// ExecuteBatch runs a batch of tool calls, concurrently if safe.
func ExecuteBatch(ctx context.Context, batch Batch) []IndexedResult {
	results := make([]IndexedResult, len(batch.Calls))

	if !batch.Concurrent || len(batch.Calls) == 1 {
		for i, call := range batch.Calls {
			output, err := call.Tool.Execute(ctx, call.Input)
			results[i] = IndexedResult{Index: call.Index, Output: output, Err: err}
		}
		return results
	}

	batchCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	maxConc := MaxConcurrency()
	sem := make(chan struct{}, maxConc)
	var wg sync.WaitGroup

	for i, call := range batch.Calls {
		wg.Add(1)
		go func(idx int, c PendingCall) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			output, err := c.Tool.Execute(batchCtx, c.Input)
			results[idx] = IndexedResult{Index: c.Index, Output: output, Err: err}
		}(i, call)
	}
	wg.Wait()
	return results
}

// IndexedResult pairs a tool result with its original call index.
type IndexedResult struct {
	Index  int
	Output ToolOutput
	Err    error
}
