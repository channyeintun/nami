package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	mcppkg "github.com/channyeintun/nami/internal/mcp"
)

type mcpManagerRuntime struct {
	mu      sync.RWMutex
	manager *mcppkg.Manager
}

var globalMCPManagerRuntime mcpManagerRuntime

type ListMCPResourcesTool struct{}

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

func SetGlobalMCPManager(manager *mcppkg.Manager) {
	globalMCPManagerRuntime.mu.Lock()
	defer globalMCPManagerRuntime.mu.Unlock()
	globalMCPManagerRuntime.manager = manager
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
	manager, err := getGlobalMCPManager()
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
