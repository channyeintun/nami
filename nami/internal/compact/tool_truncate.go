package compact

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/channyeintun/nami/internal/api"
)

// CompactableTools lists tools whose old results can be safely truncated.
var CompactableTools = map[string]bool{
	"apply_patch":                  true,
	"create_file":                  true,
	"read_file":                    true,
	"read_project_structure":       true,
	"bash":                         true,
	"grep_search":                  true,
	"file_search":                  true,
	"web_search":                   true,
	"web_fetch":                    true,
	"replace_string_in_file":       true,
	"multi_replace_string_in_file": true,
	"file_write":                   true,
}

// TruncateToolResults replaces old tool results with a short marker.
// Only truncates results from compactable tools, preserving the most recent
// tool result of each type.
func TruncateToolResults(messages []api.Message) []api.Message {
	toolCallsByID := make(map[string]api.ToolCall)
	for _, msg := range messages {
		for _, toolCall := range msg.ToolCalls {
			toolCallsByID[toolCall.ID] = toolCall
		}
	}

	// Find the last occurrence index for each compactable tool type and resource.
	lastSeen := make(map[string]int)
	for i, msg := range messages {
		if msg.ToolResult == nil {
			continue
		}
		toolCall := toolCallsByID[msg.ToolResult.ToolCallID]
		toolName := canonicalCompactableToolName(toolCall.Name)
		if !CompactableTools[toolName] {
			continue
		}
		lastSeen[compactKey(toolName, toolCall, msg.ToolResult)] = i
	}

	result := make([]api.Message, len(messages))
	copy(result, messages)

	for i, msg := range result {
		if msg.Role != api.RoleTool || msg.ToolResult == nil {
			continue
		}
		toolCall := toolCallsByID[msg.ToolResult.ToolCallID]
		toolName := canonicalCompactableToolName(toolCall.Name)
		if !CompactableTools[toolName] {
			continue
		}
		key := compactKey(toolName, toolCall, msg.ToolResult)
		// Don't truncate the most recent results
		if i == lastSeen[key] {
			continue
		}
		if len(msg.Content) > 200 || len(msg.ToolResult.Output) > 200 {
			toolResultCopy := *result[i].ToolResult
			marker := summarizeCompactedResult(toolName, toolCall, toolResultCopy)
			toolResultCopy.Output = marker
			result[i].Content = marker
			result[i].ToolResult = &toolResultCopy
		}
	}

	return result
}

func compactKey(toolName string, toolCall api.ToolCall, result *api.ToolResult) string {
	resource := compactResource(toolName, toolCall, result)
	if resource == "" {
		return toolName
	}
	return toolName + "::" + resource
}

func summarizeCompactedResult(toolName string, toolCall api.ToolCall, result api.ToolResult) string {
	parts := []string{fmt.Sprintf("Older %s output cleared", toolName)}
	if resource := compactResource(toolName, toolCall, &result); resource != "" {
		parts = append(parts, resource)
	}
	if result.IsError {
		parts = append(parts, "status: error")
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

func compactResource(toolName string, toolCall api.ToolCall, result *api.ToolResult) string {
	if result != nil && strings.TrimSpace(result.FilePath) != "" {
		return "file: " + strings.TrimSpace(result.FilePath)
	}
	parsed := parseToolInput(toolCall.Input)
	switch toolName {
	case "read_file", "create_file", "file_write", "replace_string_in_file", "multi_replace_string_in_file", "apply_patch":
		if path := firstStringField(parsed, "filePath", "path"); path != "" {
			return "file: " + path
		}
	case "grep_search", "file_search", "read_project_structure":
		if include := firstStringField(parsed, "includePattern", "query", "path"); include != "" {
			return "target: " + include
		}
	case "bash":
		if command := firstStringField(parsed, "command"); command != "" {
			return "command: " + compactSnippet(command, 120)
		}
		if raw := strings.TrimSpace(toolCall.Input); raw != "" {
			return "command: " + compactSnippet(raw, 120)
		}
	}
	return ""
}

func parseToolInput(raw string) map[string]any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return nil
	}
	return payload
}

func firstStringField(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if payload == nil {
			return ""
		}
		if value, ok := payload[key].(string); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func compactSnippet(value string, maxLen int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if maxLen <= 0 || len(value) <= maxLen {
		return value
	}
	return strings.TrimSpace(value[:maxLen])
}

func canonicalCompactableToolName(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	switch normalized {
	case "applypatch", "apply_patch":
		return "apply_patch"
	case "fileread", "file_read", "read_file":
		return "read_file"
	case "readprojectstructure", "read_project_structure":
		return "read_project_structure"
	case "createfile", "create_file":
		return "create_file"
	case "bash":
		return "bash"
	case "grep", "grepsearch", "grep_search":
		return "grep_search"
	case "glob", "filesearch", "file_search":
		return "file_search"
	case "websearch", "web_search":
		return "web_search"
	case "webfetch", "web_fetch":
		return "web_fetch"
	case "fileedit", "file_edit", "replacestringinfile", "replace_string_in_file":
		return "replace_string_in_file"
	case "multireplacestringinfile", "multi_replace_string_in_file":
		return "multi_replace_string_in_file"
	case "filewrite", "file_write":
		return "file_write"
	default:
		return normalized
	}
}
