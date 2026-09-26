package api

// interruptedToolOutput is the result recorded for a tool call that never
// returned one.
const interruptedToolOutput = "The tool call was interrupted before it returned a result."

// answerUnansweredToolCalls returns messages with an error result added for
// every tool call that was never answered, placed before the conversation
// moves on. A turn cancelled while its tools ran is saved with the
// assistant's calls but without their results, and Anthropic, OpenAI and the
// Responses API reject any request in which a tool call is left unanswered,
// which would fail every later request in the session.
func answerUnansweredToolCalls(messages []Message) []Message {
	answered := make([]Message, 0, len(messages))
	var pending []ToolCall
	resolved := make(map[string]bool)

	closeRound := func() {
		for _, call := range pending {
			if resolved[call.ID] {
				continue
			}
			answered = append(answered, Message{
				Role:       RoleTool,
				ToolResult: &ToolResult{ToolCallID: call.ID, Output: interruptedToolOutput, IsError: true},
			})
		}
		pending = nil
		clear(resolved)
	}

	for _, msg := range messages {
		if msg.ToolResult != nil {
			resolved[msg.ToolResult.ToolCallID] = true
		} else {
			closeRound()
		}
		answered = append(answered, msg)
		if msg.Role == RoleAssistant {
			pending = msg.ToolCalls
		}
	}
	closeRound()
	return answered
}
