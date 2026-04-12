package utils

import (
	"strings"

	"github.com/channyeintun/gocode/internal/api"
)

// NormalizeMessages prepares messages for API submission:
// - Consolidates consecutive same-role messages
// - Strips trailing whitespace
// - Ensures tool results are paired with tool calls
func NormalizeMessages(messages []api.Message) []api.Message {
	if len(messages) == 0 {
		return messages
	}

	var result []api.Message

	for _, msg := range messages {
		msg.Content = strings.TrimRight(msg.Content, " \t\n")

		// Consolidate consecutive messages with the same role (except tool)
		if len(result) > 0 && result[len(result)-1].Role == msg.Role &&
			msg.Role != api.RoleTool && len(msg.ToolCalls) == 0 &&
			len(result[len(result)-1].ToolCalls) == 0 &&
			len(msg.Images) == 0 && len(result[len(result)-1].Images) == 0 {
			result[len(result)-1].Content += "\n" + msg.Content
			continue
		}
		result = append(result, msg)
	}

	return result
}
