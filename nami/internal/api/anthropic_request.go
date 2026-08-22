package api

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
)

func (c *AnthropicClient) buildRequest(req ModelRequest) (anthropicRequest, map[string]string, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = c.capabilities.MaxOutputTokens
	}

	systemPrompt, messages, err := buildAnthropicMessages(req.SystemPrompt, req.Messages)
	if err != nil {
		return anthropicRequest{}, nil, err
	}
	tools := buildAnthropicTools(req.Tools, c.provider == "github-copilot", c.capabilities.SupportsCaching)
	if c.capabilities.SupportsCaching {
		applyAnthropicCacheControl(systemPrompt, messages)
	}

	payload := anthropicRequest{
		Model:         c.model,
		System:        systemPrompt,
		Messages:      messages,
		Tools:         tools,
		MaxTokens:     maxTokens,
		Stream:        true,
		StopSequences: req.StopSequences,
	}

	var extraHeaders map[string]string
	if c.provider == "github-copilot" {
		extraHeaders = GitHubCopilotStaticHeaders()
		maps.Copy(extraHeaders, BuildGitHubCopilotDynamicHeaders(req.Messages))
	}

	if req.ThinkingBudget > 0 {
		payload.Thinking = &anthropicThinking{Type: "enabled", BudgetTokens: req.ThinkingBudget}
	} else if req.Temperature != nil {
		payload.Temperature = req.Temperature
	}

	return payload, extraHeaders, nil
}

func buildAnthropicMessages(systemPrompt string, messages []Message) ([]anthropicTextBlock, []anthropicMessage, error) {
	system := make([]anthropicTextBlock, 0, len(messages)+1)
	if trimmed := strings.TrimSpace(systemPrompt); trimmed != "" {
		system = append(system, anthropicTextBlock{Type: "text", Text: trimmed})
	}

	built := make([]anthropicMessage, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == RoleSystem {
			if trimmed := strings.TrimSpace(msg.Content); trimmed != "" {
				system = append(system, anthropicTextBlock{Type: "text", Text: trimmed})
			}
			continue
		}

		converted, skip, err := convertAnthropicMessage(msg)
		if err != nil {
			return nil, nil, err
		}
		if skip {
			continue
		}
		built = append(built, converted)
	}

	return system, built, nil
}

func convertAnthropicMessage(msg Message) (anthropicMessage, bool, error) {
	trimmed := strings.TrimSpace(msg.Content)
	blocks := make([]map[string]any, 0, 1+len(msg.ToolCalls)+len(msg.Images))

	switch msg.Role {
	case RoleUser:
		if msg.ToolResult != nil {
			blocks = append(blocks, toolResultBlock(*msg.ToolResult))
		}
		if trimmed != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": trimmed})
		}
		for _, image := range msg.Images {
			blocks = append(blocks, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": image.MediaType,
					"data":       image.Data,
				},
			})
		}
		if len(blocks) == 0 {
			return anthropicMessage{}, true, nil
		}
		return anthropicMessage{Role: "user", Content: blocks}, false, nil
	case RoleTool:
		if msg.ToolResult == nil {
			if trimmed == "" {
				return anthropicMessage{}, true, nil
			}
			return anthropicMessage{Role: "user", Content: []map[string]any{{"type": "text", "text": trimmed}}}, false, nil
		}
		blocks = append(blocks, toolResultBlock(*msg.ToolResult))
		return anthropicMessage{Role: "user", Content: blocks}, false, nil
	case RoleAssistant:
		if trimmed != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": trimmed})
		}
		for _, toolCall := range msg.ToolCalls {
			input, err := decodeToolInput(toolCall.Input)
			if err != nil {
				return anthropicMessage{}, false, err
			}
			blocks = append(blocks, map[string]any{"type": "tool_use", "id": toolCall.ID, "name": toolCall.Name, "input": input})
		}
		if len(blocks) == 0 {
			return anthropicMessage{}, true, nil
		}
		return anthropicMessage{Role: "assistant", Content: blocks}, false, nil
	default:
		if trimmed != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": trimmed})
		}
		if len(blocks) == 0 {
			return anthropicMessage{}, true, nil
		}
		return anthropicMessage{Role: string(msg.Role), Content: blocks}, false, nil
	}
}

func toolResultBlock(result ToolResult) map[string]any {
	block := map[string]any{"type": "tool_result", "tool_use_id": result.ToolCallID, "content": result.Output}
	if result.IsError {
		block["is_error"] = true
	}
	return block
}

func decodeToolInput(input string) (any, error) {
	if strings.TrimSpace(input) == "" {
		return map[string]any{}, nil
	}

	var decoded any
	if err := json.Unmarshal([]byte(input), &decoded); err != nil {
		return nil, fmt.Errorf("decode tool input JSON: %w", err)
	}
	return decoded, nil
}

func buildAnthropicTools(tools []ToolDefinition, flattenTopLevelCombinators bool, enablePromptCaching bool) []anthropicToolDefinition {
	if len(tools) == 0 {
		return nil
	}

	sorted := append([]ToolDefinition(nil), tools...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	built := make([]anthropicToolDefinition, 0, len(sorted))
	for _, tool := range sorted {
		built = append(built, anthropicToolDefinition{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: normalizeAnthropicToolSchema(tool.InputSchema, flattenTopLevelCombinators),
		})
	}
	if enablePromptCaching {
		built[len(built)-1].CacheControl = defaultAnthropicCacheControl()
	}
	return built
}

func applyAnthropicCacheControl(system []anthropicTextBlock, messages []anthropicMessage) {
	if len(system) > 0 {
		system[len(system)-1].CacheControl = defaultAnthropicCacheControl()
	}
	applyAnthropicMessageCacheControl(messages)
}

func applyAnthropicMessageCacheControl(messages []anthropicMessage) {
	for _, message := range slices.Backward(messages) {
		blocks, ok := message.Content.([]map[string]any)
		if !ok || len(blocks) == 0 {
			continue
		}
		blocks[len(blocks)-1]["cache_control"] = defaultAnthropicCacheControl()
		return
	}
}

func defaultAnthropicCacheControl() *anthropicCacheControl {
	return &anthropicCacheControl{Type: "ephemeral"}
}

func normalizeAnthropicToolSchema(schema any, flattenTopLevelCombinators bool) any {
	if !flattenTopLevelCombinators {
		return schema
	}
	root, ok := schema.(map[string]any)
	if !ok {
		return schema
	}
	if len(schemaOptionList(root["oneOf"])) == 0 && len(schemaOptionList(root["anyOf"])) == 0 && len(schemaOptionList(root["allOf"])) == 0 {
		return schema
	}

	clone := make(map[string]any, len(root))
	maps.Copy(clone, root)
	delete(clone, "oneOf")
	delete(clone, "anyOf")
	delete(clone, "allOf")

	description := strings.TrimSpace(stringValue(clone["description"]))
	note := "Provide arguments using the documented properties. Top-level schema alternatives were flattened for GitHub Copilot compatibility."
	if description == "" {
		clone["description"] = note
	} else {
		clone["description"] = description + " " + note
	}
	return clone
}

func schemaOptionList(value any) []map[string]any {
	items, ok := value.([]map[string]any)
	if ok {
		return items
	}
	rawItems, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]map[string]any, 0, len(rawItems))
	for _, item := range rawItems {
		entry, ok := item.(map[string]any)
		if ok {
			result = append(result, entry)
		}
	}
	return result
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
