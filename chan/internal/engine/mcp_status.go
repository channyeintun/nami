package engine

import (
	"fmt"
	"strings"

	mcppkg "github.com/channyeintun/chan/internal/mcp"
)

func formatMCPStatusText(manager *mcppkg.Manager) string {
	if manager == nil {
		return ""
	}
	statuses := manager.Statuses()
	if len(statuses) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("MCP Servers:")
	for _, status := range statuses {
		builder.WriteString("\n")
		builder.WriteString(formatSingleMCPStatus(status))
	}
	return builder.String()
}

func formatSingleMCPStatus(status mcppkg.ServerStatus) string {
	transport := strings.TrimSpace(status.Transport)
	if transport == "" {
		transport = "unknown"
	}
	if !status.Enabled {
		return fmt.Sprintf("- %s (%s): disabled", status.Name, transport)
	}
	if strings.TrimSpace(status.Error) != "" {
		return fmt.Sprintf("- %s (%s): error: %s", status.Name, transport, status.Error)
	}
	if !status.Connected {
		return fmt.Sprintf("- %s (%s): not connected", status.Name, transport)
	}

	line := fmt.Sprintf(
		"- %s (%s): connected, %d tools, %d prompts, %d resources, %d templates",
		status.Name,
		transport,
		status.ToolCount,
		status.PromptCount,
		status.ResourceCount,
		status.ResourceTemplateCount,
	)
	if len(status.Warnings) > 0 {
		line += fmt.Sprintf(" [warnings: %s]", strings.Join(status.Warnings, "; "))
	}
	return line
}
