package session

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/ipc"
)

func TestTranscriptRoundTrips(t *testing.T) {
	store := NewStore(t.TempDir())
	messages := []api.Message{
		{Role: api.RoleUser, Content: "fix the build"},
		{Role: api.RoleAssistant, ToolCalls: []api.ToolCall{{ID: "call_1", Name: "bash", Input: `{"command":"go build ./..."}`}}},
		{Role: api.RoleTool, Content: "ok", ToolResult: &api.ToolResult{ToolCallID: "call_1", Output: "ok"}},
	}
	if err := store.SaveTranscript("s1", messages); err != nil {
		t.Fatalf("SaveTranscript: %v", err)
	}
	loaded, err := store.LoadTranscript("s1")
	if err != nil {
		t.Fatalf("LoadTranscript: %v", err)
	}
	if len(loaded) != len(messages) {
		t.Fatalf("loaded %d messages, want %d", len(loaded), len(messages))
	}
	if loaded[2].ToolResult == nil || loaded[2].ToolResult.ToolCallID != "call_1" {
		t.Fatalf("tool result lost its pairing: %+v", loaded[2])
	}
}

// A user message carries pasted images as base64, and the IPC bridge accepts
// messages up to 10 MiB. Whatever SaveTranscript writes, LoadTranscript must
// read back, or the session can never be resumed.
func TestTranscriptRoundTripsAMessageLargerThanALineBuffer(t *testing.T) {
	store := NewStore(t.TempDir())
	screenshot := strings.Repeat("A", 3*1024*1024)
	messages := []api.Message{
		{Role: api.RoleUser, Content: "what is wrong here?", Images: []api.ImageAttachment{{Data: screenshot, MediaType: "image/png"}}},
		{Role: api.RoleAssistant, Content: "the layout overflows"},
	}
	if err := store.SaveTranscript("s1", messages); err != nil {
		t.Fatalf("SaveTranscript: %v", err)
	}
	loaded, err := store.LoadTranscript("s1")
	if err != nil {
		t.Fatalf("LoadTranscript: %v", err)
	}
	if len(loaded) != 2 || len(loaded[0].Images) != 1 || loaded[0].Images[0].Data != screenshot {
		t.Fatalf("large message did not survive the round trip (%d messages)", len(loaded))
	}
}

// Transcripts routinely hold secrets — a pasted key, a tool that printed an
// .env file — and the timeline mirrors the same conversation. Every file the
// store writes must be as private as the transcript.
func TestSessionFilesAreOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply on Windows")
	}
	store := NewStore(filepath.Join(t.TempDir(), "sessions"))
	messages := []api.Message{{Role: api.RoleUser, Content: "my key is sk-secret"}}
	if err := store.SaveTranscript("s1", messages); err != nil {
		t.Fatalf("SaveTranscript: %v", err)
	}
	if err := store.SaveMetadata(Metadata{SessionID: "s1", Title: "key rotation"}); err != nil {
		t.Fatalf("SaveMetadata: %v", err)
	}
	timeline := ipc.ConversationHydratedPayload{Messages: []ipc.ConversationHydratedMessagePayload{{Text: "my key is sk-secret"}}}
	if err := store.SaveConversationTimeline("s1", timeline); err != nil {
		t.Fatalf("SaveConversationTimeline: %v", err)
	}

	dir := store.SessionDir("s1")
	for path, want := range map[string]os.FileMode{
		dir:                                     0o700,
		filepath.Join(dir, "transcript.ndjson"): 0o600,
		filepath.Join(dir, "metadata.json"):     0o600,
		filepath.Join(dir, "timeline.json"):     0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s has mode %o, want %o", filepath.Base(path), got, want)
		}
	}
}

func TestLoadTranscriptOfAMissingSessionIsEmpty(t *testing.T) {
	store := NewStore(t.TempDir())
	messages, err := store.LoadTranscript("absent")
	if err != nil || messages != nil {
		t.Fatalf("LoadTranscript = %v, %v; want nil, nil", messages, err)
	}
}
