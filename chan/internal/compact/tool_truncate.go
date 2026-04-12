package compact

import (
	"strings"

	"github.com/channyeintun/chan/internal/api"
)

// CompactableTools lists tools whose old results can be safely truncated.
var CompactableTools = map[string]bool{
	"apply_patch": true,
	"create_file": true,
	"file_read":   true,
	"bash":        true,
	"grep":        true,
	"glob":        true,
	"web_search":  true,
	"web_fetch":   true,
	"file_edit":   true,
	"file_write":  true,
}

const truncatedMarker = "[Old tool result content cleared]"

// TruncateToolResults replaces old tool results with a short marker.
// Only truncates results from compactable tools, preserving the most recent
// tool result of each type.
func TruncateToolResults(messages []api.Message) []api.Message {
	toolNamesByCallID := make(map[string]string)
	for _, msg := range messages {
		for _, toolCall := range msg.ToolCalls {
			toolNamesByCallID[toolCall.ID] = toolCall.Name
		}
	}

	// Find the last occurrence index for each compactable tool type.
	lastSeen := make(map[string]int)
	for i, msg := range messages {
		if msg.ToolResult == nil {
			continue
		}
		toolName := canonicalCompactableToolName(toolNamesByCallID[msg.ToolResult.ToolCallID])
		if !CompactableTools[toolName] {
			continue
		}
		lastSeen[toolName] = i
	}

	result := make([]api.Message, len(messages))
	copy(result, messages)

	for i, msg := range result {
		if msg.Role != api.RoleTool || msg.ToolResult == nil {
			continue
		}
		toolName := canonicalCompactableToolName(toolNamesByCallID[msg.ToolResult.ToolCallID])
		if !CompactableTools[toolName] {
			continue
		}
		// Don't truncate the most recent results
		if i == lastSeen[toolName] {
			continue
		}
		if len(msg.Content) > 200 || len(msg.ToolResult.Output) > 200 {
			toolResultCopy := *result[i].ToolResult
			toolResultCopy.Output = truncatedMarker
			result[i].Content = truncatedMarker
			result[i].ToolResult = &toolResultCopy
		}
	}

	return result
}

func canonicalCompactableToolName(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	switch normalized {
	case "applypatch", "apply_patch":
		return "apply_patch"
	case "fileread", "file_read", "read_file":
		return "file_read"
	case "createfile", "create_file":
		return "create_file"
	case "bash":
		return "bash"
	case "grep", "grepsearch", "grep_search":
		return "grep"
	case "glob", "filesearch", "file_search":
		return "glob"
	case "websearch", "web_search":
		return "web_search"
	case "webfetch", "web_fetch":
		return "web_fetch"
	case "fileedit", "file_edit":
		return "file_edit"
	case "filewrite", "file_write":
		return "file_write"
	default:
		return normalized
	}
}
