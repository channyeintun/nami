package agent

import (
	"context"
	"iter"
	"time"

	"github.com/channyeintun/go-cli/internal/api"
	"github.com/channyeintun/go-cli/internal/ipc"
	"github.com/channyeintun/go-cli/internal/tools"
)

// QueryRequest holds everything needed to start a query.
type QueryRequest struct {
	Messages     []api.Message
	SystemPrompt string
	Mode         ExecutionMode
	SessionID    string
}

// ToolBatch is a set of tool calls from one model turn.
type ToolBatch struct {
	Calls []tools.PendingCall
}

// CompactReason indicates why compaction was triggered.
type CompactReason string

const (
	CompactAuto   CompactReason = "auto"
	CompactManual CompactReason = "manual"
)

// QueryDeps injects all side effects into the query engine.
type QueryDeps struct {
	CallModel         func(context.Context, api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error)
	ExecuteToolBatch  func(context.Context, ToolBatch) ([]api.ToolResult, error)
	CompactMessages   func(context.Context, []api.Message, CompactReason) ([]api.Message, error)
	ApplyResultBudget func([]api.Message) []api.Message
	EmitTelemetry     func(ipc.StreamEvent)
	Cleanup           func()
	Clock             func() time.Time
}

// QueryState tracks iteration state within a query.
type QueryState struct {
	Messages      []api.Message
	Mode          ExecutionMode
	Profile       ExecutionProfile
	TurnCount     int
	MaxTurns      int
	StopRequested bool
	Continuation  ContinuationTracker
}

// NewQueryState creates initial state from a request.
func NewQueryState(req QueryRequest) *QueryState {
	return &QueryState{
		Messages: req.Messages,
		Mode:     req.Mode,
		Profile:  ProfileForMode(req.Mode),
		MaxTurns: 50,
	}
}

// ShouldContinue returns true if the query loop should keep iterating.
func (s *QueryState) ShouldContinue() bool {
	if s.StopRequested {
		return false
	}
	if s.TurnCount >= s.MaxTurns {
		return false
	}
	return !s.Continuation.ShouldStop()
}

// QueryStream is the core streaming query interface.
// It returns an iter.Seq2 of StreamEvents, suitable for pull-based consumption.
func QueryStream(ctx context.Context, req QueryRequest, deps QueryDeps) iter.Seq2[ipc.StreamEvent, error] {
	return func(yield func(ipc.StreamEvent, error) bool) {
		if deps.Cleanup != nil {
			defer deps.Cleanup()
		}

		state := NewQueryState(req)

		for state.ShouldContinue() {
			select {
			case <-ctx.Done():
				yield(ipc.StreamEvent{}, ctx.Err())
				return
			default:
			}

			if err := runIteration(ctx, state, deps, yield); err != nil {
				yield(ipc.StreamEvent{}, err)
				return
			}
		}
	}
}

// runIteration executes one iteration of the five-phase query loop.
// Phases: Setup → Model invocation → Recovery → Tool execution → Continuation
func runIteration(
	ctx context.Context,
	state *QueryState,
	deps QueryDeps,
	yield func(ipc.StreamEvent, error) bool,
) error {
	state.TurnCount++

	// Phase 1: Setup — apply result budgets, compact if needed
	if deps.ApplyResultBudget != nil {
		state.Messages = deps.ApplyResultBudget(state.Messages)
	}

	// Phase 2: Model invocation — stream tokens and tool calls
	// (implementation will call deps.CallModel and yield events)

	// Phase 3: Recovery — handle API errors (prompt_too_long → compact, etc.)
	// (implementation will wrap Phase 2 with retry logic)

	// Phase 4: Tool execution — execute any tool calls from the model
	// (implementation will call deps.ExecuteToolBatch and append results)

	// Phase 5: Continuation — decide whether to loop
	// (check stop_reason, continuation tracker, mode policy)

	return nil
}
