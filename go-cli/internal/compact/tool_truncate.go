package compact

import "github.com/channyeintun/go-cli/internal/api"

// CompactableTools lists tools whose old results can be safely truncated.
var CompactableTools = map[string]bool{
	"FileRead":  true,
	"Bash":      true,
	"Grep":      true,
	"Glob":      true,
	"WebSearch": true,
	"WebFetch":  true,
	"FileEdit":  true,
	"FileWrite": true,
}

const truncatedMarker = "[Old tool result content cleared]"

// TruncateToolResults replaces old tool results with a short marker.
// Only truncates results from compactable tools, preserving the most recent
// tool result of each type.
func TruncateToolResults(messages []api.Message) []api.Message {
	// Find the last occurrence index for each tool result
	lastSeen := make(map[string]int)
	for i, msg := range messages {
		if msg.ToolResult != nil {
			lastSeen[msg.ToolResult.ToolCallID] = i
		}
	}

	result := make([]api.Message, len(messages))
	copy(result, messages)

	for i, msg := range result {
		if msg.Role != api.RoleTool || msg.ToolResult == nil {
			continue
		}
		// Don't truncate the most recent results
		if i == lastSeen[msg.ToolResult.ToolCallID] {
			continue
		}
		// Only truncate compactable tool results
		if msg.Content != "" && len(msg.Content) > 200 {
			result[i].Content = truncatedMarker
		}
	}

	return result
}
