package agent

import (
	"github.com/channyeintun/gocode/internal/api"
	"github.com/channyeintun/gocode/internal/compact"
)

const (
	memoryRecallPressureBufferTokens   = 4_000
	continuationPressureBudgetPercent  = 75
	continuationPressureCountThreshold = 2
)

// ContextPressureDecision centralizes turn-level prompt pressure heuristics so
// memory recall, continuation, and proactive compaction cooperate.
type ContextPressureDecision struct {
	ConversationTokens int
	EffectiveWindow    int
	WarningThreshold   int
	CompactThreshold   int
	ShouldCompact      bool
	SkipMemoryRecall   bool
}

func EvaluateContextPressure(messages []api.Message, contextWindow, maxOutputTokens int, continuation ContinuationTracker) ContextPressureDecision {
	effectiveWindow := compact.EffectiveContextWindow(contextWindow, maxOutputTokens)
	warningThreshold := compact.WarningThreshold(effectiveWindow)
	compactThreshold := compact.AutocompactThreshold(effectiveWindow)
	conversationTokens := compact.EstimateConversationTokens(messages)

	decision := ContextPressureDecision{
		ConversationTokens: conversationTokens,
		EffectiveWindow:    effectiveWindow,
		WarningThreshold:   warningThreshold,
		CompactThreshold:   compactThreshold,
	}

	if compactThreshold > 0 && conversationTokens >= compactThreshold {
		decision.ShouldCompact = true
	}

	recallThreshold := warningThreshold - memoryRecallPressureBufferTokens
	if recallThreshold < 0 {
		recallThreshold = 0
	}
	continuationHot := continuation.ContinuationCount >= continuationPressureCountThreshold
	if continuation.MaxBudgetTokens > 0 && continuation.BudgetUsedTokens*100 >= continuation.MaxBudgetTokens*continuationPressureBudgetPercent {
		continuationHot = true
	}

	if conversationTokens >= recallThreshold && (warningThreshold > 0 || continuationHot) {
		decision.SkipMemoryRecall = true
	}
	if decision.ShouldCompact {
		decision.SkipMemoryRecall = true
	}

	return decision
}
