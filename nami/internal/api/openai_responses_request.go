package api

import (
	"fmt"
	"maps"
	"strings"
)

func (c *OpenAIResponsesClient) buildRequest(req ModelRequest) (openAIResponsesRequest, map[string]string, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = c.capabilities.MaxOutputTokens
	}

	input, err := buildOpenAIResponsesInput(req.SystemPrompt, req.Messages, openAIResponsesUsesDeveloperRole(c.model))
	if err != nil {
		return openAIResponsesRequest{}, nil, err
	}

	payload := openAIResponsesRequest{
		Model:           c.model,
		Input:           input,
		Tools:           buildOpenAIResponsesTools(req.Tools),
		MaxOutputTokens: maxTokens,
		Temperature:     req.Temperature,
		Stream:          true,
		Store:           false,
	}
	if c.provider == "codex" {
		payload.MaxOutputTokens = 0
	}
	if effort := ClampReasoningEffort(c.model, req.ReasoningEffort); effort != "" {
		payload.Reasoning = &openAIResponsesReasoning{
			Effort:  effort,
			Summary: "auto",
		}
		payload.Include = []string{"reasoning.encrypted_content"}
	}

	var extraHeaders map[string]string
	if c.provider == "github-copilot" {
		extraHeaders = GitHubCopilotStaticHeaders()
		maps.Copy(extraHeaders, BuildGitHubCopilotDynamicHeaders(req.Messages))
	} else if c.provider == "codex" {
		extraHeaders = CodexStaticHeaders(c.resolveCodexAccountID())
	}

	return payload, extraHeaders, nil
}

func buildOpenAIResponsesInput(systemPrompt string, messages []Message, developerRole bool) ([]map[string]any, error) {
	items := make([]map[string]any, 0, len(messages)+1)
	if trimmed := strings.TrimSpace(systemPrompt); trimmed != "" {
		role := "system"
		if developerRole {
			role = "developer"
		}
		items = append(items, map[string]any{
			"role":    role,
			"content": trimmed,
		})
	}

	assistantIndex := 0
	toolIndex := 0
	for _, msg := range answerUnansweredToolCalls(messages) {
		trimmed := strings.TrimSpace(msg.Content)
		switch msg.Role {
		case RoleSystem:
			if trimmed == "" {
				continue
			}
			role := "system"
			if developerRole {
				role = "developer"
			}
			items = append(items, map[string]any{
				"role":    role,
				"content": trimmed,
			})
		case RoleUser:
			content := make([]map[string]any, 0, len(msg.Images)+1)
			if trimmed != "" {
				content = append(content, map[string]any{
					"type": "input_text",
					"text": trimmed,
				})
			}
			for _, image := range msg.Images {
				content = append(content, map[string]any{
					"type":      "input_image",
					"detail":    "auto",
					"image_url": fmt.Sprintf("data:%s;base64,%s", image.MediaType, image.Data),
				})
			}
			if len(content) > 0 {
				items = append(items, map[string]any{
					"role":    "user",
					"content": content,
				})
			}
			if msg.ToolResult != nil {
				items = append(items, openAIResponsesToolResultItem(*msg.ToolResult))
			}
		case RoleTool:
			if msg.ToolResult == nil {
				continue
			}
			items = append(items, openAIResponsesToolResultItem(*msg.ToolResult))
		case RoleAssistant:
			if trimmed != "" {
				items = append(items, map[string]any{
					"type":   "message",
					"role":   "assistant",
					"status": "completed",
					"id":     fmt.Sprintf("msg_%d", assistantIndex),
					"content": []map[string]any{{
						"type":        "output_text",
						"text":        trimmed,
						"annotations": []any{},
					}},
				})
				assistantIndex++
			}
			for _, toolCall := range msg.ToolCalls {
				arguments, err := sanitizeOpenAIResponsesToolArguments(toolCall.Input)
				if err != nil {
					arguments = "{}"
				}
				items = append(items, map[string]any{
					"type":      "function_call",
					"id":        fmt.Sprintf("fc_%d", toolIndex),
					"call_id":   toolCall.ID,
					"name":      toolCall.Name,
					"arguments": arguments,
				})
				toolIndex++
			}
		}
	}

	return items, nil
}

func openAIResponsesToolResultItem(result ToolResult) map[string]any {
	output := result.Output
	if result.IsError {
		output = "ERROR: " + output
	}
	return map[string]any{
		"type":    "function_call_output",
		"call_id": result.ToolCallID,
		"output":  output,
	}
}

func buildOpenAIResponsesTools(tools []ToolDefinition) []openAIResponsesToolDefinition {
	if len(tools) == 0 {
		return nil
	}

	built := make([]openAIResponsesToolDefinition, 0, len(tools))
	for _, tool := range tools {
		built = append(built, openAIResponsesToolDefinition{
			Type:        "function",
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  sanitizeOpenAIToolSchema(tool.InputSchema),
			Strict:      false,
		})
	}
	return built
}
