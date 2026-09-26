package api

import (
	"encoding/json"
	"fmt"
	"strings"
)

func (c *OpenAIResponsesClient) handleEvent(data string, state *openAIResponsesStreamState, yield func(ModelEvent, error) bool) error {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return nil
	}
	if trimmed == "[DONE]" {
		return state.emitStop("end_turn", yield)
	}

	var envelope openAIResponsesEnvelope
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		return fmt.Errorf("decode OpenAI Responses event: %w", err)
	}

	switch envelope.Type {
	case "response.created", "response.reasoning_summary_part.added", "response.content_part.added":
		return nil
	case "response.output_item.added":
		return state.handleOutputItemAdded(trimmed)
	case "response.reasoning_summary_text.delta":
		var evt openAIResponsesReasoningDeltaEvent
		if err := json.Unmarshal([]byte(trimmed), &evt); err != nil {
			return fmt.Errorf("decode OpenAI Responses reasoning delta: %w", err)
		}
		if evt.Delta == "" {
			return nil
		}
		state.sawReasoningText = true
		if !yield(ModelEvent{Type: ModelEventThinking, Text: evt.Delta}, nil) {
			return errStopStream
		}
		return nil
	case "response.reasoning_summary_part.done":
		if !state.sawReasoningText {
			return nil
		}
		if !yield(ModelEvent{Type: ModelEventThinking, Text: "\n\n"}, nil) {
			return errStopStream
		}
		return nil
	case "response.output_text.delta":
		var evt openAIResponsesTextDeltaEvent
		if err := json.Unmarshal([]byte(trimmed), &evt); err != nil {
			return fmt.Errorf("decode OpenAI Responses text delta: %w", err)
		}
		if evt.Delta == "" {
			return nil
		}
		state.currentText.WriteString(evt.Delta)
		state.sawContentText = true
		if !yield(ModelEvent{Type: ModelEventToken, Text: evt.Delta}, nil) {
			return errStopStream
		}
		return nil
	case "response.output_text.done":
		var evt openAIResponsesOutputTextDoneEvent
		if err := json.Unmarshal([]byte(trimmed), &evt); err != nil {
			return fmt.Errorf("decode OpenAI Responses output text done: %w", err)
		}
		if evt.Text == "" {
			return nil
		}
		streamed := state.currentText.String()
		if streamed == "" {
			state.currentText.WriteString(evt.Text)
			state.sawContentText = true
			if !yield(ModelEvent{Type: ModelEventToken, Text: evt.Text}, nil) {
				return errStopStream
			}
		} else if strings.HasPrefix(evt.Text, streamed) {
			suffix := evt.Text[len(streamed):]
			if suffix != "" {
				state.currentText.WriteString(suffix)
				if !yield(ModelEvent{Type: ModelEventToken, Text: suffix}, nil) {
					return errStopStream
				}
			}
		}
		return nil
	case "response.refusal.delta":
		var evt openAIResponsesTextDeltaEvent
		if err := json.Unmarshal([]byte(trimmed), &evt); err != nil {
			return fmt.Errorf("decode OpenAI Responses refusal delta: %w", err)
		}
		if evt.Delta == "" {
			return nil
		}
		state.currentText.WriteString(evt.Delta)
		state.sawContentText = true
		if !yield(ModelEvent{Type: ModelEventToken, Text: evt.Delta}, nil) {
			return errStopStream
		}
		return nil
	case "response.function_call_arguments.delta":
		var evt openAIResponsesToolArgumentsDeltaEvent
		if err := json.Unmarshal([]byte(trimmed), &evt); err != nil {
			return fmt.Errorf("decode OpenAI Responses tool delta: %w", err)
		}
		state.appendToolArguments(evt.Delta)
		return nil
	case "response.function_call_arguments.done":
		var evt openAIResponsesToolArgumentsDoneEvent
		if err := json.Unmarshal([]byte(trimmed), &evt); err != nil {
			return fmt.Errorf("decode OpenAI Responses tool final arguments: %w", err)
		}
		state.setToolArguments(evt.Arguments)
		return nil
	case "response.output_item.done":
		return state.handleOutputItemDone(trimmed, yield)
	case "response.completed", "response.incomplete", "response.done":
		var evt openAIResponsesCompletedEvent
		if err := json.Unmarshal([]byte(trimmed), &evt); err != nil {
			return fmt.Errorf("decode OpenAI Responses completed event: %w", err)
		}
		if !state.sawContentText && !state.sawToolCall {
			for _, item := range evt.Response.Output {
				if item.Type != "message" {
					continue
				}
				text := strings.TrimSpace(joinOpenAIResponsesMessageContent(item.Content))
				if text == "" {
					continue
				}
				state.sawContentText = true
				if !yield(ModelEvent{Type: ModelEventToken, Text: text}, nil) {
					return errStopStream
				}
				break
			}
		}
		usage := evt.Response.Usage.toUsage()
		if usage != nil {
			if !yield(ModelEvent{Type: ModelEventUsage, Usage: usage}, nil) {
				return errStopStream
			}
		}
		return state.emitStop(openAIResponsesStopReason(evt.Response.Status), yield)
	case "response.failed":
		var evt openAIResponsesFailedEvent
		if err := json.Unmarshal([]byte(trimmed), &evt); err != nil {
			return fmt.Errorf("decode OpenAI Responses failed event: %w", err)
		}
		message := strings.TrimSpace(evt.Response.Error.Message)
		if message == "" {
			message = strings.TrimSpace(evt.Response.IncompleteDetails.Reason)
		}
		if message == "" {
			message = "OpenAI Responses request failed"
		}
		return &APIError{
			Type:    classifyOpenAICompatErrorType(0, evt.Response.Error.Code, message),
			Message: message,
		}
	case "error":
		var evt openAIResponsesErrorEvent
		if err := json.Unmarshal([]byte(trimmed), &evt); err != nil {
			return fmt.Errorf("decode OpenAI Responses error event: %w", err)
		}
		message := strings.TrimSpace(evt.Message)
		if message == "" {
			message = "OpenAI Responses stream error"
		}
		return &APIError{
			Type:    classifyOpenAICompatErrorType(0, evt.Code, message),
			Message: message,
		}
	default:
		return nil
	}
}

func (s *openAIResponsesStreamState) handleOutputItemAdded(data string) error {
	var evt openAIResponsesOutputItemEvent
	if err := json.Unmarshal([]byte(data), &evt); err != nil {
		return fmt.Errorf("decode OpenAI Responses output item: %w", err)
	}
	if evt.Item.Type == "message" {
		s.currentText.Reset()
		return nil
	}
	if evt.Item.Type != "function_call" {
		return nil
	}
	tool := &openAIResponsesToolCallState{
		ID:   strings.TrimSpace(evt.Item.CallID),
		Name: strings.TrimSpace(evt.Item.Name),
	}
	tool.Arguments.WriteString(evt.Item.Arguments)
	s.currentTool = tool
	return nil
}

func (s *openAIResponsesStreamState) appendToolArguments(delta string) {
	if s.currentTool == nil || delta == "" {
		return
	}
	s.currentTool.Arguments.WriteString(delta)
}

func (s *openAIResponsesStreamState) setToolArguments(arguments string) {
	if s.currentTool == nil {
		return
	}
	s.currentTool.Arguments.Reset()
	s.currentTool.Arguments.WriteString(arguments)
}

func (s *openAIResponsesStreamState) handleOutputItemDone(data string, yield func(ModelEvent, error) bool) error {
	var evt openAIResponsesOutputItemEvent
	if err := json.Unmarshal([]byte(data), &evt); err != nil {
		return fmt.Errorf("decode OpenAI Responses output item done: %w", err)
	}
	if evt.Item.Type == "message" {
		return s.emitMessageSuffix(evt.Item, yield)
	}
	if evt.Item.Type == "reasoning" {
		if len(evt.Item.Summary) > 0 {
			s.sawReasoningText = true
		}
		return nil
	}
	if evt.Item.Type != "function_call" {
		return nil
	}

	tool := s.currentTool
	if tool == nil {
		tool = &openAIResponsesToolCallState{
			ID:   strings.TrimSpace(evt.Item.CallID),
			Name: strings.TrimSpace(evt.Item.Name),
		}
		tool.Arguments.WriteString(evt.Item.Arguments)
	}

	if tool.ID == "" {
		tool.ID = strings.TrimSpace(evt.Item.CallID)
	}
	if tool.ID == "" {
		tool.ID = strings.TrimSpace(evt.Item.ID)
	}
	if tool.Name == "" {
		tool.Name = strings.TrimSpace(evt.Item.Name)
	}

	arguments, err := resolveOpenAIResponsesToolArguments(tool.Arguments.String(), evt.Item.Arguments)
	if err != nil {
		return fmt.Errorf("decode OpenAI Responses tool input: %w", err)
	}

	s.currentTool = nil
	s.sawToolCall = true
	if !yield(ModelEvent{Type: ModelEventToolCall, ToolCall: &ToolCall{ID: tool.ID, Name: tool.Name, Input: arguments}}, nil) {
		return errStopStream
	}
	return nil
}

func (s *openAIResponsesStreamState) emitMessageSuffix(item openAIResponsesOutputItem, yield func(ModelEvent, error) bool) error {
	finalText := joinOpenAIResponsesMessageContent(item.Content)
	streamed := s.currentText.String()
	s.currentText.Reset()
	if strings.TrimSpace(finalText) == "" {
		return nil
	}

	// The deltas normally streamed the text already, so only what they missed
	// is emitted. The streamed text is compared with the final text exactly as
	// sent: trimming only the final text made any message with leading or
	// trailing whitespace look unstreamed, and it was emitted a second time.
	suffix := strings.TrimSpace(finalText)
	if streamed != "" && strings.HasPrefix(finalText, streamed) {
		suffix = finalText[len(streamed):]
	}
	if suffix == "" {
		return nil
	}
	s.sawContentText = true
	if !yield(ModelEvent{Type: ModelEventToken, Text: suffix}, nil) {
		return errStopStream
	}
	return nil
}

func resolveOpenAIResponsesToolArguments(streamed string, final string) (string, error) {
	candidates := []string{streamed}
	if final != streamed {
		candidates = append(candidates, final)
	}

	var lastErr error
	for _, candidate := range candidates {
		normalized, err := sanitizeOpenAIResponsesToolArguments(candidate)
		if err == nil {
			return normalized, nil
		}
		lastErr = err
	}
	return "", lastErr
}

func sanitizeOpenAIResponsesToolArguments(arguments string) (string, error) {
	normalized := strings.TrimSpace(arguments)
	if normalized == "" {
		normalized = "{}"
	}
	if _, err := decodeToolInput(normalized); err != nil {
		return "", err
	}
	return normalized, nil
}

func joinOpenAIResponsesMessageContent(content []openAIResponsesMessageContent) string {
	var builder strings.Builder
	for _, part := range content {
		switch part.Type {
		case "output_text":
			builder.WriteString(part.Text)
		case "refusal":
			builder.WriteString(part.Refusal)
		}
	}
	return builder.String()
}

func (s *openAIResponsesStreamState) emitStop(stopReason string, yield func(ModelEvent, error) bool) error {
	if s.sentStop {
		return nil
	}
	if s.sawToolCall {
		stopReason = "tool_use"
	}
	if stopReason == "" {
		stopReason = "end_turn"
	}
	s.sentStop = true
	if !yield(ModelEvent{Type: ModelEventStop, StopReason: stopReason}, nil) {
		return errStopStream
	}
	return errStopStream
}
