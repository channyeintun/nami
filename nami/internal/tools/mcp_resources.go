package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	mcppkg "github.com/channyeintun/nami/internal/mcp"
	"github.com/channyeintun/nami/internal/textutil"
)

type mcpManagerRuntime struct {
	mu      sync.RWMutex
	manager *mcppkg.Manager
}

var globalMCPManagerRuntime mcpManagerRuntime

type ListMCPResourcesTool struct {
	manager *mcppkg.Manager
}

type ReadMCPResourceTool struct {
	manager *mcppkg.Manager
}

type listMCPResourcesResponse struct {
	Servers []listMCPResourcesServer `json:"servers"`
}

type listMCPResourcesServer struct {
	Server            string                          `json:"server"`
	Connected         bool                            `json:"connected"`
	ResourcesCapable  bool                            `json:"resourcesCapable"`
	Resources         []mcpResourceDescriptor         `json:"resources"`
	ResourceTemplates []mcpResourceTemplateDescriptor `json:"resourceTemplates,omitempty"`
	Warnings          []string                        `json:"warnings,omitempty"`
	Error             string                          `json:"error,omitempty"`
}

type mcpResourceDescriptor struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mime_type,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

type mcpResourceTemplateDescriptor struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

type readMCPResourceResponse struct {
	Server   string                   `json:"server"`
	URI      string                   `json:"uri"`
	Contents []readMCPResourceContent `json:"contents"`
}

type readMCPResourceContent struct {
	Kind     string `json:"kind"`
	URI      string `json:"uri,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
	Text     string `json:"text,omitempty"`
	Size     int    `json:"size,omitempty"`
	Summary  string `json:"summary,omitempty"`
}

func getGlobalMCPManager() (*mcppkg.Manager, error) {
	globalMCPManagerRuntime.mu.RLock()
	defer globalMCPManagerRuntime.mu.RUnlock()
	if globalMCPManagerRuntime.manager == nil {
		return nil, fmt.Errorf("mcp manager is unavailable")
	}
	return globalMCPManagerRuntime.manager, nil
}

func NewListMCPResourcesTool() *ListMCPResourcesTool {
	return &ListMCPResourcesTool{}
}

func NewListMCPResourcesToolWithManager(manager *mcppkg.Manager) *ListMCPResourcesTool {
	return &ListMCPResourcesTool{manager: manager}
}

func NewReadMCPResourceTool() *ReadMCPResourceTool {
	return &ReadMCPResourceTool{}
}

func NewReadMCPResourceToolWithManager(manager *mcppkg.Manager) *ReadMCPResourceTool {
	return &ReadMCPResourceTool{manager: manager}
}

func (t *ListMCPResourcesTool) mcpManager() (*mcppkg.Manager, error) {
	if t != nil && t.manager != nil {
		return t.manager, nil
	}
	return getGlobalMCPManager()
}

func (t *ReadMCPResourceTool) mcpManager() (*mcppkg.Manager, error) {
	if t != nil && t.manager != nil {
		return t.manager, nil
	}
	return getGlobalMCPManager()
}

func (t *ListMCPResourcesTool) Name() string {
	return "list_mcp_resources"
}

func (t *ListMCPResourcesTool) Description() string {
	return "List MCP resources and resource templates exposed by connected servers so the model can discover non-tool MCP context."
}

func (t *ListMCPResourcesTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"server": map[string]any{
				"type":        "string",
				"description": "Optional MCP server name filter.",
			},
			"includeTemplates": map[string]any{
				"type":        "boolean",
				"description": "Whether resource templates should be included. Defaults to true.",
			},
			"include_templates": map[string]any{
				"type":        "boolean",
				"description": "Snake_case alias for includeTemplates.",
			},
		},
	}
}

func (t *ListMCPResourcesTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *ListMCPResourcesTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *ListMCPResourcesTool) Validate(input ToolInput) error {
	if server, ok := stringParam(input.Params, "server"); ok && strings.TrimSpace(server) == "" {
		return fmt.Errorf("list_mcp_resources server must not be empty")
	}
	return nil
}

func (t *ListMCPResourcesTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	manager, err := t.mcpManager()
	if err != nil {
		return ToolOutput{}, err
	}
	server, _ := stringParam(input.Params, "server")
	includeTemplates := true
	if firstBoolParam(input.Params, "includeTemplates", "include_templates") == false {
		if _, exists := firstParam(input.Params, "includeTemplates", "include_templates"); exists {
			includeTemplates = false
		}
	}
	response, err := manager.ResourceInventories(strings.TrimSpace(server), includeTemplates)
	if err != nil {
		return ToolOutput{}, err
	}

	output := listMCPResourcesResponse{Servers: make([]listMCPResourcesServer, 0, len(response))}
	for _, inventory := range response {
		select {
		case <-ctx.Done():
			return ToolOutput{}, ctx.Err()
		default:
		}
		entry := listMCPResourcesServer{
			Server:           inventory.ServerName,
			Connected:        inventory.Connected,
			ResourcesCapable: inventory.ResourcesCapable,
			Warnings:         append([]string(nil), inventory.Warnings...),
			Error:            strings.TrimSpace(inventory.Error),
			Resources:        make([]mcpResourceDescriptor, 0, len(inventory.Resources)),
		}
		for _, resource := range inventory.Resources {
			entry.Resources = append(entry.Resources, mcpResourceDescriptor{
				URI:         resource.URI,
				Name:        resource.Name,
				Title:       resource.Title,
				Description: resource.Description,
				MIMEType:    resource.MIMEType,
				Size:        resource.Size,
			})
		}
		if includeTemplates {
			entry.ResourceTemplates = make([]mcpResourceTemplateDescriptor, 0, len(inventory.ResourceTemplates))
			for _, template := range inventory.ResourceTemplates {
				entry.ResourceTemplates = append(entry.ResourceTemplates, mcpResourceTemplateDescriptor{
					URITemplate: template.URITemplate,
					Name:        template.Name,
					Title:       template.Title,
					Description: template.Description,
					MIMEType:    template.MIMEType,
				})
			}
		}
		output.Servers = append(output.Servers, entry)
	}

	encoded, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return ToolOutput{}, fmt.Errorf("marshal list_mcp_resources: %w", err)
	}
	return ToolOutput{Output: string(encoded)}, nil
}

func (t *ReadMCPResourceTool) Name() string {
	return "read_mcp_resource"
}

func (t *ReadMCPResourceTool) Description() string {
	return "Read the contents of a specific MCP resource by server name and URI. Text content is returned inline; binary content is summarized safely."
}

func (t *ReadMCPResourceTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"server": map[string]any{
				"type":        "string",
				"description": "The MCP server name.",
			},
			"uri": map[string]any{
				"type":        "string",
				"description": "The resource URI to read.",
			},
			"maxBytes": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "Optional safety cap for returned text content.",
			},
			"max_bytes": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "Snake_case alias for maxBytes.",
			},
		},
		"required": []string{"server", "uri"},
	}
}

func (t *ReadMCPResourceTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *ReadMCPResourceTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *ReadMCPResourceTool) Validate(input ToolInput) error {
	server, ok := stringParam(input.Params, "server")
	if !ok || strings.TrimSpace(server) == "" {
		return fmt.Errorf("read_mcp_resource requires server")
	}
	uri, ok := stringParam(input.Params, "uri")
	if !ok || strings.TrimSpace(uri) == "" {
		return fmt.Errorf("read_mcp_resource requires uri")
	}
	if maxBytes, ok := firstIntParam(input.Params, "maxBytes", "max_bytes"); ok && maxBytes < 1 {
		return fmt.Errorf("read_mcp_resource maxBytes must be >= 1")
	}
	return nil
}

func (t *ReadMCPResourceTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	manager, err := t.mcpManager()
	if err != nil {
		return ToolOutput{}, err
	}
	server, _ := stringParam(input.Params, "server")
	uri, _ := stringParam(input.Params, "uri")
	maxBytes := firstPositiveIntOrDefault(input.Params, 32*1024, "maxBytes", "max_bytes")

	result, err := manager.ReadResource(ctx, server, uri)
	if err != nil {
		return ToolOutput{}, err
	}

	response := readMCPResourceResponse{
		Server:   result.ServerName,
		URI:      result.URI,
		Contents: make([]readMCPResourceContent, 0, len(result.Contents)),
	}
	for _, content := range result.Contents {
		select {
		case <-ctx.Done():
			return ToolOutput{}, ctx.Err()
		default:
		}
		response.Contents = append(response.Contents, summarizeReadMCPResourceContent(content, maxBytes))
	}

	encoded, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return ToolOutput{}, fmt.Errorf("marshal read_mcp_resource: %w", err)
	}
	return ToolOutput{Output: string(encoded)}, nil
}

func summarizeReadMCPResourceContent(content mcppkg.ResourceContent, maxBytes int) readMCPResourceContent {
	entry := readMCPResourceContent{
		URI:      content.URI,
		MIMEType: content.MIMEType,
	}
	if strings.TrimSpace(content.Text) != "" {
		entry.Kind = "text"
		entry.Text = clipMCPText(content.Text, maxBytes)
		entry.Size = len([]byte(content.Text))
		return entry
	}
	entry.Size = len(content.Blob)
	switch {
	case strings.HasPrefix(strings.ToLower(content.MIMEType), "image/"):
		entry.Kind = "image"
		entry.Summary = "Binary image content omitted from transcript"
	case strings.HasPrefix(strings.ToLower(content.MIMEType), "audio/"):
		entry.Kind = "audio"
		entry.Summary = "Binary audio content omitted from transcript"
	default:
		entry.Kind = "binary"
		entry.Summary = "Binary content omitted from transcript"
	}
	return entry
}

func clipMCPText(text string, maxBytes int) string {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text
	}
	return textutil.TruncateHead(text, maxBytes) + "\n\n[truncated]"
}
