package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/channyeintun/nami/internal/config"
	"github.com/channyeintun/nami/internal/engine"
	"github.com/channyeintun/nami/internal/ipc"
)

const engineChannelSize = 64

type engineClient struct {
	ctx      context.Context
	cancel   context.CancelFunc
	messages chan ipc.ClientMessage
	events   chan ipc.StreamEvent
	done     chan error
}

type engineStartedMsg struct{}

type engineEventMsg struct {
	event ipc.StreamEvent
}

type engineDoneMsg struct {
	err error
}

type clientMessageSentMsg struct{}

func newEngineClient(parent context.Context) engineClient {
	ctx, cancel := context.WithCancel(parent)
	return engineClient{
		ctx:      ctx,
		cancel:   cancel,
		messages: make(chan ipc.ClientMessage, engineChannelSize),
		events:   make(chan ipc.StreamEvent, engineChannelSize),
		done:     make(chan error, 1),
	}
}

func (c engineClient) start(cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		go func() {
			c.done <- engine.RunEmbeddedEngine(c.ctx, cfg, c.messages, c.events)
		}()
		return engineStartedMsg{}
	}
}

func (c engineClient) wait() tea.Cmd {
	return func() tea.Msg {
		select {
		case event := <-c.events:
			return engineEventMsg{event: event}
		case err := <-c.done:
			return engineDoneMsg{err: err}
		case <-c.ctx.Done():
			return engineDoneMsg{err: c.ctx.Err()}
		}
	}
}

func (c engineClient) send(msg ipc.ClientMessage) tea.Cmd {
	return func() tea.Msg {
		select {
		case c.messages <- msg:
			return clientMessageSentMsg{}
		case <-c.ctx.Done():
			return engineDoneMsg{err: c.ctx.Err()}
		}
	}
}

func (c engineClient) shutdown() tea.Cmd {
	return tea.Batch(
		c.send(ipc.ClientMessage{Type: ipc.MsgShutdown}),
		func() tea.Msg {
			c.cancel()
			return nil
		},
	)
}

func (c engineClient) cancelTurn() tea.Cmd {
	return c.send(ipc.ClientMessage{Type: ipc.MsgCancel})
}

func makeUserInputMessage(text string, images []ipc.ImageInputPayload) (ipc.ClientMessage, error) {
	payload, err := json.Marshal(ipc.UserInputPayload{Text: text, Images: images})
	if err != nil {
		return ipc.ClientMessage{}, fmt.Errorf("marshal user input: %w", err)
	}
	return ipc.ClientMessage{
		Type:    ipc.MsgUserInput,
		Payload: payload,
	}, nil
}

func makePermissionResponseMessage(requestID, decision string) (ipc.ClientMessage, error) {
	payload, err := json.Marshal(ipc.PermissionResponsePayload{
		RequestID: requestID,
		Decision:  decision,
	})
	if err != nil {
		return ipc.ClientMessage{}, fmt.Errorf("marshal permission response: %w", err)
	}
	return ipc.ClientMessage{Type: ipc.MsgPermissionResponse, Payload: payload}, nil
}

func makeArtifactReviewResponseMessage(requestID, decision string) (ipc.ClientMessage, error) {
	payload, err := json.Marshal(ipc.ArtifactReviewResponsePayload{
		RequestID: requestID,
		Decision:  decision,
	})
	if err != nil {
		return ipc.ClientMessage{}, fmt.Errorf("marshal artifact review response: %w", err)
	}
	return ipc.ClientMessage{Type: ipc.MsgArtifactReviewResponse, Payload: payload}, nil
}

func makeSelectionResponseMessage(request selectionRequestState, option selectionOptionState, cancel bool) (ipc.ClientMessage, error) {
	switch request.Kind {
	case "model":
		payload, err := json.Marshal(ipc.ModelSelectionResponsePayload{
			RequestID: request.RequestID,
			Model:     option.Model,
			Provider:  option.Provider,
			Cancel:    cancel,
		})
		if err != nil {
			return ipc.ClientMessage{}, fmt.Errorf("marshal model selection response: %w", err)
		}
		return ipc.ClientMessage{Type: ipc.MsgModelSelectionResponse, Payload: payload}, nil
	case "reasoning":
		payload, err := json.Marshal(ipc.ReasoningSelectionResponsePayload{
			RequestID: request.RequestID,
			Effort:    option.Effort,
			Cancel:    cancel,
		})
		if err != nil {
			return ipc.ClientMessage{}, fmt.Errorf("marshal reasoning selection response: %w", err)
		}
		return ipc.ClientMessage{Type: ipc.MsgReasoningSelectionResponse, Payload: payload}, nil
	case "resume":
		payload, err := json.Marshal(ipc.ResumeSelectionResponsePayload{
			RequestID: request.RequestID,
			SessionID: option.SessionID,
			Cancel:    cancel,
		})
		if err != nil {
			return ipc.ClientMessage{}, fmt.Errorf("marshal resume selection response: %w", err)
		}
		return ipc.ClientMessage{Type: ipc.MsgResumeSelectionResponse, Payload: payload}, nil
	case "rewind":
		payload, err := json.Marshal(ipc.RewindSelectionResponsePayload{
			RequestID:    request.RequestID,
			MessageIndex: option.MessageIndex,
			Cancel:       cancel,
		})
		if err != nil {
			return ipc.ClientMessage{}, fmt.Errorf("marshal rewind selection response: %w", err)
		}
		return ipc.ClientMessage{Type: ipc.MsgRewindSelectionResponse, Payload: payload}, nil
	default:
		return ipc.ClientMessage{}, fmt.Errorf("unknown selection kind %q", request.Kind)
	}
}

func makeQuestionResponseMessage(request questionRequestState, cancel bool) (ipc.ClientMessage, error) {
	payload := ipc.AskUserQuestionResponsePayload{
		RequestID: request.RequestID,
		Status:    "cancelled",
	}
	if !cancel {
		payload.Status = "answered"
		payload.Answers = make([]ipc.AskUserQuestionAnswerPayload, 0, len(request.Questions))
		for _, question := range request.Questions {
			option, ok := defaultQuestionOption(question.Options)
			if !ok {
				return ipc.ClientMessage{}, fmt.Errorf("question %q has no selectable option", question.Header)
			}
			payload.Answers = append(payload.Answers, ipc.AskUserQuestionAnswerPayload{
				Header:         question.Header,
				SelectedValues: []string{option.Value},
				RawAnswer:      option.Value,
			})
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ipc.ClientMessage{}, fmt.Errorf("marshal question response: %w", err)
	}
	return ipc.ClientMessage{Type: ipc.MsgAskUserQuestionResponse, Payload: data}, nil
}

func defaultQuestionOption(options []questionOptionState) (questionOptionState, bool) {
	for _, option := range options {
		if option.Recommended {
			return option, true
		}
	}
	if len(options) == 0 {
		return questionOptionState{}, false
	}
	return options[0], true
}

func summarizeEvent(event ipc.StreamEvent) string {
	switch event.Type {
	case ipc.EventReady:
		var payload ipc.ReadyPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("ready: decode failed: %v", err)
		}
		return fmt.Sprintf("ready: protocol v%d, %d slash command(s)", payload.ProtocolVersion, len(payload.SlashCommands))
	case ipc.EventNotice:
		var payload ipc.NoticePayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("notice: decode failed: %v", err)
		}
		return "notice: " + strings.TrimSpace(payload.Message)
	case ipc.EventError:
		var payload ipc.ErrorPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("error: decode failed: %v", err)
		}
		return "error: " + strings.TrimSpace(payload.Message)
	case ipc.EventTokenDelta:
		var payload ipc.TokenDeltaPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("assistant: decode failed: %v", err)
		}
		return strings.TrimRight(payload.Text, "\n")
	case ipc.EventThinkingDelta:
		var payload ipc.TokenDeltaPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("thinking: decode failed: %v", err)
		}
		return "thinking: " + strings.TrimSpace(payload.Text)
	case ipc.EventProgress:
		var payload ipc.ProgressPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("progress: decode failed: %v", err)
		}
		return "progress: " + strings.TrimSpace(payload.Message)
	case ipc.EventToolStart:
		var payload ipc.ToolStartPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("tool: decode failed: %v", err)
		}
		return "tool started: " + strings.TrimSpace(payload.Name)
	case ipc.EventToolResult:
		var payload ipc.ToolResultPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("tool result: decode failed: %v", err)
		}
		name := strings.TrimSpace(payload.Name)
		if name == "" {
			name = strings.TrimSpace(payload.ToolID)
		}
		return "tool finished: " + name
	case ipc.EventToolError:
		var payload ipc.ToolErrorPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("tool error: decode failed: %v", err)
		}
		name := strings.TrimSpace(payload.Name)
		if name == "" {
			name = strings.TrimSpace(payload.ToolID)
		}
		return fmt.Sprintf("tool failed: %s: %s", name, strings.TrimSpace(payload.Error))
	case ipc.EventArtifactCreated:
		var payload ipc.ArtifactCreatedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("artifact: decode failed: %v", err)
		}
		return "artifact created: " + strings.TrimSpace(payload.Title)
	case ipc.EventArtifactFocused:
		var payload ipc.ArtifactFocusedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("artifact focus: decode failed: %v", err)
		}
		return "artifact focused: " + strings.TrimSpace(payload.Title)
	case ipc.EventArtifactReviewRequested:
		var payload ipc.ArtifactReviewRequestedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("artifact review: decode failed: %v", err)
		}
		return "artifact review requested: " + strings.TrimSpace(payload.Title)
	case ipc.EventBackgroundCommandUpdated:
		var payload ipc.BackgroundCommandUpdatedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("background command: decode failed: %v", err)
		}
		return fmt.Sprintf("background command %s: %s", strings.TrimSpace(payload.CommandID), strings.TrimSpace(payload.Status))
	case ipc.EventBackgroundAgentUpdated:
		var payload ipc.BackgroundAgentUpdatedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("background agent: decode failed: %v", err)
		}
		return fmt.Sprintf("background agent %s: %s", strings.TrimSpace(payload.AgentID), strings.TrimSpace(payload.Status))
	case ipc.EventPermissionRequest:
		var payload ipc.PermissionRequestPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("permission: decode failed: %v", err)
		}
		return fmt.Sprintf("permission requested: %s %s", strings.TrimSpace(payload.Tool), strings.TrimSpace(payload.Risk))
	case ipc.EventAskUserQuestionRequested:
		var payload ipc.AskUserQuestionRequestedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("question: decode failed: %v", err)
		}
		return fmt.Sprintf("question requested: %d prompt(s)", len(payload.Questions))
	case ipc.EventModelSelectionRequested:
		var payload ipc.ModelSelectionRequestedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("model selection: decode failed: %v", err)
		}
		return fmt.Sprintf("model selection requested: %d option(s)", len(payload.Options))
	case ipc.EventReasoningSelectionRequested:
		var payload ipc.ReasoningSelectionRequestedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("reasoning selection: decode failed: %v", err)
		}
		return fmt.Sprintf("reasoning selection requested: %d option(s)", len(payload.Options))
	case ipc.EventResumeSelectionRequested:
		var payload ipc.ResumeSelectionRequestedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("resume selection: decode failed: %v", err)
		}
		return fmt.Sprintf("resume selection requested: %d session(s)", len(payload.Sessions))
	case ipc.EventRewindSelectionRequested:
		var payload ipc.RewindSelectionRequestedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("rewind selection: decode failed: %v", err)
		}
		return fmt.Sprintf("rewind selection requested: %d turn(s)", len(payload.Turns))
	case ipc.EventModelChanged:
		var payload ipc.ModelChangedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("model: decode failed: %v", err)
		}
		return "model: " + strings.TrimSpace(payload.Model)
	case ipc.EventModeChanged:
		var payload ipc.ModeChangedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("mode: decode failed: %v", err)
		}
		return "mode: " + strings.TrimSpace(payload.Mode)
	case ipc.EventTurnComplete:
		return "turn complete"
	case ipc.EventConversationHydrated:
		var payload ipc.ConversationHydratedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("conversation restored: decode failed: %v", err)
		}
		return fmt.Sprintf("conversation restored: %d message(s)", len(payload.Messages))
	case ipc.EventMemoryRecalled:
		var payload ipc.MemoryRecalledPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("memory: decode failed: %v", err)
		}
		return fmt.Sprintf("memory recalled: %d item(s)", payload.Count)
	case ipc.EventRetrievalUsed:
		var payload ipc.RetrievalUsedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("retrieval: decode failed: %v", err)
		}
		if payload.Skipped {
			return "retrieval skipped"
		}
		return fmt.Sprintf("retrieval used: %d snippet(s)", payload.SnippetCount)
	case ipc.EventCompactStart:
		return "compaction started"
	case ipc.EventCompactEnd:
		var payload ipc.CompactEndPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("compaction: decode failed: %v", err)
		}
		return fmt.Sprintf("compaction finished: %d token(s) saved", payload.TokensSaved)
	case ipc.EventSessionRestored:
		var payload ipc.SessionRestoredPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("session restore: decode failed: %v", err)
		}
		return "session restored: " + strings.TrimSpace(payload.SessionID)
	case ipc.EventSessionRewound:
		var payload ipc.SessionRewoundPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Sprintf("session rewind: decode failed: %v", err)
		}
		return fmt.Sprintf("session rewound: %d message(s)", payload.MessageCount)
	default:
		if len(event.Payload) == 0 {
			return string(event.Type)
		}
		return fmt.Sprintf("%s: %s", event.Type, strings.TrimSpace(string(event.Payload)))
	}
}
