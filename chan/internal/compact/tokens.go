package compact

import "github.com/channyeintun/gocode/internal/api"

// Token counting and compaction thresholds.

const (
	// AutocompactBufferTokens is the buffer before the context window limit.
	AutocompactBufferTokens = 13_000

	// WarningThresholdBufferTokens triggers a UI warning before hard threshold.
	WarningThresholdBufferTokens = 20_000

	// ManualCompactBufferTokens is the buffer for /compact command.
	ManualCompactBufferTokens = 3_000

	// MaxConsecutiveFailures is the circuit breaker for auto-compaction.
	MaxConsecutiveFailures = 3

	// MaxReservedOutputTokens mirrors the reference implementation's reserve so
	// compaction can still produce output before the context window is exhausted.
	MaxReservedOutputTokens = 20_000
)

// EffectiveContextWindow reserves room for model output before applying any
// warning or compaction thresholds.
func EffectiveContextWindow(contextWindow, maxOutputTokens int) int {
	reserved := maxOutputTokens
	if reserved < 0 {
		reserved = 0
	}
	if reserved > MaxReservedOutputTokens {
		reserved = MaxReservedOutputTokens
	}
	effective := contextWindow - reserved
	if effective < 0 {
		return 0
	}
	return effective
}

// AutocompactThreshold returns the token count that triggers auto-compaction.
func AutocompactThreshold(contextWindow int) int {
	return contextWindow - AutocompactBufferTokens
}

// WarningThreshold returns the token count that triggers a warning.
func WarningThreshold(contextWindow int) int {
	return contextWindow - WarningThresholdBufferTokens
}

// EstimateTokens provides a rough token count (~4 chars per token).
func EstimateTokens(text string) int {
	return len(text) / 4
}

// EstimateMessagesTokens estimates total tokens across messages.
func EstimateMessagesTokens(messages []string) int {
	total := 0
	for _, m := range messages {
		total += EstimateTokens(m)
	}
	return total
}

// EstimateConversationTokens estimates total tokens across API conversation messages.
func EstimateConversationTokens(messages []api.Message) int {
	total := 0
	for _, message := range messages {
		total += EstimateTokens(message.Content)
		for _, call := range message.ToolCalls {
			total += EstimateTokens(call.Name)
			total += EstimateTokens(call.Input)
		}
		if message.ToolResult != nil {
			total += EstimateTokens(message.ToolResult.Output)
		}
	}
	return total
}
