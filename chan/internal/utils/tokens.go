package utils

import "github.com/channyeintun/gocode/internal/api"

// EstimateTokens estimates token count for a string (~4 chars per token).
func EstimateTokens(text string) int {
	return len(text) / 4
}

// EstimateMessageTokens estimates tokens for a single message.
func EstimateMessageTokens(msg api.Message) int {
	tokens := EstimateTokens(msg.Content)
	for _, tc := range msg.ToolCalls {
		tokens += EstimateTokens(tc.Input) + 20 // overhead for tool call structure
	}
	if msg.ToolResult != nil {
		tokens += EstimateTokens(msg.ToolResult.Output) + 10
	}
	return tokens + 4 // per-message overhead
}

// EstimateConversationTokens estimates total tokens across all messages.
func EstimateConversationTokens(messages []api.Message) int {
	total := 0
	for _, msg := range messages {
		total += EstimateMessageTokens(msg)
	}
	return total
}
