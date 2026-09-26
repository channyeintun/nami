package engine

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/config"
	costpkg "github.com/channyeintun/nami/internal/cost"
	"github.com/channyeintun/nami/internal/ipc"
	"github.com/channyeintun/nami/internal/session"
)

// isolateUserConfig points the user config and home directories at empty
// temporary ones so a handler that loads config reads defaults only.
func isolateUserConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
}

func newTestSlashCommandContext(t *testing.T, store *session.Store, messages []api.Message, timeline *conversationTimeline) *slashCommandContext {
	t.Helper()
	var client api.LLMClient
	return newSlashCommandContext(
		t.Context(),
		ipc.NewBridge(strings.NewReader(""), io.Discard),
		nil,
		store,
		nil,
		config.DefaultConfig(),
		nil,
		nil,
		costpkg.NewTracker(),
		ipc.SlashCommandPayload{},
		"old-session",
		time.Now(),
		agent.ModeFast,
		"anthropic/claude-sonnet-5",
		"",
		t.TempDir(),
		messages,
		timeline,
		nil,
		&client,
	)
}

func TestClearSlashCommandStartsAFreshTimeline(t *testing.T) {
	isolateUserConfig(t)
	store := session.NewStore(t.TempDir())
	oldMessages := []api.Message{
		{Role: api.RoleUser, Content: "first question"},
		{Role: api.RoleAssistant, Content: "looking", ToolCalls: []api.ToolCall{{ID: "old-tool", Name: "read_file", Input: "{}"}}},
		{Role: api.RoleTool, ToolResult: &api.ToolResult{ToolCallID: "old-tool", Output: "contents"}},
		{Role: api.RoleAssistant, Content: "first answer"},
	}
	oldTimeline := rebuildConversationTimeline(oldMessages)
	oldTimeline.RecordProgress(ipc.ProgressPayload{ID: "old-progress", Message: "working on the old task"})
	cmd := newTestSlashCommandContext(t, store, oldMessages, oldTimeline)

	if err := handleClearSlashCommand(cmd); err != nil {
		t.Fatalf("handleClearSlashCommand: %v", err)
	}
	if cmd.state.SessionID == "old-session" {
		t.Fatal("/clear kept the old session id")
	}

	saved, err := store.LoadConversationTimeline(cmd.state.SessionID)
	if err != nil {
		t.Fatalf("LoadConversationTimeline: %v", err)
	}
	if len(saved.Transcript) != 0 || len(saved.Progress) != 0 {
		t.Fatalf("new session saved the old timeline: transcript=%+v progress=%+v", saved.Transcript, saved.Progress)
	}

	// The first message of the new session reuses position 0. It must be
	// recorded, not deduplicated against the old session's entry.
	cmd.state.Messages = append(cmd.state.Messages, api.Message{Role: api.RoleUser, Content: "new question"})
	cmd.state.Timeline.RecordUserMessage(0)
	payload := cmd.state.Timeline.HydratedPayload(cmd.state.Messages, "")
	if len(payload.Transcript) != 1 || payload.Transcript[0].ID != conversationTimelineMessageID(0) {
		t.Fatalf("new session transcript = %+v, want just the new message", payload.Transcript)
	}
}
