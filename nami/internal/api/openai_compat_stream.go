package api

import (
	"encoding/json"
	"fmt"
	"strings"
)

func (c *OpenAICompatClient) handleEvent(
	data string,
	state *openAICompatStreamState,
	yield func(ModelEvent, error) bool,
) error {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return nil
	}
	if trimmed == "[DONE]" {
		return state.emitStop(yield)
	}

	var chunk openAICompatChunk
	if err := json.Unmarshal([]byte(trimmed), &chunk); err != nil {
		return fmt.Errorf("decode OpenAI-compatible stream chunk: %w", err)
	}
	if chunk.Error != nil {
		message := openAICompatErrorMessage(*chunk.Error)
		return &APIError{
			Type:    classifyOpenAICompatErrorType(0, chunk.Error.Type, message),
			Message: message,
		}
	}
	// Usage can arrive on the finish chunk, on its own chunk after it, or as a
	// running total on every chunk, so it is reported once, by emitStop.
	state.usage.merge(chunk.Usage)

	for _, choice := range chunk.Choices {
		if reasoning := firstNonEmpty(choice.Delta.Reasoning, choice.Delta.ReasoningContent); reasoning != "" {
			if !yield(ModelEvent{Type: ModelEventThinking, Text: reasoning}, nil) {
				return errStopStream
			}
		}
		if choice.Delta.Content != "" {
			if !yield(ModelEvent{Type: ModelEventToken, Text: choice.Delta.Content}, nil) {
				return errStopStream
			}
		}
		if choice.Delta.Refusal != "" {
			if !yield(ModelEvent{Type: ModelEventToken, Text: choice.Delta.Refusal}, nil) {
				return errStopStream
			}
		}

		for _, toolCall := range choice.Delta.ToolCalls {
			state.applyToolCallDelta(toolCall)
		}
		if choice.Delta.FunctionCall != nil {
			state.applyLegacyFunctionDelta(*choice.Delta.FunctionCall)
		}

		if choice.FinishReason != "" {
			mapped := mapOpenAICompatStopReason(choice.FinishReason)
			if mapped == "tool_use" {
				if err := state.emitToolCalls(yield); err != nil {
					return err
				}
			}
			state.stopReason = mapped
		}
	}

	return nil
}

func (s *openAICompatStreamState) applyToolCallDelta(delta openAICompatDeltaToolCall) {
	state := s.toolCallState(delta.Index)
	if delta.ID != "" {
		state.ID = delta.ID
	}
	if delta.Type != "" {
		state.Type = delta.Type
	}
	if delta.Function.Name != "" {
		state.Name = delta.Function.Name
	}
	if delta.Function.Arguments != "" {
		state.Arguments.WriteString(delta.Function.Arguments)
	}
}

func (s *openAICompatStreamState) applyLegacyFunctionDelta(delta openAICompatFunctionCall) {
	state := s.toolCallState(0)
	if state.Type == "" {
		state.Type = "function"
	}
	if delta.Name != "" {
		state.Name = delta.Name
	}
	if delta.Arguments != "" {
		state.Arguments.WriteString(delta.Arguments)
	}
}

func (s *openAICompatStreamState) toolCallState(index int) *openAICompatToolCallState {
	state, ok := s.toolCalls[index]
	if ok {
		return state
	}
	state = &openAICompatToolCallState{}
	s.toolCalls[index] = state
	return state
}

func (s *openAICompatStreamState) emitToolCalls(yield func(ModelEvent, error) bool) error {
	for index, toolCall := range s.toolCalls {
		if toolCall.ID == "" {
			toolCall.ID = fmt.Sprintf("call_%d", index)
		}
		arguments := toolCall.Arguments.String()
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		if _, err := decodeToolInput(arguments); err != nil {
			return fmt.Errorf("decode OpenAI-compatible tool input: %w", err)
		}
		if !yield(ModelEvent{
			Type: ModelEventToolCall,
			ToolCall: &ToolCall{
				ID:    toolCall.ID,
				Name:  toolCall.Name,
				Input: arguments,
			},
		}, nil) {
			return errStopStream
		}
		delete(s.toolCalls, index)
	}
	return nil
}

func (s *openAICompatStreamState) emitStop(yield func(ModelEvent, error) bool) error {
	if s.sentStop {
		return nil
	}
	if len(s.toolCalls) > 0 {
		if err := s.emitToolCalls(yield); err != nil {
			return err
		}
		if s.stopReason == "" {
			s.stopReason = "tool_use"
		}
	}
	if s.stopReason == "" {
		s.stopReason = "end_turn"
	}
	s.sentStop = true
	if s.usage != (openAICompatUsage{}) {
		if !yield(ModelEvent{Type: ModelEventUsage, Usage: s.usage.toUsage()}, nil) {
			return errStopStream
		}
	}
	if !yield(ModelEvent{Type: ModelEventStop, StopReason: s.stopReason}, nil) {
		return errStopStream
	}
	return errStopStream
}
