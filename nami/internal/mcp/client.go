package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type ToolDescriptor struct {
	Name         string
	Title        string
	Description  string
	InputSchema  any
	OutputSchema any
	ReadOnlyHint bool
	TitleHint    string
}

type PromptArgument struct {
	Name        string
	Title       string
	Description string
	Required    bool
}

type PromptDescriptor struct {
	Name        string
	Title       string
	Description string
	Arguments   []PromptArgument
}

type ResourceDescriptor struct {
	URI         string
	Name        string
	Title       string
	Description string
	MIMEType    string
	Size        int64
}

type ResourceTemplateDescriptor struct {
	URITemplate string
	Name        string
	Title       string
	Description string
	MIMEType    string
}

type ServerInfo struct {
	Name         string
	Title        string
	Version      string
	Instructions string
}

type ContentBlock struct {
	Kind        string         `json:"kind"`
	Text        string         `json:"text,omitempty"`
	URI         string         `json:"uri,omitempty"`
	Name        string         `json:"name,omitempty"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	MIMEType    string         `json:"mime_type,omitempty"`
	Size        int            `json:"size,omitempty"`
	Meta        map[string]any `json:"meta,omitempty"`
}

type CallResult struct {
	StructuredContent any
	Content           []ContentBlock
	IsError           bool
}

func (r CallResult) Render() (string, error) {
	if r.StructuredContent != nil {
		data, err := json.Marshal(r.StructuredContent)
		if err == nil {
			return string(data), nil
		}
	}

	if len(r.Content) == 0 {
		return "", nil
	}

	if text, ok := renderTextOnlyContent(r.Content); ok {
		return text, nil
	}

	payload := map[string]any{"content": r.Content}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal call result: %w", err)
	}
	return string(data), nil
}

type Session interface {
	ID() string
	ServerInfo() ServerInfo
	HasResourcesCapability() bool
	ListTools(context.Context) ([]ToolDescriptor, error)
	ListPrompts(context.Context) ([]PromptDescriptor, error)
	ListResources(context.Context) ([]ResourceDescriptor, error)
	ListResourceTemplates(context.Context) ([]ResourceTemplateDescriptor, error)
	CallTool(context.Context, string, any) (CallResult, error)
	Close() error
}

type sdkClientSession struct {
	session *sdkmcp.ClientSession
	info    ServerInfo
}

func NewClientSession(session *sdkmcp.ClientSession) Session {
	info := ServerInfo{}
	if session != nil && session.InitializeResult() != nil {
		initialize := session.InitializeResult()
		info.Instructions = strings.TrimSpace(initialize.Instructions)
		if initialize.ServerInfo != nil {
			info.Name = strings.TrimSpace(initialize.ServerInfo.Name)
			info.Title = strings.TrimSpace(initialize.ServerInfo.Title)
			info.Version = strings.TrimSpace(initialize.ServerInfo.Version)
		}
	}
	return &sdkClientSession{session: session, info: info}
}

func (s *sdkClientSession) ID() string {
	if s == nil || s.session == nil {
		return ""
	}
	return s.session.ID()
}

func (s *sdkClientSession) ServerInfo() ServerInfo {
	if s == nil {
		return ServerInfo{}
	}
	return s.info
}

func (s *sdkClientSession) HasResourcesCapability() bool {
	if s == nil || s.session == nil {
		return false
	}
	initialize := s.session.InitializeResult()
	return initialize != nil && initialize.Capabilities != nil && initialize.Capabilities.Resources != nil
}

func (s *sdkClientSession) ListTools(ctx context.Context) ([]ToolDescriptor, error) {
	result, err := s.session.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	tools := make([]ToolDescriptor, 0, len(result.Tools))
	for _, tool := range result.Tools {
		if tool == nil {
			continue
		}
		descriptor := ToolDescriptor{
			Name:         strings.TrimSpace(tool.Name),
			Title:        strings.TrimSpace(tool.Title),
			Description:  strings.TrimSpace(tool.Description),
			InputSchema:  tool.InputSchema,
			OutputSchema: tool.OutputSchema,
		}
		if tool.Annotations != nil {
			descriptor.ReadOnlyHint = tool.Annotations.ReadOnlyHint
			descriptor.TitleHint = strings.TrimSpace(tool.Annotations.Title)
		}
		tools = append(tools, descriptor)
	}
	return tools, nil
}

func (s *sdkClientSession) ListPrompts(ctx context.Context) ([]PromptDescriptor, error) {
	if initialize := s.session.InitializeResult(); initialize != nil && initialize.Capabilities != nil && initialize.Capabilities.Prompts == nil {
		return nil, nil
	}
	result, err := s.session.ListPrompts(ctx, nil)
	if err != nil {
		return nil, err
	}
	prompts := make([]PromptDescriptor, 0, len(result.Prompts))
	for _, prompt := range result.Prompts {
		if prompt == nil {
			continue
		}
		arguments := make([]PromptArgument, 0, len(prompt.Arguments))
		for _, argument := range prompt.Arguments {
			if argument == nil {
				continue
			}
			arguments = append(arguments, PromptArgument{
				Name:        strings.TrimSpace(argument.Name),
				Title:       strings.TrimSpace(argument.Title),
				Description: strings.TrimSpace(argument.Description),
				Required:    argument.Required,
			})
		}
		prompts = append(prompts, PromptDescriptor{
			Name:        strings.TrimSpace(prompt.Name),
			Title:       strings.TrimSpace(prompt.Title),
			Description: strings.TrimSpace(prompt.Description),
			Arguments:   arguments,
		})
	}
	return prompts, nil
}

func (s *sdkClientSession) ListResources(ctx context.Context) ([]ResourceDescriptor, error) {
	if initialize := s.session.InitializeResult(); initialize != nil && initialize.Capabilities != nil && initialize.Capabilities.Resources == nil {
		return nil, nil
	}
	result, err := s.session.ListResources(ctx, nil)
	if err != nil {
		return nil, err
	}
	resources := make([]ResourceDescriptor, 0, len(result.Resources))
	for _, resource := range result.Resources {
		if resource == nil {
			continue
		}
		resources = append(resources, ResourceDescriptor{
			URI:         strings.TrimSpace(resource.URI),
			Name:        strings.TrimSpace(resource.Name),
			Title:       strings.TrimSpace(resource.Title),
			Description: strings.TrimSpace(resource.Description),
			MIMEType:    strings.TrimSpace(resource.MIMEType),
			Size:        resource.Size,
		})
	}
	return resources, nil
}

func (s *sdkClientSession) ListResourceTemplates(ctx context.Context) ([]ResourceTemplateDescriptor, error) {
	if initialize := s.session.InitializeResult(); initialize != nil && initialize.Capabilities != nil && initialize.Capabilities.Resources == nil {
		return nil, nil
	}
	result, err := s.session.ListResourceTemplates(ctx, nil)
	if err != nil {
		return nil, err
	}
	templates := make([]ResourceTemplateDescriptor, 0, len(result.ResourceTemplates))
	for _, template := range result.ResourceTemplates {
		if template == nil {
			continue
		}
		templates = append(templates, ResourceTemplateDescriptor{
			URITemplate: strings.TrimSpace(template.URITemplate),
			Name:        strings.TrimSpace(template.Name),
			Title:       strings.TrimSpace(template.Title),
			Description: strings.TrimSpace(template.Description),
			MIMEType:    strings.TrimSpace(template.MIMEType),
		})
	}
	return templates, nil
}

func (s *sdkClientSession) CallTool(ctx context.Context, name string, arguments any) (CallResult, error) {
	result, err := s.session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      name,
		Arguments: arguments,
	})
	if err != nil {
		return CallResult{}, err
	}

	callResult := CallResult{
		StructuredContent: result.StructuredContent,
		IsError:           result.IsError,
		Content:           make([]ContentBlock, 0, len(result.Content)),
	}
	for _, block := range result.Content {
		contentBlock, err := convertContentBlock(block)
		if err != nil {
			return CallResult{}, err
		}
		callResult.Content = append(callResult.Content, contentBlock)
	}
	return callResult, nil
}

func (s *sdkClientSession) Close() error {
	if s == nil || s.session == nil {
		return nil
	}
	return s.session.Close()
}

func convertContentBlock(content sdkmcp.Content) (ContentBlock, error) {
	switch value := content.(type) {
	case *sdkmcp.TextContent:
		return ContentBlock{Kind: "text", Text: value.Text, Meta: mapOrNil(value.Meta)}, nil
	case *sdkmcp.ImageContent:
		return ContentBlock{Kind: "image", MIMEType: value.MIMEType, Size: len(value.Data), Meta: mapOrNil(value.Meta)}, nil
	case *sdkmcp.AudioContent:
		return ContentBlock{Kind: "audio", MIMEType: value.MIMEType, Size: len(value.Data), Meta: mapOrNil(value.Meta)}, nil
	case *sdkmcp.ResourceLink:
		return ContentBlock{
			Kind:        "resource_link",
			URI:         value.URI,
			Name:        value.Name,
			Title:       value.Title,
			Description: value.Description,
			MIMEType:    value.MIMEType,
			Meta:        mapOrNil(value.Meta),
		}, nil
	case *sdkmcp.EmbeddedResource:
		block := ContentBlock{Kind: "embedded_resource", Meta: mapOrNil(value.Meta)}
		if value.Resource != nil {
			block.URI = value.Resource.URI
			block.MIMEType = value.Resource.MIMEType
			block.Text = value.Resource.Text
			block.Size = len(value.Resource.Blob)
		}
		return block, nil
	default:
		payload, err := json.Marshal(content)
		if err != nil {
			return ContentBlock{}, fmt.Errorf("marshal unknown content block: %w", err)
		}
		return ContentBlock{Kind: "unknown", Text: string(payload)}, nil
	}
}

func mapOrNil(meta sdkmcp.Meta) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(meta))
	for key, value := range meta {
		cloned[key] = value
	}
	return cloned
}

func renderTextOnlyContent(blocks []ContentBlock) (string, bool) {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Kind != "text" {
			return "", false
		}
		parts = append(parts, block.Text)
	}
	return strings.Join(parts, "\n"), true
}
