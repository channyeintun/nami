package workflow

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Status is a node's terminal state within a run.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusSkipped   Status = "skipped"
	// StatusCached marks a node replayed from a prior run's journal instead of
	// re-executed.
	StatusCached Status = "cached"
)

// Succeeded reports whether the status counts as a usable result for dependents.
func (s Status) Succeeded() bool {
	return s == StatusSucceeded || s == StatusCached
}

// NodeRequest is what the runner hands a NodeRunner. Prompt has already had its
// ${outputs.*} references expanded, and Inputs carries the same dependency
// outputs unexpanded for a runner that would rather assemble its own context.
type NodeRequest struct {
	RunID       string
	ID          string
	Description string
	Prompt      string
	Agent       AgentSpec
	Inputs      map[string]string
}

// NodeResult is what a NodeRunner returns. A runner reports work-level failure
// through the error; NodeResult carries only the successful payload.
type NodeResult struct {
	Output   string
	Metadata map[string]string
}

// NodeRunner performs one node's work.
type NodeRunner func(context.Context, NodeRequest) (NodeResult, error)

// NodeState is a node's outcome in a finished (or in-flight) run.
type NodeState struct {
	ID          string            `json:"id"`
	Description string            `json:"description"`
	Status      Status            `json:"status"`
	Output      string            `json:"output,omitempty"`
	Error       string            `json:"error,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	DependsOn   []string          `json:"depends_on,omitempty"`
	StartedAt   time.Time         `json:"started_at,omitzero"`
	CompletedAt time.Time         `json:"completed_at,omitzero"`
}

// Duration is how long the node ran, or zero if it never started or is running.
func (n NodeState) Duration() time.Duration {
	if n.StartedAt.IsZero() || n.CompletedAt.IsZero() {
		return 0
	}
	return n.CompletedAt.Sub(n.StartedAt)
}

// Result is the outcome of a whole run.
type Result struct {
	RunID       string      `json:"run_id"`
	Description string      `json:"description,omitempty"`
	Status      Status      `json:"status"`
	Nodes       []NodeState `json:"nodes"`
	Succeeded   int         `json:"succeeded"`
	Failed      int         `json:"failed"`
	Skipped     int         `json:"skipped"`
	Cached      int         `json:"cached"`
	StartedAt   time.Time   `json:"started_at,omitzero"`
	CompletedAt time.Time   `json:"completed_at,omitzero"`
}

// Outputs maps node id to output for every node that produced one.
func (r Result) Outputs() map[string]string {
	outputs := make(map[string]string, len(r.Nodes))
	for _, node := range r.Nodes {
		if node.Status.Succeeded() {
			outputs[node.ID] = node.Output
		}
	}
	return outputs
}

// Progress reports a node state transition. It is called from the scheduler
// goroutine, one call per transition, never per output token.
type Progress struct {
	RunID     string
	Node      NodeState
	Completed int
	Total     int
}

// Options configures a run.
type Options struct {
	RunID string
	Run   NodeRunner
	// OnProgress is called synchronously on each node transition. A slow
	// callback slows the whole run, so it should hand off rather than block.
	OnProgress func(Progress)
	// Journal, when set, records node results and replays matching ones from a
	// prior run instead of re-executing them.
	Journal *Journal
	Clock   func() time.Time
}

// ErrNoRunner is returned when Options carries no NodeRunner.
var ErrNoRunner = errors.New("workflow: no node runner configured")

// ConcurrencyLimit returns the default cap on simultaneously running nodes.
// Leaving two cores free keeps the scheduler and the rest of the process
// responsive while a run saturates the machine.
func ConcurrencyLimit() int {
	return min(max(runtime.NumCPU()-2, 2), 16)
}

// Run executes the graph, starting each node the moment its dependencies finish
// rather than advancing in fixed phases. A slow node therefore only delays its
// own dependents, never an unrelated branch.
func (s ResolvedSpec) Run(ctx context.Context, opts Options) (Result, error) {
	if opts.Run == nil {
		return Result{}, ErrNoRunner
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}

	limit := max(min(s.MaxParallel, ConcurrencyLimit()), 1)

	run := &runState{
		spec:    s,
		runID:   strings.TrimSpace(opts.RunID),
		opts:    opts,
		clock:   clock,
		limit:   limit,
		index:   make(map[string]int, len(s.Nodes)),
		states:  make([]NodeState, len(s.Nodes)),
		blocked: make([]int, len(s.Nodes)),
		keys:    make([]string, len(s.Nodes)),
		outputs: make(map[string]string, len(s.Nodes)),
	}
	for i, node := range s.Nodes {
		run.index[node.ID] = i
		run.blocked[i] = len(node.DependsOn)
		run.states[i] = NodeState{
			ID:          node.ID,
			Description: node.Description,
			Status:      StatusQueued,
			DependsOn:   slices.Clone(node.DependsOn),
		}
	}

	return run.execute(ctx), nil
}

type runState struct {
	spec    ResolvedSpec
	runID   string
	opts    Options
	clock   func() time.Time
	limit   int
	index   map[string]int
	states  []NodeState
	blocked []int
	outputs map[string]string
	// keys holds each node's journal key once it launches, so a dependent can
	// derive its own key from the keys of everything it depended on.
	keys      []string
	completed int
	aborted   bool
}

type completion struct {
	idx    int
	result NodeResult
	err    error
	cached bool
}

func (r *runState) execute(ctx context.Context) Result {
	startedAt := r.clock()

	// Cancelling on the way out stops any node still in flight when the run
	// aborts, so Run never returns while work continues behind it.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	ready := r.initialReady()
	done := make(chan completion)
	var wg sync.WaitGroup
	inflight := 0

	for {
		for inflight < r.limit && len(ready) > 0 {
			idx := ready[0]
			ready = ready[1:]
			// A node can be marked skipped while it sits in the ready queue,
			// when an abort or an upstream failure lands first. Launching it
			// anyway would overwrite that outcome and run work the run has
			// already given up on.
			if r.states[idx].Status != StatusQueued {
				continue
			}
			inflight++
			r.launch(runCtx, &wg, done, idx)
		}
		if inflight == 0 {
			break
		}

		finished := <-done
		inflight--
		ready = append(ready, r.settle(finished)...)
		sort.Ints(ready)
	}

	wg.Wait()

	result := Result{
		RunID:       r.runID,
		Description: r.spec.Description,
		Nodes:       r.states,
		StartedAt:   startedAt,
		CompletedAt: r.clock(),
	}
	for _, state := range r.states {
		switch state.Status {
		case StatusSucceeded:
			result.Succeeded++
		case StatusCached:
			result.Succeeded++
			result.Cached++
		case StatusFailed:
			result.Failed++
		case StatusSkipped:
			result.Skipped++
		}
	}
	result.Status = StatusSucceeded
	if result.Failed > 0 || result.Skipped > 0 {
		result.Status = StatusFailed
	}
	return result
}

func (r *runState) initialReady() []int {
	ready := make([]int, 0, len(r.spec.Nodes))
	for i := range r.spec.Nodes {
		if r.blocked[i] == 0 {
			ready = append(ready, i)
		}
	}
	sort.Ints(ready)
	return ready
}

func (r *runState) launch(ctx context.Context, wg *sync.WaitGroup, done chan<- completion, idx int) {
	node := r.spec.Nodes[idx]
	inputs := make(map[string]string, len(node.DependsOn))
	for _, dep := range node.DependsOn {
		if output, ok := r.outputs[dep]; ok {
			inputs[dep] = output
		}
	}
	prompt := ExpandPrompt(node.Prompt, r.outputs)

	// Derived here, in the scheduler goroutine, where every dependency has
	// already settled and recorded its own key.
	dependencyKeys := make([]string, 0, len(node.DependsOn))
	for _, dep := range node.DependsOn {
		dependencyKeys = append(dependencyKeys, r.keys[r.index[dep]])
	}
	key := nodeKey(dependencyKeys, node, prompt)
	r.keys[idx] = key

	r.states[idx].Status = StatusRunning
	r.states[idx].StartedAt = r.clock()
	r.emit(idx)

	if cached, ok := r.opts.Journal.Replay(key); ok {
		go func() { done <- completion{idx: idx, result: cached, cached: true} }()
		return
	}

	request := NodeRequest{
		RunID:       r.runID,
		ID:          node.ID,
		Description: node.Description,
		Prompt:      prompt,
		Agent:       node.Agent,
		Inputs:      inputs,
	}
	wg.Go(func() {
		result, err := r.opts.Run(ctx, request)
		if err == nil {
			r.opts.Journal.Record(key, node.ID, result)
		}
		done <- completion{idx: idx, result: result, err: err}
	})
}

// settle applies one completion and returns the nodes it unblocked.
func (r *runState) settle(finished completion) []int {
	idx := finished.idx
	state := &r.states[idx]
	state.CompletedAt = r.clock()
	r.completed++

	if finished.err != nil {
		state.Status = StatusFailed
		state.Error = finished.err.Error()
		r.emit(idx)
		return r.propagateFailure(idx)
	}

	state.Status = StatusSucceeded
	if finished.cached {
		state.Status = StatusCached
	}
	state.Output = finished.result.Output
	state.Metadata = finished.result.Metadata
	r.outputs[state.ID] = finished.result.Output
	r.emit(idx)

	return r.unblockDependents(idx)
}

func (r *runState) unblockDependents(idx int) []int {
	if r.aborted {
		return nil
	}
	unblocked := make([]int, 0, len(r.spec.Nodes[idx].Dependents))
	for _, dependentID := range r.spec.Nodes[idx].Dependents {
		dependentIdx := r.index[dependentID]
		r.blocked[dependentIdx]--
		if r.blocked[dependentIdx] == 0 && r.states[dependentIdx].Status == StatusQueued {
			unblocked = append(unblocked, dependentIdx)
		}
	}
	return unblocked
}

// propagateFailure marks everything the failed node's result was needed for as
// skipped. Under FailureAbort that is every node still queued, not just the
// dependents.
func (r *runState) propagateFailure(idx int) []int {
	failedID := r.spec.Nodes[idx].ID
	reason := fmt.Sprintf("dependency %q failed", failedID)
	doomed := transitiveDependents(r.spec.Nodes, r.index, failedID)

	if r.spec.FailurePolicy == FailureAbort {
		r.aborted = true
		reason = fmt.Sprintf("run aborted after %q failed", failedID)
		doomed = doomed[:0]
		for _, node := range r.spec.Nodes {
			if node.ID != failedID {
				doomed = append(doomed, node.ID)
			}
		}
	}

	for _, id := range doomed {
		doomedIdx := r.index[id]
		if r.states[doomedIdx].Status != StatusQueued {
			continue
		}
		r.states[doomedIdx].Status = StatusSkipped
		r.states[doomedIdx].Error = reason
		r.states[doomedIdx].CompletedAt = r.clock()
		r.completed++
		r.emit(doomedIdx)
	}
	return nil
}

func (r *runState) emit(idx int) {
	if r.opts.OnProgress == nil {
		return
	}
	r.opts.OnProgress(Progress{
		RunID:     r.runID,
		Node:      r.states[idx],
		Completed: r.completed,
		Total:     len(r.spec.Nodes),
	})
}
