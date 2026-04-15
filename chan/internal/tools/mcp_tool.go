package tools

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	mcppkg "github.com/channyeintun/chan/internal/mcp"
)

const mcpToolPrefix = "mcp__"

type MCPTool struct {
	registeredName string
	serverName     string
	toolName       string
	workingDir     string
	description    string
	inputSchema    any
	permission     PermissionLevel
	manager        *mcppkg.Manager
}

func NewMCPTool(manager *mcppkg.Manager, descriptor mcppkg.DiscoveredTool) *MCPTool {
	return &MCPTool{
		registeredName: MCPToolName(descriptor.ServerName, descriptor.Tool.Name),
		serverName:     descriptor.ServerName,
		toolName:       descriptor.Tool.Name,
		workingDir:     descriptor.WorkingDir,
		description:    buildMCPToolDescription(descriptor),
		inputSchema:    normalizeMCPToolSchema(descriptor.Tool.InputSchema),
		permission:     mcpPermissionLevel(descriptor.Permission),
		manager:        manager,
	}
}

func MCPToolName(serverName, toolName string) string {
	return mcpToolPrefix + sanitizeMCPToolComponent(serverName) + "__" + sanitizeMCPToolComponent(toolName)
}

func (t *MCPTool) Name() string {
	return t.registeredName
}

func (t *MCPTool) Description() string {
	return t.description
}

func (t *MCPTool) InputSchema() any {
	return t.inputSchema
}

func (t *MCPTool) Permission() PermissionLevel {
	return t.permission
}

func (t *MCPTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *MCPTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	if t.manager == nil {
		return ToolOutput{}, fmt.Errorf("mcp manager is unavailable")
	}
	result, err := t.manager.CallTool(ctx, t.serverName, t.toolName, input.Params)
	if err != nil {
		return ToolOutput{}, err
	}
	rendered, err := result.Render()
	if err != nil {
		return ToolOutput{}, err
	}
	if result.IsError && strings.TrimSpace(rendered) == "" {
		rendered = fmt.Sprintf("MCP tool %s/%s returned an error with no details", t.serverName, t.toolName)
	}
	return ToolOutput{Output: rendered, IsError: result.IsError}, nil
}

func (t *MCPTool) PermissionTarget(input ToolInput) PermissionTarget {
	return PermissionTarget{
		Kind:       "mcp_tool",
		Value:      t.serverName + "/" + t.toolName,
		WorkingDir: t.workingDir,
	}
}

func buildMCPToolDescription(descriptor mcppkg.DiscoveredTool) string {
	description := strings.TrimSpace(descriptor.Tool.Description)
	if description == "" {
		description = fmt.Sprintf("Call the MCP tool %q exposed by server %q.", descriptor.Tool.Name, descriptor.ServerName)
	}
	return fmt.Sprintf("%s\n\nMCP server: %s.", description, descriptor.ServerName)
}

func normalizeMCPToolSchema(schema any) any {
	if schemaMap, ok := schema.(map[string]any); ok && len(schemaMap) > 0 {
		return schemaMap
	}
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func mcpPermissionLevel(permission mcppkg.ToolPermission) PermissionLevel {
	switch permission {
	case mcppkg.ToolPermissionRead:
		return PermissionReadOnly
	case mcppkg.ToolPermissionWrite:
		return PermissionWrite
	default:
		return PermissionExecute
	}
}

func sanitizeMCPToolComponent(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "unnamed"
	}
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range trimmed {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(unicode.ToLower(r))
			lastUnderscore = false
		case r == '-' || r == '_':
			builder.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				builder.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	result := strings.Trim(builder.String(), "_-")
	if result == "" {
		return "unnamed"
	}
	return result
}
