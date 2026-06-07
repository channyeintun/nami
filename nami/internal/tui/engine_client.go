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
	case ipc.EventTurnComplete:
		return "turn complete"
	default:
		if len(event.Payload) == 0 {
			return string(event.Type)
		}
		return fmt.Sprintf("%s: %s", event.Type, strings.TrimSpace(string(event.Payload)))
	}
}
