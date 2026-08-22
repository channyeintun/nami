package api

import (
	"encoding/json"
	"slices"
	"strings"
)

func (c *GeminiClient) buildRequest(req ModelRequest) (geminiGenerateContentRequest, error) {
	contents, systemInstruction, err := buildGeminiContents(c.model, req.SystemPrompt, req.Messages)
	if err != nil {
		return geminiGenerateContentRequest{}, err
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = c.capabilities.MaxOutputTokens
	}

	payload := geminiGenerateContentRequest{
		Contents:          contents,
		SystemInstruction: systemInstruction,
		Tools:             buildGeminiTools(req.Tools),
		GenerationConfig: geminiGenerationConfig{
			MaxOutputTokens: maxTokens,
			Temperature:     req.Temperature,
			StopSequences:   req.StopSequences,
		},
	}

	return payload, nil
}

func geminiContentIsOnlyFunctionResponses(c geminiContent) bool {
	if len(c.Parts) == 0 {
		return false
	}
	for _, p := range c.Parts {
		if p.FunctionResponse == nil {
			return false
		}
	}
	return true
}

func buildGeminiContents(modelID, systemPrompt string, messages []Message) ([]geminiContent, *geminiContent, error) {
	systemParts := make([]geminiPart, 0, len(messages)+1)
	if trimmed := strings.TrimSpace(systemPrompt); trimmed != "" {
		systemParts = append(systemParts, geminiPart{Text: trimmed})
	}

	toolNames := make(map[string]string)
	for _, msg := range messages {
		for _, tc := range msg.ToolCalls {
			if tc.ID != "" && tc.Name != "" {
				toolNames[tc.ID] = tc.Name
			}
		}
	}

	contents := make([]geminiContent, 0, len(messages))
	type pendingCall struct{ id, name string }
	var pendingCalls []pendingCall
	pendingCallIDs := map[string]struct{}{}

	flushOrphanedCalls := func() {
		if len(pendingCalls) == 0 {
			return
		}
		for _, pc := range pendingCalls {
			if _, resolved := pendingCallIDs[pc.id]; resolved {
				continue
			}
			name := pc.name
			if name == "" {
				name = pc.id
			}
			part := geminiPart{
				FunctionResponse: &geminiFunctionResponse{
					ID:       pc.id,
					Name:     name,
					Response: map[string]any{"error": "No result provided"},
				},
			}
			if len(contents) > 0 {
				last := &contents[len(contents)-1]
				if last.Role == "user" && geminiContentIsOnlyFunctionResponses(*last) {
					last.Parts = append(last.Parts, part)
					continue
				}
			}
			contents = append(contents, geminiContent{Role: "user", Parts: []geminiPart{part}})
		}
		pendingCalls = pendingCalls[:0]
		pendingCallIDs = map[string]struct{}{}
	}

	for _, msg := range messages {
		if msg.Role == RoleSystem {
			if trimmed := strings.TrimSpace(msg.Content); trimmed != "" {
				systemParts = append(systemParts, geminiPart{Text: trimmed})
			}
			continue
		}

		isUserText := (msg.Role == RoleUser || msg.Role == RoleTool) && msg.ToolResult == nil && strings.TrimSpace(msg.Content) != ""
		if msg.Role == RoleAssistant || isUserText {
			flushOrphanedCalls()
		}

		if msg.Role == RoleAssistant && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					pendingCalls = append(pendingCalls, pendingCall{id: tc.ID, name: tc.Name})
				}
			}
		}

		if msg.ToolResult != nil && msg.ToolResult.ToolCallID != "" {
			pendingCallIDs[msg.ToolResult.ToolCallID] = struct{}{}
		}

		converted, err := convertGeminiMessage(msg, toolNames)
		if err != nil {
			return nil, nil, err
		}
		for _, c := range converted {
			if c.Role == "user" && geminiContentIsOnlyFunctionResponses(c) && len(contents) > 0 {
				last := &contents[len(contents)-1]
				if last.Role == "user" && geminiContentIsOnlyFunctionResponses(*last) {
					last.Parts = append(last.Parts, c.Parts...)
					continue
				}
			}
			contents = append(contents, c)
		}
	}

	var instruction *geminiContent
	if len(systemParts) > 0 {
		instruction = &geminiContent{Role: "system", Parts: systemParts}
	}
	contents = ensureGeminiActiveLoopThoughtSignatures(contents, modelID)
	return contents, instruction, nil
}

func ensureGeminiActiveLoopThoughtSignatures(contents []geminiContent, modelID string) []geminiContent {
	activeLoopStart := -1
	for index, content := range slices.Backward(contents) {
		if content.Role != "user" {
			continue
		}
		for _, part := range content.Parts {
			if strings.TrimSpace(part.Text) != "" {
				activeLoopStart = index
				break
			}
		}
		if activeLoopStart >= 0 {
			break
		}
	}

	if activeLoopStart < 0 {
		return contents
	}

	updated := make([]geminiContent, len(contents))
	copy(updated, contents)
	for index := activeLoopStart; index < len(updated); index++ {
		content := updated[index]
		if content.Role != "model" || len(content.Parts) == 0 {
			continue
		}
		parts := make([]geminiPart, len(content.Parts))
		copy(parts, content.Parts)
		for partIndex := range parts {
			if parts[partIndex].FunctionCall == nil {
				continue
			}
			if strings.TrimSpace(parts[partIndex].ThoughtSignature) == "" && geminiMajorVersion(modelID) >= 3 {
				parts[partIndex].ThoughtSignature = geminiSyntheticThoughtSignature
				updated[index] = geminiContent{Role: content.Role, Parts: parts}
			}
			break
		}
	}

	return updated
}

func convertGeminiMessage(msg Message, toolNames map[string]string) ([]geminiContent, error) {
	trimmed := strings.TrimSpace(msg.Content)
	parts := make([]geminiPart, 0, 1+len(msg.ToolCalls)+len(msg.Images))

	switch msg.Role {
	case RoleUser:
		if trimmed != "" {
			parts = append(parts, geminiPart{Text: trimmed})
		}
		for _, image := range msg.Images {
			parts = append(parts, geminiPart{InlineData: &geminiInlineData{MimeType: image.MediaType, Data: image.Data}})
		}
		if msg.ToolResult != nil {
			resultPart, err := geminiFunctionResponsePart(*msg.ToolResult, toolNames)
			if err != nil {
				return nil, err
			}
			parts = append(parts, resultPart)
		}
		if len(parts) == 0 {
			return nil, nil
		}
		return []geminiContent{{Role: "user", Parts: parts}}, nil
	case RoleTool:
		if msg.ToolResult == nil {
			if trimmed == "" {
				return nil, nil
			}
			return []geminiContent{{Role: "user", Parts: []geminiPart{{Text: trimmed}}}}, nil
		}
		resultPart, err := geminiFunctionResponsePart(*msg.ToolResult, toolNames)
		if err != nil {
			return nil, err
		}
		return []geminiContent{{Role: "user", Parts: []geminiPart{resultPart}}}, nil
	case RoleAssistant:
		if trimmed != "" {
			parts = append(parts, geminiPart{Text: trimmed})
		}
		for _, toolCall := range msg.ToolCalls {
			args, err := decodeToolInput(toolCall.Input)
			if err != nil {
				return nil, err
			}
			parts = append(parts, geminiPart{
				ThoughtSignature: toolCall.ThoughtSignature,
				FunctionCall: &geminiFunctionCall{
					ID:   toolCall.ID,
					Name: toolCall.Name,
					Args: args,
				},
			})
		}
		if len(parts) == 0 {
			return nil, nil
		}
		return []geminiContent{{Role: "model", Parts: parts}}, nil
	default:
		if trimmed == "" {
			return nil, nil
		}
		return []geminiContent{{Role: string(msg.Role), Parts: []geminiPart{{Text: trimmed}}}}, nil
	}
}

func geminiFunctionResponsePart(result ToolResult, toolNames map[string]string) (geminiPart, error) {
	response := map[string]any{}
	text := strings.TrimSpace(result.Output)
	if text != "" {
		var decoded any
		if err := json.Unmarshal([]byte(result.Output), &decoded); err == nil {
			if result.IsError {
				response["error"] = decoded
			} else {
				response["output"] = decoded
			}
		} else {
			if result.IsError {
				response["error"] = result.Output
			} else {
				response["output"] = result.Output
			}
		}
	}
	name := toolNames[result.ToolCallID]
	if name == "" {
		name = result.ToolCallID
	}
	return geminiPart{
		FunctionResponse: &geminiFunctionResponse{
			ID:       result.ToolCallID,
			Name:     name,
			Response: response,
		},
	}, nil
}

func buildGeminiTools(tools []ToolDefinition) []geminiTool {
	if len(tools) == 0 {
		return nil
	}

	decls := make([]geminiFunctionDeclaration, 0, len(tools))
	for _, tool := range tools {
		decls = append(decls, geminiFunctionDeclaration{
			Name:                 tool.Name,
			Description:          tool.Description,
			ParametersJsonSchema: sanitizeGeminiSchema(tool.InputSchema),
		})
	}

	return []geminiTool{{FunctionDeclarations: decls}}
}
