package agent

import (
	"context"
	"iter"
	"strings"
	"time"

	"github.com/channyeintun/go-cli/internal/api"
	"github.com/channyeintun/go-cli/internal/ipc"
	skillspkg "github.com/channyeintun/go-cli/internal/skills"
)

// QueryRequest holds everything needed to start a query.
type QueryRequest struct {
	Messages      []api.Message
	SystemPrompt  string
	Mode          ExecutionMode
	SessionID     string
	Skills        []skillspkg.Skill
	Tools         []api.ToolDefinition
	Capabilities  api.ModelCapabilities
	ContextWindow int
	MaxTokens     int
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
	ExecuteToolBatch  func(context.Context, []api.ToolCall) ([]api.ToolResult, error)
	CompactMessages   func(context.Context, []api.Message, CompactReason) ([]api.Message, error)
	ApplyResultBudget func([]api.Message) []api.Message
	EmitTelemetry     func(ipc.StreamEvent)
	PersistMessages   func([]api.Message)
	Cleanup           func()
	Clock             func() time.Time
}

// QueryState tracks iteration state within a query.
type QueryState struct {
	Messages            []api.Message
	BasePrompt          string
	SystemPrompt        string
	SystemContext       SystemContext
	TurnContext         TurnContext
	Mode                ExecutionMode
	Profile             ExecutionProfile
	Skills              []skillspkg.Skill
	Tools               []api.ToolDefinition
	Capabilities        api.ModelCapabilities
	ContextWindow       int
	MaxTokens           int
	TurnCount           int
	MaxTurns            int
	StopRequested       bool
	AutoCompactFailures int
	Continuation        ContinuationTracker
}

// NewQueryState creates initial state from a request.
func NewQueryState(req QueryRequest) *QueryState {
	return &QueryState{
		Messages:      req.Messages,
		BasePrompt:    req.SystemPrompt,
		SystemPrompt:  req.SystemPrompt,
		SystemContext: LoadSystemContext(),
		Mode:          req.Mode,
		Profile:       ProfileForMode(req.Mode),
		Skills:        req.Skills,
		Tools:         req.Tools,
		Capabilities:  req.Capabilities,
		ContextWindow: req.ContextWindow,
		MaxTokens:     req.MaxTokens,
		MaxTurns:      50,
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
				persistMessages(state.Messages, deps.PersistMessages)
				yield(ipc.StreamEvent{}, ctx.Err())
				return
			default:
			}

			if err := runIteration(ctx, state, deps, yield); err != nil {
				persistMessages(state.Messages, deps.PersistMessages)
				yield(ipc.StreamEvent{}, err)
				return
			}

			persistMessages(state.Messages, deps.PersistMessages)
		}
	}
}

func composeSystemPrompt(basePrompt string, sys SystemContext, turn TurnContext, skillPrompt string) string {
	contextPrompt := strings.TrimSpace(FormatContextPrompt(sys, turn))
	skillPrompt = strings.TrimSpace(skillPrompt)
	basePrompt = strings.TrimSpace(basePrompt)
	parts := make([]string, 0, 3)
	if basePrompt != "" {
		parts = append(parts, basePrompt)
	}
	if skillPrompt != "" {
		parts = append(parts, skillPrompt)
	}
	if contextPrompt != "" {
		parts = append(parts, contextPrompt)
	}
	return strings.Join(parts, "\n\n")
}

func persistMessages(messages []api.Message, persist func([]api.Message)) {
	if persist == nil {
		return
	}
	cloned := append([]api.Message(nil), messages...)
	persist(cloned)
}
