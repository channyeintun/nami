package api

import (
	"testing"
)

// cancelledTurnHistory is what a session holds after the user cancels while
// the second of two tool calls runs, and then sends another prompt: the
// assistant's calls are saved, the second one's result never is.
func cancelledTurnHistory() []Message {
	return []Message{
		{Role: RoleUser, Content: "read both files"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{
			{ID: "call_1", Name: "read", Input: `{"path":"a.go"}`},
			{ID: "call_2", Name: "read", Input: `{"path":"b.go"}`},
		}},
		{Role: RoleTool, ToolResult: &ToolResult{ToolCallID: "call_1", Output: "package a"}},
		{Role: RoleUser, Content: "never mind, do something else"},
	}
}

func TestAnswerUnansweredToolCalls(t *testing.T) {
	answered := answerUnansweredToolCalls(cancelledTurnHistory())

	if len(answered) != 5 {
		t.Fatalf("messages = %+v, want one result added", answered)
	}
	added := answered[3]
	if added.Role != RoleTool || added.ToolResult == nil || added.ToolResult.ToolCallID != "call_2" || !added.ToolResult.IsError {
		t.Fatalf("added message = %+v, want an error result for call_2", added)
	}
	// The result has to come before the conversation moves on.
	if answered[4].Content != "never mind, do something else" {
		t.Fatalf("last message = %+v, want the new prompt after the added result", answered[4])
	}
}

func TestAnswerUnansweredToolCallsLeavesAnsweredHistoryAlone(t *testing.T) {
	history := []Message{
		{Role: RoleUser, Content: "read a.go"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_1", Name: "read", Input: `{}`}}},
		{Role: RoleTool, ToolResult: &ToolResult{ToolCallID: "call_1", Output: "package a"}},
		{Role: RoleAssistant, Content: "done"},
	}
	if got := answerUnansweredToolCalls(history); len(got) != len(history) {
		t.Fatalf("messages = %+v, want the history unchanged", got)
	}
}

func TestRequestsAnswerToolCallsLeftByACancelledTurn(t *testing.T) {
	history := cancelledTurnHistory()

	t.Run("anthropic", func(t *testing.T) {
		_, messages, err := buildAnthropicMessages("", history)
		if err != nil {
			t.Fatalf("buildAnthropicMessages: %v", err)
		}
		// The unanswered call's result must be in the user turn after the
		// tool_use, ahead of the new prompt.
		found := false
		for _, message := range messages[2:] {
			for _, block := range contentBlocks(t, message) {
				if block["type"] == "tool_result" && block["tool_use_id"] == "call_2" {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("messages = %+v, want a tool_result for call_2", messages)
		}
	})

	t.Run("openai-compatible", func(t *testing.T) {
		messages, err := buildOpenAICompatMessages("", history)
		if err != nil {
			t.Fatalf("buildOpenAICompatMessages: %v", err)
		}
		// assistant, tool call_1, tool call_2, then the new prompt.
		if len(messages) != 5 || messages[3].Role != "tool" || messages[3].ToolCallID != "call_2" || messages[4].Role != "user" {
			t.Fatalf("messages = %+v, want call_2 answered before the new prompt", messages)
		}
	})

	t.Run("responses", func(t *testing.T) {
		items, err := buildOpenAIResponsesInput("", history, false)
		if err != nil {
			t.Fatalf("buildOpenAIResponsesInput: %v", err)
		}
		outputs := map[any]bool{}
		for _, item := range items {
			if item["type"] == "function_call_output" {
				outputs[item["call_id"]] = true
			}
		}
		if !outputs["call_1"] || !outputs["call_2"] {
			t.Fatalf("items = %+v, want an output for both calls", items)
		}
	})
}
