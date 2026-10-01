package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/channyeintun/nami/internal/config"
	"github.com/channyeintun/nami/internal/ipc"
	"github.com/channyeintun/nami/internal/session"
)

// /workflows opens the dialog with every saved workflow the user can run,
// the project's copy winning over the user's of the same name, and closes the
// turn so the prompt comes back.
func TestWorkflowsSlashCommandListsSavedWorkflows(t *testing.T) {
	isolateUserConfig(t)
	cwd := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd, ".git"), 0o700); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	projectReview := writeWorkflowFile(t, filepath.Join(cwd, ".nami", "workflows"), "review.js", "review")
	writeWorkflowFile(t, config.GlobalWorkflowDir(), "review.js", "user-review")
	userTriage := writeWorkflowFile(t, config.GlobalWorkflowDir(), "triage.js", "triage")

	cmd, emitted := newTestSlashCommandContext(t, session.NewStore(t.TempDir()), nil, nil)
	cmd.state.CWD = cwd
	if err := handleWorkflowsSlashCommand(cmd); err != nil {
		t.Fatalf("handleWorkflowsSlashCommand: %v", err)
	}

	events := emittedEvents(t, emitted)
	if len(events) != 2 || events[0].Type != ipc.EventWorkflowsRequested || events[1].Type != ipc.EventTurnComplete {
		t.Fatalf("events = %+v, want workflows_requested then turn_complete", events)
	}
	var payload ipc.WorkflowsRequestedPayload
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	want := []ipc.SavedWorkflowPayload{
		{Name: "review", Description: "the review workflow", WhenToUse: "when review is needed", Scope: "project", Path: projectReview},
		{Name: "triage", Description: "the triage workflow", WhenToUse: "when triage is needed", Scope: "user", Path: userTriage},
	}
	if len(payload.Saved) != len(want) {
		t.Fatalf("saved = %+v, want %+v", payload.Saved, want)
	}
	for index := range want {
		if payload.Saved[index] != want[index] {
			t.Fatalf("saved[%d] = %+v, want %+v", index, payload.Saved[index], want[index])
		}
	}
}

// A broken saved workflow must not keep the dialog from opening, and the
// user has to learn which file to fix.
func TestWorkflowsSlashCommandReportsABrokenSavedWorkflow(t *testing.T) {
	isolateUserConfig(t)
	broken := filepath.Join(config.GlobalWorkflowDir(), "broken.js")
	if err := os.MkdirAll(filepath.Dir(broken), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(broken, []byte("return 1"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cmd, emitted := newTestSlashCommandContext(t, session.NewStore(t.TempDir()), nil, nil)
	if err := handleWorkflowsSlashCommand(cmd); err != nil {
		t.Fatalf("handleWorkflowsSlashCommand: %v", err)
	}
	events := emittedEvents(t, emitted)
	if len(events) != 3 || events[0].Type != ipc.EventNotice || events[1].Type != ipc.EventWorkflowsRequested || events[2].Type != ipc.EventTurnComplete {
		t.Fatalf("events = %+v, want a notice, workflows_requested, turn_complete", events)
	}
	var notice ipc.NoticePayload
	if err := json.Unmarshal(events[0].Payload, &notice); err != nil {
		t.Fatalf("decode notice: %v", err)
	}
	if !strings.Contains(notice.Message, broken) {
		t.Fatalf("notice = %q, want it to name %s", notice.Message, broken)
	}
}
