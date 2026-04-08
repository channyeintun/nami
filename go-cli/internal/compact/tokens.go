package compact

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
)

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
