package compact

import (
	"strings"
	"testing"

	"github.com/channyeintun/nami/internal/api"
)

func TestEstimateConversationTokensCountsToolOutputOnce(t *testing.T) {
	// The agent loop records a tool result in both Content and
	// ToolResult.Output. Counting both made tool-heavy conversations look
	// twice their size, so compaction ran at half the intended fill.
	output := strings.Repeat("x", 4_000)
	messages := []api.Message{
		{Role: api.RoleUser, Content: strings.Repeat("u", 400)},
		{Role: api.RoleAssistant, ToolCalls: []api.ToolCall{{ID: "call_1", Name: "read_file", Input: `{"filePath":"main.go"}`}}},
		{Role: api.RoleTool, Content: output, ToolResult: &api.ToolResult{ToolCallID: "call_1", Output: output}},
	}

	want := EstimateTokens(messages[0].Content) +
		EstimateTokens("read_file") + EstimateTokens(`{"filePath":"main.go"}`) +
		EstimateTokens(output)
	if got := EstimateConversationTokens(messages); got != want {
		t.Fatalf("EstimateConversationTokens = %d, want %d", got, want)
	}
}
