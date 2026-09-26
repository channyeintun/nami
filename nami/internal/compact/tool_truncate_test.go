package compact

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/channyeintun/nami/internal/api"
)

func bashCall(id string, command string) api.ToolCall {
	input, _ := json.Marshal(map[string]string{"command": command})
	return api.ToolCall{ID: id, Name: "bash", Input: string(input)}
}

// The marker that replaces an old result quotes the command that produced it.
// Cutting that quote by byte count can land inside a multi-byte character and
// put invalid UTF-8 into the conversation sent to the provider.
func TestTruncateToolResultsQuotesCommandsOnRuneBoundaries(t *testing.T) {
	command := "echo " + strings.Repeat("日本", 40)
	output := strings.Repeat("x", 500)
	messages := []api.Message{
		{Role: api.RoleAssistant, ToolCalls: []api.ToolCall{bashCall("c1", command)}},
		{Role: api.RoleTool, Content: output, ToolResult: &api.ToolResult{ToolCallID: "c1", Output: output}},
		{Role: api.RoleAssistant, ToolCalls: []api.ToolCall{bashCall("c2", command)}},
		{Role: api.RoleTool, Content: output, ToolResult: &api.ToolResult{ToolCallID: "c2", Output: output}},
	}

	compacted := TruncateToolResults(messages)

	marker := compacted[1].Content
	if marker == output {
		t.Fatal("the older result was not cleared")
	}
	if !utf8.ValidString(marker) || !utf8.ValidString(compacted[1].ToolResult.Output) {
		t.Fatalf("marker is not valid UTF-8: %q", marker)
	}
	if compacted[3].Content != output {
		t.Fatal("the most recent result must be kept")
	}
	// Clearing a result must never drop the result message itself, or the tool
	// call before it would be left unpaired.
	if len(compacted) != len(messages) || compacted[1].ToolResult.ToolCallID != "c1" {
		t.Fatalf("compaction changed the message structure: %+v", compacted)
	}
}

func TestCompactSnippetStaysWithinLimitOnRuneBoundary(t *testing.T) {
	snippet := compactSnippet("echo "+strings.Repeat("日", 60), 120)
	if !utf8.ValidString(snippet) {
		t.Fatalf("compactSnippet split a rune: %q", snippet)
	}
	if len(snippet) > 120 {
		t.Fatalf("compactSnippet returned %d bytes, limit 120", len(snippet))
	}
}
