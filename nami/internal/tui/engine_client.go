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

func makeUserInputMessage(text string) (ipc.ClientMessage, error) {
	payload, err := json.Marshal(ipc.UserInputPayload{Text: text})
	if err != nil {
		return ipc.ClientMessage{}, fmt.Errorf("marshal user input: %w", err)
	}
	return ipc.ClientMessage{
		Type:    ipc.MsgUserInput,
		Payload: payload,
	}, nil
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
	default:
		if len(event.Payload) == 0 {
			return string(event.Type)
		}
		return fmt.Sprintf("%s: %s", event.Type, strings.TrimSpace(string(event.Payload)))
	}
}
