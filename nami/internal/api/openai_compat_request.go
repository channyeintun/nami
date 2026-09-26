package api

import (
	"fmt"
	"maps"
	"strings"
)

func (c *OpenAICompatClient) buildRequest(req ModelRequest) (openAICompatRequest, map[string]string, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = c.capabilities.MaxOutputTokens
	}

	messages, err := buildOpenAICompatMessages(req.SystemPrompt, req.Messages)
	if err != nil {
		return openAICompatRequest{}, nil, err
	}

	payload := openAICompatRequest{
		Model:       c.model,
		Messages:    messages,
		Tools:       buildOpenAICompatTools(req.Tools),
		Stream:      true,
		Stop:        req.StopSequences,
		Temperature: req.Temperature,
	}
	// OpenAI rejects max_tokens for its reasoning models, the GPT-5 series
	// included, and accepts max_completion_tokens for every model; the other
	// compatible servers document max_tokens. OpenAI also streams no usage
	// unless the request asks for it.
	if c.provider == "openai" {
		payload.MaxCompletionTokens = maxTokens
		payload.StreamOptions = &openAICompatStreamOptions{IncludeUsage: true}
	} else {
		payload.MaxTokens = maxTokens
	}

	var extraHeaders map[string]string
	if c.provider == "github-copilot" {
		extraHeaders = GitHubCopilotStaticHeaders()
		maps.Copy(extraHeaders, BuildGitHubCopilotDynamicHeaders(req.Messages))
	}

	return payload, extraHeaders, nil
}

func buildOpenAICompatMessages(systemPrompt string, messages []Message) ([]openAICompatMessage, error) {
	systemParts := make([]string, 0, len(messages)+1)
	if trimmed := strings.TrimSpace(systemPrompt); trimmed != "" {
		systemParts = append(systemParts, trimmed)
	}

	built := make([]openAICompatMessage, 0, len(messages)+1)
	for _, msg := range messages {
		if msg.Role == RoleSystem {
			if trimmed := strings.TrimSpace(msg.Content); trimmed != "" {
				systemParts = append(systemParts, trimmed)
			}
			continue
		}

		converted, err := convertOpenAICompatMessage(msg)
		if err != nil {
			return nil, err
		}
		built = append(built, converted...)
	}

	if len(systemParts) > 0 {
		built = append([]openAICompatMessage{{Role: "system", Content: strings.Join(systemParts, "\n\n")}}, built...)
	}

	return built, nil
}

func convertOpenAICompatMessage(msg Message) ([]openAICompatMessage, error) {
	trimmed := strings.TrimSpace(msg.Content)
	converted := make([]openAICompatMessage, 0, 2)

	switch msg.Role {
	case RoleUser:
		if trimmed != "" || len(msg.Images) > 0 {
			content := buildOpenAICompatUserContent(trimmed, msg.Images)
			converted = append(converted, openAICompatMessage{Role: "user", Content: content})
		}
		if msg.ToolResult != nil {
			converted = append(converted, openAICompatToolResultMessage(*msg.ToolResult))
		}
	case RoleTool:
		if msg.ToolResult != nil {
			converted = append(converted, openAICompatToolResultMessage(*msg.ToolResult))
		} else if trimmed != "" {
			converted = append(converted, openAICompatMessage{Role: "tool", Content: trimmed})
		}
	case RoleAssistant:
		assistant := openAICompatMessage{Role: "assistant"}
		if trimmed != "" {
			assistant.Content = trimmed
		}
		if len(msg.ToolCalls) > 0 {
			assistant.ToolCalls = make([]openAICompatToolCall, 0, len(msg.ToolCalls))
			for _, toolCall := range msg.ToolCalls {
				assistantCall, err := buildOpenAICompatAssistantToolCall(toolCall)
				if err != nil {
					return nil, err
				}
				assistant.ToolCalls = append(assistant.ToolCalls, assistantCall)
			}
		}
		if assistant.Content != nil || len(assistant.ToolCalls) > 0 {
			converted = append(converted, assistant)
		}
	default:
		if trimmed != "" {
			converted = append(converted, openAICompatMessage{Role: string(msg.Role), Content: trimmed})
		}
	}

	return converted, nil
}

func buildOpenAICompatUserContent(text string, images []ImageAttachment) any {
	if len(images) == 0 {
		return text
	}

	parts := make([]map[string]any, 0, len(images)+1)
	if text != "" {
		parts = append(parts, map[string]any{
			"type": "text",
			"text": text,
		})
	}

	for _, image := range images {
		parts = append(parts, map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": fmt.Sprintf("data:%s;base64,%s", image.MediaType, image.Data),
			},
		})
	}

	return parts
}

func openAICompatToolResultMessage(result ToolResult) openAICompatMessage {
	content := result.Output
	if result.IsError {
		content = "ERROR: " + content
	}
	return openAICompatMessage{
		Role:       "tool",
		Content:    content,
		ToolCallID: result.ToolCallID,
	}
}

func buildOpenAICompatAssistantToolCall(toolCall ToolCall) (openAICompatToolCall, error) {
	arguments := toolCall.Input
	if strings.TrimSpace(arguments) == "" {
		arguments = "{}"
	}
	if _, err := decodeToolInput(arguments); err != nil {
		return openAICompatToolCall{}, err
	}
	return openAICompatToolCall{
		ID:   toolCall.ID,
		Type: "function",
		Function: openAICompatFunctionCall{
			Name:      toolCall.Name,
			Arguments: arguments,
		},
	}, nil
}

func buildOpenAICompatTools(tools []ToolDefinition) []openAICompatToolDefinition {
	if len(tools) == 0 {
		return nil
	}

	built := make([]openAICompatToolDefinition, 0, len(tools))
	for _, tool := range tools {
		built = append(built, openAICompatToolDefinition{
			Type: "function",
			Function: openAICompatFunctionDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  sanitizeOpenAIToolSchema(tool.InputSchema),
			},
		})
	}
	return built
}
