package agent

import (
	"strings"
	"time"

	"github.com/channyeintun/chan/internal/api"
	"github.com/channyeintun/chan/internal/compact"
)

const (
	memoryRecallPressureBufferTokens   = 4_000
	continuationCompactBufferTokens    = 6_000
	continuationPressureBudgetPercent  = 75
	continuationPressureCountThreshold = 2
	freshSessionCompactBufferTokens    = 3_000
	freshSessionRecallBufferTokens     = 1_500
	pendingChainBufferTokens           = 3_000
	fileFocusBufferTokens              = 2_000
	retryLoopCompactBufferTokens       = 2_500
	recentFileFocusThreshold           = 3
	recentMessagesWindow               = 6
	recentAttemptsWindow               = 3
)

type ContextPressureSignals struct {
	SessionMemory    SessionMemorySnapshot
	RetrievalTouched []string
	AttemptEntries   []AttemptEntry
}

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
	SkipLiveRetrieval     bool
	DelayOutputEscalation bool
	RetrievalBudgetTokens int
	FreshSessionMemory    bool
	PendingToolChain      bool
	RetryLoopHot          bool
	RecentFileFocus       bool
}

func EvaluateContextPressure(messages []api.Message, contextWindow, maxOutputTokens int, continuation ContinuationTracker, signals ContextPressureSignals) ContextPressureDecision {
	effectiveWindow := compact.EffectiveContextWindow(contextWindow, maxOutputTokens)
	warningThreshold := compact.WarningThreshold(effectiveWindow)
	compactThreshold := compact.AutocompactThreshold(effectiveWindow)
	conversationTokens := compact.EstimateConversationTokens(messages)
	continuationHot := continuation.ContinuationCount >= continuationPressureCountThreshold
	freshSessionMemory := signals.SessionMemory.IsFresh(time.Now())
	pendingToolChain := hasPendingToolChain(messages)
	retryLoopHot := hasRecentRetryLoop(signals.AttemptEntries)
	recentFileFocus := hasRecentFileFocus(messages, signals.RetrievalTouched)
	if continuation.MaxBudgetTokens > 0 && continuation.BudgetUsedTokens*100 >= continuation.MaxBudgetTokens*continuationPressureBudgetPercent {
		continuationHot = true
	}
	if continuationHot && compactThreshold > 0 {
		compactThreshold -= continuationCompactBufferTokens
		if compactThreshold < 0 {
			compactThreshold = 0
		}
	}
	if freshSessionMemory && compactThreshold > 0 {
		compactThreshold -= freshSessionCompactBufferTokens
		if compactThreshold < 0 {
			compactThreshold = 0
		}
	}
	if retryLoopHot && compactThreshold > 0 {
		compactThreshold -= retryLoopCompactBufferTokens
		if compactThreshold < 0 {
			compactThreshold = 0
		}
	}
	if pendingToolChain && !freshSessionMemory {
		compactThreshold += pendingChainBufferTokens
	}
	if recentFileFocus && !freshSessionMemory {
		compactThreshold += fileFocusBufferTokens
	}

	decision := ContextPressureDecision{
		ConversationTokens: conversationTokens,
		EffectiveWindow:    effectiveWindow,
		WarningThreshold:   warningThreshold,
		CompactThreshold:   compactThreshold,
		ContinuationHot:    continuationHot,
		FreshSessionMemory: freshSessionMemory,
		PendingToolChain:   pendingToolChain,
		RetryLoopHot:       retryLoopHot,
		RecentFileFocus:    recentFileFocus,
	}

	if compactThreshold > 0 && conversationTokens >= compactThreshold {
		decision.ShouldCompact = true
	}

	recallThreshold := warningThreshold - memoryRecallPressureBufferTokens
	if recallThreshold < 0 {
		recallThreshold = 0
	}
	if freshSessionMemory {
		recallThreshold -= freshSessionRecallBufferTokens
		if recallThreshold < 0 {
			recallThreshold = 0
		}
	}
	if (pendingToolChain || recentFileFocus) && !freshSessionMemory {
		recallThreshold += fileFocusBufferTokens / 2
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

	// Live retrieval shares the same skip gate as memory recall.
	decision.SkipLiveRetrieval = decision.SkipMemoryRecall

	// Compute the live retrieval token budget: shrink when context is warm.
	const baseRetrievalBudget = 3_000
	if !decision.SkipLiveRetrieval && effectiveWindow > 0 {
		budget := baseRetrievalBudget
		if conversationTokens*100/effectiveWindow > 50 {
			budget = baseRetrievalBudget / 2
		}
		decision.RetrievalBudgetTokens = budget
	}

	return decision
}

func hasPendingToolChain(messages []api.Message) bool {
	for index := len(messages) - 1; index >= 0 && len(messages)-index <= recentMessagesWindow; index-- {
		message := messages[index]
		switch message.Role {
		case api.RoleUser:
			return false
		case api.RoleTool:
			return true
		case api.RoleAssistant:
			if len(message.ToolCalls) > 0 {
				return true
			}
			if strings.TrimSpace(message.Content) != "" {
				return false
			}
		}
	}
	return false
}

func hasRecentRetryLoop(entries []AttemptEntry) bool {
	if len(entries) == 0 {
		return false
	}
	start := 0
	if len(entries) > recentAttemptsWindow {
		start = len(entries) - recentAttemptsWindow
	}
	recent := entries[start:]
	seen := map[string]int{}
	commandErrors := map[string]int{}
	blockedPaths := map[string]int{}
	for _, entry := range recent {
		if entry.DoNotRetry {
			return true
		}
		signature := retryLoopSignature(entry)
		if signature != "" {
			seen[signature]++
			if seen[signature] >= 2 {
				return true
			}
		}
		command := strings.TrimSpace(entry.Command)
		errorSignature := strings.TrimSpace(entry.ErrorSignature)
		blockedPath := strings.TrimSpace(entry.BlockedPath)
		if command != "" && errorSignature != "" {
			commandErrors[command+"|"+errorSignature]++
			if commandErrors[command+"|"+errorSignature] >= 2 {
				return true
			}
		}
		if blockedPath != "" {
			blockedPaths[blockedPath]++
			if blockedPaths[blockedPath] >= 2 {
				return true
			}
		}
	}
	return false
}

func retryLoopSignature(entry AttemptEntry) string {
	parts := make([]string, 0, 3)
	if command := strings.TrimSpace(entry.Command); command != "" {
		parts = append(parts, command)
	}
	if signature := strings.TrimSpace(entry.ErrorSignature); signature != "" {
		parts = append(parts, signature)
	}
	if path := strings.TrimSpace(entry.BlockedPath); path != "" {
		parts = append(parts, path)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "|")
}

func hasRecentFileFocus(messages []api.Message, retrievalTouched []string) bool {
	if len(retrievalTouched) >= recentFileFocusThreshold {
		return true
	}
	toolNamesByID := make(map[string]string)
	for _, message := range messages {
		for _, toolCall := range message.ToolCalls {
			toolNamesByID[toolCall.ID] = toolCall.Name
		}
	}
	count := 0
	for index := len(messages) - 1; index >= 0 && len(messages)-index <= recentMessagesWindow; index-- {
		message := messages[index]
		for _, call := range message.ToolCalls {
			if isFileFocusTool(call.Name) {
				count++
			}
		}
		if message.ToolResult != nil {
			if message.ToolResult.FilePath != "" {
				count++
				continue
			}
			if isFileFocusTool(toolNamesByID[message.ToolResult.ToolCallID]) {
				count++
			}
		}
		if count >= recentFileFocusThreshold {
			return true
		}
	}
	return false
}

func isFileFocusTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read_file", "file_search", "grep_search", "apply_patch", "create_file", "replace_string_in_file", "multi_replace_string_in_file":
		return true
	default:
		return false
	}
}
