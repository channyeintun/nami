package agent

import (
	"slices"
	"strings"

	"github.com/channyeintun/nami/internal/api"
)

func latestUserPrompt(messages []api.Message) string {
	for _, message := range slices.Backward(messages) {
		if message.Role == api.RoleUser {
			return message.Content
		}
	}
	return ""
}

func latestAssistantMessage(messages []api.Message) api.Message {
	for _, message := range slices.Backward(messages) {
		if message.Role == api.RoleAssistant {
			return message
		}
	}
	return api.Message{Role: api.RoleAssistant}
}

func latestToolOutput(messages []api.Message) string {
	var builder strings.Builder
	for _, msg := range slices.Backward(messages) {
		if msg.Role != api.RoleTool {
			break
		}
		if msg.ToolResult != nil && strings.TrimSpace(msg.ToolResult.Output) != "" {
			builder.WriteString(msg.ToolResult.Output)
			builder.WriteString("\n")
		}
	}
	return builder.String()
}
