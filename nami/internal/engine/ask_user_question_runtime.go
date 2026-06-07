package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/ipc"
	toolpkg "github.com/channyeintun/nami/internal/tools"
)

type askUserQuestionRuntime struct {
	bridge ipc.EventSink
	router *ipc.MessageRouter
}

func newAskUserQuestionRuntime(bridge ipc.EventSink, router *ipc.MessageRouter) toolpkg.AskUserQuestionRuntime {
	return &askUserQuestionRuntime{bridge: bridge, router: router}
}

func (r *askUserQuestionRuntime) Ask(ctx context.Context, request toolpkg.AskUserQuestionRequest) (toolpkg.AskUserQuestionResult, error) {
	requestID := fmt.Sprintf("question-%d", time.Now().UnixNano())
	payload := ipc.AskUserQuestionRequestedPayload{
		RequestID: requestID,
		Questions: make([]ipc.AskUserQuestionPromptPayload, 0, len(request.Questions)),
	}
	for _, question := range request.Questions {
		entry := ipc.AskUserQuestionPromptPayload{
			Header:        question.Header,
			Question:      question.Question,
			MultiSelect:   question.MultiSelect,
			AllowFreeform: question.AllowFreeform,
			Options:       make([]ipc.AskUserQuestionOptionPayload, 0, len(question.Options)),
		}
		for _, option := range question.Options {
			entry.Options = append(entry.Options, ipc.AskUserQuestionOptionPayload{
				Label:       option.Label,
				Value:       option.Value,
				Description: option.Description,
				Recommended: option.Recommended,
			})
		}
		payload.Questions = append(payload.Questions, entry)
	}
	if err := r.bridge.Emit(ipc.EventAskUserQuestionRequested, payload); err != nil {
		return toolpkg.AskUserQuestionResult{}, err
	}

	deferred := make([]ipc.ClientMessage, 0, 4)
	defer func() {
		r.router.Requeue(deferred...)
	}()

	for {
		msg, err := r.router.Next(ctx)
		if err != nil {
			return toolpkg.AskUserQuestionResult{}, err
		}
		switch msg.Type {
		case ipc.MsgAskUserQuestionResponse:
			var response ipc.AskUserQuestionResponsePayload
			if err := json.Unmarshal(msg.Payload, &response); err != nil {
				return toolpkg.AskUserQuestionResult{}, fmt.Errorf("decode ask_user_question response: %w", err)
			}
			if response.RequestID != requestID {
				deferred = append(deferred, msg)
				continue
			}
			return convertAskUserQuestionResponse(request, response)
		case ipc.MsgShutdown:
			return toolpkg.AskUserQuestionResult{}, context.Canceled
		default:
			deferred = append(deferred, msg)
		}
	}
}

func convertAskUserQuestionResponse(request toolpkg.AskUserQuestionRequest, response ipc.AskUserQuestionResponsePayload) (toolpkg.AskUserQuestionResult, error) {
	status := strings.TrimSpace(response.Status)
	if status == "" {
		status = "answered"
	}
	if status != "answered" && status != "declined" && status != "cancelled" {
		return toolpkg.AskUserQuestionResult{}, fmt.Errorf("invalid ask_user_question status %q", status)
	}
	if status != "answered" {
		return toolpkg.AskUserQuestionResult{Status: status, Answers: []toolpkg.AskUserQuestionAnswer{}}, nil
	}
	answersByHeader := make(map[string]ipc.AskUserQuestionAnswerPayload, len(response.Answers))
	for _, answer := range response.Answers {
		header := strings.TrimSpace(answer.Header)
		if header == "" {
			return toolpkg.AskUserQuestionResult{}, fmt.Errorf("ask_user_question response includes an answer without a header")
		}
		answersByHeader[header] = answer
	}
	answers := make([]toolpkg.AskUserQuestionAnswer, 0, len(request.Questions))
	for _, question := range request.Questions {
		answer, ok := answersByHeader[question.Header]
		if !ok {
			return toolpkg.AskUserQuestionResult{}, fmt.Errorf("ask_user_question response is missing answer for %q", question.Header)
		}
		selectedValues := normalizeAskUserQuestionAnswerValues(answer.SelectedValues)
		freeformText := strings.TrimSpace(answer.FreeformText)
		if !question.AllowFreeform && freeformText != "" {
			return toolpkg.AskUserQuestionResult{}, fmt.Errorf("ask_user_question response for %q included freeform text unexpectedly", question.Header)
		}
		if !question.MultiSelect && len(selectedValues) > 1 {
			return toolpkg.AskUserQuestionResult{}, fmt.Errorf("ask_user_question response for %q selected multiple values unexpectedly", question.Header)
		}
		for _, value := range selectedValues {
			if _, exists := question.NormalizedOptions[value]; !exists {
				return toolpkg.AskUserQuestionResult{}, fmt.Errorf("ask_user_question response for %q selected unknown option %q", question.Header, value)
			}
		}
		if len(selectedValues) == 0 && freeformText == "" {
			return toolpkg.AskUserQuestionResult{}, fmt.Errorf("ask_user_question response for %q is empty", question.Header)
		}
		rawAnswer := strings.TrimSpace(answer.RawAnswer)
		if rawAnswer == "" {
			rawAnswer = buildAskUserQuestionRawAnswer(selectedValues, freeformText)
		}
		answers = append(answers, toolpkg.AskUserQuestionAnswer{
			Header:         question.Header,
			Question:       question.Question,
			SelectedValues: selectedValues,
			FreeformText:   freeformText,
			RawAnswer:      rawAnswer,
		})
	}
	return toolpkg.AskUserQuestionResult{Status: "answered", Answers: answers}, nil
}

func normalizeAskUserQuestionAnswerValues(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

func buildAskUserQuestionRawAnswer(selectedValues []string, freeformText string) string {
	parts := make([]string, 0, len(selectedValues)+1)
	parts = append(parts, selectedValues...)
	if trimmed := strings.TrimSpace(freeformText); trimmed != "" {
		parts = append(parts, trimmed)
	}
	return strings.Join(parts, ", ")
}
