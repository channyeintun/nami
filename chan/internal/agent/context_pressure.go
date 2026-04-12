package agent

import (
	"github.com/channyeintun/gocode/internal/api"
	"github.com/channyeintun/gocode/internal/compact"
)

const (
	memoryRecallPressureBufferTokens   = 4_000
	continuationCompactBufferTokens    = 6_000
	continuationPressureBudgetPercent  = 75
	continuationPressureCountThreshold = 2
)

// ContextPressureDecision centralizes turn-level prompt pressure heuristics so
// memory recall, continuation, and proactive compaction cooperate.
type ContextPressureDecision struct {
	ConversationTokens    int
	EffectiveWindow       int
	WarningThreshold      int
	CompactThreshold      int
	ContinuationHot       bool
	ShouldCompact         bool
	SkipMemoryRecall      bool
	DelayOutputEscalation bool
}

func EvaluateContextPressure(messages []api.Message, contextWindow, maxOutputTokens int, continuation ContinuationTracker) ContextPressureDecision {
	effectiveWindow := compact.EffectiveContextWindow(contextWindow, maxOutputTokens)
	warningThreshold := compact.WarningThreshold(effectiveWindow)
	compactThreshold := compact.AutocompactThreshold(effectiveWindow)
	conversationTokens := compact.EstimateConversationTokens(messages)
	continuationHot := continuation.ContinuationCount >= continuationPressureCountThreshold
	if continuation.MaxBudgetTokens > 0 && continuation.BudgetUsedTokens*100 >= continuation.MaxBudgetTokens*continuationPressureBudgetPercent {
		continuationHot = true
	}
	if continuationHot && compactThreshold > 0 {
		compactThreshold -= continuationCompactBufferTokens
		if compactThreshold < 0 {
			compactThreshold = 0
		}
	}

	decision := ContextPressureDecision{
		ConversationTokens: conversationTokens,
		EffectiveWindow:    effectiveWindow,
		WarningThreshold:   warningThreshold,
		CompactThreshold:   compactThreshold,
		ContinuationHot:    continuationHot,
	}

	if compactThreshold > 0 && conversationTokens >= compactThreshold {
		decision.ShouldCompact = true
	}

	recallThreshold := warningThreshold - memoryRecallPressureBufferTokens
	if recallThreshold < 0 {
		recallThreshold = 0
	}

	if conversationTokens >= recallThreshold && (warningThreshold > 0 || continuationHot) {
		decision.SkipMemoryRecall = true
	}
	if decision.ShouldCompact {
		decision.SkipMemoryRecall = true
	}
	if decision.ShouldCompact || (continuationHot && warningThreshold > 0 && conversationTokens >= recallThreshold) {
		decision.DelayOutputEscalation = true
	}

	return decision
}
