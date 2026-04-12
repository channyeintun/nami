package agent

import (
	"context"
	"iter"
	"strings"
	"time"

	"github.com/channyeintun/gocode/internal/api"
	"github.com/channyeintun/gocode/internal/ipc"
	skillspkg "github.com/channyeintun/gocode/internal/skills"
)

// QueryRequest holds everything needed to start a query.
type QueryRequest struct {
	Messages        []api.Message
	SystemPrompt    string
	ModelID         string
	ReasoningEffort string
	Mode            ExecutionMode
	SessionID       string
	Skills          []skillspkg.Skill
	Tools           []api.ToolDefinition
	Capabilities    api.ModelCapabilities
	ContextWindow   int
	MaxTokens       int
}

// CompactReason indicates why compaction was triggered.
type CompactReason string

const (
	CompactAuto   CompactReason = "auto"
	CompactManual CompactReason = "manual"
)

// QueryDeps injects all side effects into the query engine.
type QueryDeps struct {
	CallModel           func(context.Context, api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error)
	ExecuteToolBatch    func(context.Context, []api.ToolCall) ([]api.ToolResult, error)
	CompactMessages     func(context.Context, []api.Message, CompactReason) ([]api.Message, error)
	RecallMemory        func(context.Context, []MemoryFile, string) ([]MemoryRecallResult, error)
	BeforeStop          func(context.Context, StopRequest) (StopDecision, error)
	StopController      *StopController
	ApplyResultBudget   func([]api.Message) []api.Message
	ObserveContinuation func(ContinuationTracker, string)
	EmitTelemetry       func(ipc.StreamEvent) error
	PersistMessages     func([]api.Message)
	Cleanup             func()
	Clock               func() time.Time
}

type StopRequest struct {
	Messages         []api.Message
	AssistantMessage api.Message
	StopReason       string
	TurnCount        int
}

type StopDecision struct {
	Continue        bool
	Reason          string
	FollowUpMessage string
}

// QueryState tracks iteration state within a query.
type QueryState struct {
	Messages            []api.Message
	BasePrompt          string
	SystemPrompt        string
	ModelID             string
	ReasoningEffort     string
	SystemContext       SystemContext
	TurnContext         TurnContext
	PromptCache         *PromptAssemblyCache
	Mode                ExecutionMode
	Profile             ExecutionProfile
	Skills              []skillspkg.Skill
	Tools               []api.ToolDefinition
	Capabilities        api.ModelCapabilities
	ContextWindow       int
	MaxTokens           int
	MaxOutputCeiling    int
	TurnCount           int
	MaxTurns            int
	StopRequested       bool
	NoToolRetryUsed     bool
	AutoCompactFailures int
	Continuation        ContinuationTracker
}

// NewQueryState creates initial state from a request.
func NewQueryState(req QueryRequest) *QueryState {
	initialOutputBudget := defaultOutputBudget(req.MaxTokens)
	return &QueryState{
		Messages:         req.Messages,
		BasePrompt:       req.SystemPrompt,
		SystemPrompt:     req.SystemPrompt,
		ModelID:          req.ModelID,
		ReasoningEffort:  req.ReasoningEffort,
		SystemContext:    LoadSystemContext(),
		PromptCache:      NewPromptAssemblyCache(),
		Mode:             req.Mode,
		Profile:          ProfileForMode(req.Mode),
		Skills:           req.Skills,
		Tools:            req.Tools,
		Capabilities:     req.Capabilities,
		ContextWindow:    req.ContextWindow,
		MaxTokens:        initialOutputBudget,
		MaxOutputCeiling: req.MaxTokens,
		MaxTurns:         50,
		Continuation:     NewContinuationTracker(req.MaxTokens),
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
	return !s.Continuation.Decision().ShouldStop
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

			handled, err := handlePendingStopRequest(ctx, state, deps, yield)
			if err != nil {
				persistMessages(state.Messages, deps.PersistMessages)
				yield(ipc.StreamEvent{}, err)
				return
			}
			if handled {
				persistMessages(state.Messages, deps.PersistMessages)
				continue
			}

			if err := runIteration(ctx, state, deps, yield); err != nil {
				persistMessages(state.Messages, deps.PersistMessages)
				yield(ipc.StreamEvent{}, err)
				return
			}

			persistMessages(state.Messages, deps.PersistMessages)
		}

		if deps.ObserveContinuation != nil {
			decision := state.Continuation.Decision()
			deps.ObserveContinuation(state.Continuation, decision.Reason)
		}

		// If the loop exited without the agent explicitly stopping (e.g. hit the
		// max-turn limit or continuation budget), emit turn_complete so the TUI
		// transitions out of the "Working" state instead of spinning forever.
		if !state.StopRequested {
			yield(newEvent(ipc.EventTurnComplete, ipc.TurnCompletePayload{StopReason: "stop"}), nil)
		}
	}
}

func composeSystemPrompt(basePrompt string, sys SystemContext, turn TurnContext, currentUserPrompt string, recalls []MemoryRecallResult, capabilities api.ModelCapabilities, skillPrompt string) string {
	contextPrompt := strings.TrimSpace(FormatContextPrompt(sys, turn))
	memoryPrompt := strings.TrimSpace(FormatMemoryPrompt(sys.MemoryFiles, currentUserPrompt, recalls))
	skillPrompt = strings.TrimSpace(skillPrompt)
	basePrompt = strings.TrimSpace(basePrompt)
	return joinPromptSections(orderedPromptSections(capabilities.SupportsCaching, basePrompt, skillPrompt, memoryPrompt, contextPrompt))
}

func orderedPromptSections(supportsCaching bool, basePrompt, skillPrompt, memoryPrompt, contextPrompt string) []string {
	if supportsCaching {
		return []string{basePrompt, skillPrompt, memoryPrompt, contextPrompt}
	}
	return []string{basePrompt, memoryPrompt, skillPrompt, contextPrompt}
}

func joinPromptSections(parts []string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		filtered = append(filtered, part)
	}
	return strings.Join(filtered, "\n\n")
}

func persistMessages(messages []api.Message, persist func([]api.Message)) {
	if persist == nil {
		return
	}
	cloned := append([]api.Message(nil), messages...)
	persist(cloned)
}
