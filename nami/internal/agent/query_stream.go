package agent

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/compact"
	"github.com/channyeintun/nami/internal/ipc"
	skillspkg "github.com/channyeintun/nami/internal/skills"
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
	ExplicitSkills  []skillspkg.Skill
	Tools           []api.ToolDefinition
	Capabilities    api.ModelCapabilities
	ContextWindow   int
	MaxTokens       int
	SessionMemory   SessionMemorySnapshot
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
	CompactMessages     func(context.Context, []api.Message, CompactReason) (compact.CompactResult, error)
	RecallMemory        func(context.Context, []MemoryFile, string) ([]MemoryRecallResult, error)
	LoadSessionMemory   func(context.Context) (SessionMemorySnapshot, error)
	BeforeStop          func(context.Context, StopRequest) (StopDecision, error)
	StopController      *StopController
	ApplyResultBudget   func([]api.Message) []api.Message
	ObserveContinuation func(ContinuationTracker, string)
	EmitTelemetry       func(ipc.StreamEvent) error
	PersistMessages     func([]api.Message)
	Cleanup             func()
	Clock               func() time.Time
	// AttemptLog records session-scoped failed attempts for retry prevention.
	AttemptLog *AttemptLog
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
	Messages []api.Message
	// UserPrompt is the user message this query answers. The loop appends
	// user-role messages of its own (retry directives, stop-hook and goal
	// follow-ups) and compaction can summarize the request away, so context
	// chosen from the request is chosen from this, not the latest message.
	UserPrompt          string
	BasePrompt          string
	SystemPrompt        string
	ModelID             string
	ReasoningEffort     string
	SystemContext       SystemContext
	TurnContext         TurnContext
	PromptCache         *PromptAssemblyCache
	PromptInjection     string
	Mode                ExecutionMode
	Profile             ExecutionProfile
	Skills              []skillspkg.Skill
	ExplicitSkills      []skillspkg.Skill
	Tools               []api.ToolDefinition
	Capabilities        api.ModelCapabilities
	ContextWindow       int
	MaxTokens           int
	MaxOutputCeiling    int
	ProgressIDBase      int
	TurnCount           int
	MaxTurns            int
	StopRequested       bool
	NoToolRetryUsed     bool
	AutoCompactFailures int
	Continuation        ContinuationTracker
	// RetrievalTouched accumulates file paths touched via tools this session
	// to boost their retrieval score on subsequent turns.
	RetrievalTouched []string
	// Graph is the session-scoped retrieval graph for structural cross-references.
	Graph *RetrievalGraph
	// SessionMemory carries extracted session continuity state when available.
	SessionMemory SessionMemorySnapshot
	// AttemptEntries carries recent failed attempts for continuity-aware pressure decisions.
	AttemptEntries []AttemptEntry
	// GoalProgress holds the monotonic per-turn state behind the goal progress
	// indicator (see progress_directive.go).
	GoalProgress GoalProgressState
	// blockedStop is the BeforeStop decision that last kept the query going,
	// until the next model turn starts.
	blockedStop StopDecision
}

// stopReasonMaxTurns is the stop reason of a query that used all MaxTurns.
const stopReasonMaxTurns = "max_turns"

// NewQueryState creates initial state from a request.
func NewQueryState(req QueryRequest) *QueryState {
	initialOutputBudget := defaultOutputBudget(req.MaxTokens)
	ctx := LoadTurnContext()
	state := &QueryState{
		Messages:         req.Messages,
		UserPrompt:       latestUserPrompt(req.Messages),
		BasePrompt:       req.SystemPrompt,
		SystemPrompt:     req.SystemPrompt,
		ModelID:          req.ModelID,
		ReasoningEffort:  req.ReasoningEffort,
		SystemContext:    LoadSystemContext(),
		PromptCache:      NewPromptAssemblyCache(),
		Mode:             req.Mode,
		Profile:          ProfileForMode(req.Mode),
		Skills:           req.Skills,
		ExplicitSkills:   req.ExplicitSkills,
		Tools:            req.Tools,
		Capabilities:     req.Capabilities,
		ContextWindow:    req.ContextWindow,
		MaxTokens:        initialOutputBudget,
		MaxOutputCeiling: req.MaxTokens,
		ProgressIDBase:   len(req.Messages),
		MaxTurns:         50,
		Continuation:     NewContinuationTracker(req.MaxTokens),
		Graph:            NewRetrievalGraph(ctx.CurrentDir),
		SessionMemory:    req.SessionMemory,
	}
	return state
}

// ShouldContinue returns true if the query loop should keep iterating.
func (s *QueryState) ShouldContinue() bool {
	return !s.StopRequested && s.limitReached() == ""
}

// limitReached names the limit that ends the query before the model has
// finished: the turn cap, or a run of continuations no longer worth
// continuing. It returns "" while the query is within its limits.
func (s *QueryState) limitReached() string {
	if s.TurnCount >= s.MaxTurns {
		return stopReasonMaxTurns
	}
	return s.Continuation.Decision().Reason
}

// QueryStream is the core streaming query interface.
// It returns an iter.Seq2 of StreamEvents, suitable for pull-based consumption.
func QueryStream(ctx context.Context, req QueryRequest, deps QueryDeps) iter.Seq2[ipc.StreamEvent, error] {
	return func(consumerYield func(ipc.StreamEvent, error) bool) {
		yield := stopAwareYield(consumerYield)
		if deps.Cleanup != nil {
			defer deps.Cleanup()
		}

		state := NewQueryState(req)
		if err := emitSkippedMemoryFilesNotice(deps.EmitTelemetry, state.SystemContext.SkippedMemoryFiles); err != nil {
			yield(ipc.StreamEvent{}, err)
			return
		}

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

		if !state.StopRequested {
			if err := stopAtLimit(ctx, state, deps, yield); err != nil {
				yield(ipc.StreamEvent{}, err)
			}
		}
	}
}

// stopAtLimit ends a query that ran into a limit before the model finished:
// the turn cap, a reply that used up its output budget, or replies that
// stopped making progress. It takes the way out a normal stop takes, so the
// stop hooks and the goal see the stop, except that a hook or goal asking
// for more work cannot take the query past its limit. The user is told why
// the query ended, since from the transcript it looks like a normal finish.
func stopAtLimit(ctx context.Context, state *QueryState, deps QueryDeps, yield func(ipc.StreamEvent, error) bool) error {
	// A cancelled query is not stopping at a limit, and a goal judged on a
	// cancelled context would fail open and clear itself as met.
	if err := ctx.Err(); err != nil {
		return err
	}
	limit := state.limitReached()
	blocked := state.blockedStop
	// When the last stop was just blocked, BeforeStop has already judged
	// this transcript; asking again would repeat the hooks and the goal.
	if !blocked.Continue && deps.BeforeStop != nil {
		decision, err := deps.BeforeStop(ctx, StopRequest{
			Messages:         append([]api.Message(nil), state.Messages...),
			AssistantMessage: latestAssistantMessage(state.Messages),
			StopReason:       limit,
			TurnCount:        state.TurnCount,
		})
		if err != nil {
			return err
		}
		blocked = decision
	}

	state.StopRequested = true
	if err := yieldEvent(yield, ipc.EventTurnComplete, ipc.TurnCompletePayload{StopReason: limit}); err != nil {
		return err
	}
	// After turn_complete, which resets the TUI's status line.
	return yieldEvent(yield, ipc.EventNotice, ipc.NoticePayload{Message: limitStopNotice(state, limit, blocked)})
}

// limitStopNotice explains to the user why a query stopped at a limit.
func limitStopNotice(state *QueryState, limit string, blocked StopDecision) string {
	var notice string
	switch limit {
	case stopReasonMaxTurns:
		notice = fmt.Sprintf("Stopped after %d model turns, the most one request may take.", state.MaxTurns)
	case ContinuationStopBudgetExhausted:
		notice = fmt.Sprintf("Stopped: the model wrote %d output tokens without calling a tool, the budget for one reply.", state.Continuation.BudgetUsedTokens)
	case ContinuationStopDiminishingReturns:
		notice = "Stopped: the last replies were short and called no tools, so continuing was not making progress."
	default:
		notice = fmt.Sprintf("Stopped: %s.", limit)
	}
	if blocked.Continue {
		if reason := strings.TrimSpace(blocked.Reason); reason != "" {
			notice += fmt.Sprintf(" A stop hook or goal wanted more work (%s).", reason)
		} else {
			notice += " A stop hook or goal wanted more work."
		}
	}
	return notice + " Send another message to continue."
}

// stopAwareYield wraps a range-over-func yield so it is never called again
// after the consumer stops iterating, which the Go runtime turns into a panic.
// Once the consumer stops, yieldEvent reports context.Canceled, the stages
// unwind, and the error or turn_complete the loop would still send is dropped.
func stopAwareYield(yield func(ipc.StreamEvent, error) bool) func(ipc.StreamEvent, error) bool {
	stopped := false
	return func(event ipc.StreamEvent, err error) bool {
		if stopped {
			return false
		}
		if !yield(event, err) {
			stopped = true
			return false
		}
		return true
	}
}

// ComposeStableSystemPrompt builds the cacheable prompt prefix shared across
// normal turns and helper flows.
func ComposeStableSystemPrompt(basePrompt string, sys SystemContext, capabilities api.ModelCapabilities) string {
	return composeStableSystemPrompt(expandBaseSystemPrompt(basePrompt, capabilities), sys)
}

func composeSystemPrompt(basePrompt string, sys SystemContext, turn TurnContext, currentUserPrompt string, recalls []MemoryRecallResult, sessionMemory SessionMemorySnapshot, capabilities api.ModelCapabilities, skillPrompt string, liveRetrievalSection string, attemptLogSection string) string {
	basePrompt = expandBaseSystemPrompt(basePrompt, capabilities)
	if capabilities.SupportsCaching {
		return composeStableSystemPrompt(basePrompt, sys)
	}
	return composeLegacySystemPrompt(basePrompt, sys, turn, currentUserPrompt, recalls, sessionMemory, capabilities, skillPrompt, liveRetrievalSection, attemptLogSection)
}

func composeStableSystemPrompt(basePrompt string, sys SystemContext) string {
	basePrompt = strings.TrimSpace(basePrompt)
	memoryInstructionsPrompt := strings.TrimSpace(FormatMemoryInstructionPrompt(sys.MemoryFiles))
	systemContextPrompt := strings.TrimSpace(FormatSystemContextPrompt(sys))
	return joinPromptSections([]string{basePrompt, memoryInstructionsPrompt, systemContextPrompt})
}

func expandBaseSystemPrompt(basePrompt string, capabilities api.ModelCapabilities) string {
	if capabilityPrompt := capabilitySystemPrompt(capabilities); capabilityPrompt != "" {
		basePrompt = strings.TrimSpace(basePrompt + "\n\n" + capabilityPrompt)
	}
	return strings.TrimSpace(basePrompt)
}

func composeLegacySystemPrompt(basePrompt string, sys SystemContext, turn TurnContext, currentUserPrompt string, recalls []MemoryRecallResult, sessionMemory SessionMemorySnapshot, capabilities api.ModelCapabilities, skillPrompt string, liveRetrievalSection string, attemptLogSection string) string {
	contextPrompt := strings.TrimSpace(FormatContextPrompt(sys, turn))
	memoryPrompt := strings.TrimSpace(FormatMemoryPrompt(sys.MemoryFiles, currentUserPrompt, recalls))
	sessionMemoryPrompt := strings.TrimSpace(FormatSessionMemorySection(sessionMemory))
	skillPrompt = strings.TrimSpace(skillPrompt)
	basePrompt = strings.TrimSpace(basePrompt)
	liveRetrievalSection = strings.TrimSpace(liveRetrievalSection)
	attemptLogSection = strings.TrimSpace(attemptLogSection)
	return joinPromptSections(orderedPromptSections(capabilities.SupportsCaching, basePrompt, skillPrompt, memoryPrompt, sessionMemoryPrompt, contextPrompt, liveRetrievalSection, attemptLogSection))
}

func composePromptInjection(sys SystemContext, turn TurnContext, currentUserPrompt string, recalls []MemoryRecallResult, sessionMemory SessionMemorySnapshot, capabilities api.ModelCapabilities, skillPrompt string, liveRetrievalSection string, attemptLogSection string) string {
	if !capabilities.SupportsCaching {
		return ""
	}

	return joinPromptSections([]string{
		strings.TrimSpace(skillPrompt),
		strings.TrimSpace(FormatRelevantMemoryPrompt(sys.MemoryFiles, currentUserPrompt, recalls)),
		strings.TrimSpace(FormatSessionMemorySection(sessionMemory)),
		strings.TrimSpace(FormatTurnContextPrompt(turn)),
		strings.TrimSpace(liveRetrievalSection),
		strings.TrimSpace(attemptLogSection),
	})
}

func orderedPromptSections(supportsCaching bool, basePrompt, skillPrompt, memoryPrompt, sessionMemoryPrompt, contextPrompt, liveRetrievalSection, attemptLogSection string) []string {
	if supportsCaching {
		return []string{basePrompt, skillPrompt, memoryPrompt, sessionMemoryPrompt, contextPrompt, liveRetrievalSection, attemptLogSection}
	}
	return []string{basePrompt, memoryPrompt, skillPrompt, sessionMemoryPrompt, contextPrompt, liveRetrievalSection, attemptLogSection}
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
