package engine

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/channyeintun/nami/internal/ipc"
	"github.com/channyeintun/nami/internal/session"
)

// The TUI polls the dashboard every second while the tasks dialog is open. An
// inbox that cannot be read used to return an error to the main loop, which
// ended the whole session.
func TestSwarmDashboardInspectSurvivesAnUnreadableInbox(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sessionID := "dashboard-session"
	inbox := filepath.Join(store.SessionDir(sessionID), "swarm", "inbox.json")
	if err := os.MkdirAll(filepath.Dir(inbox), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(inbox, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var output bytes.Buffer
	bridge := ipc.NewBridge(strings.NewReader(""), &output)
	if err := handleSwarmDashboardInspectMessage(t.Context(), bridge, store, sessionID); err != nil {
		t.Fatalf("an unreadable inbox ended the session: %v", err)
	}

	var event ipc.StreamEvent
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode emitted event %q: %v", output.String(), err)
	}
	if event.Type != ipc.EventNotice || !strings.Contains(string(event.Payload), "swarm inbox") {
		t.Fatalf("emitted %s %s, want a notice naming the inbox problem", event.Type, event.Payload)
	}
}
