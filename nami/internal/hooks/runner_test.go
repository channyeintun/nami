package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write hook %s: %v", name, err)
	}
	return path
}

func requireShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("hook scripts in this test are POSIX shell scripts")
	}
}

func TestRunExecutesMatchingHookAndDecodesJSON(t *testing.T) {
	requireShell(t)
	dir := t.TempDir()
	writeScript(t, dir, "pre_tool_use.sh", `echo '{"action":"deny","message":"blocked by policy"}'`)

	responses, err := NewRunner(dir).Run(context.Background(), Payload{Type: HookPreToolUse})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(responses) != 1 {
		t.Fatalf("responses = %d, want 1", len(responses))
	}
	if responses[0].Action != "deny" || responses[0].Message != "blocked by policy" {
		t.Fatalf("response = %+v", responses[0])
	}
}

func TestRunTreatsNonJSONOutputAsMessage(t *testing.T) {
	requireShell(t)
	dir := t.TempDir()
	writeScript(t, dir, "session_start", `echo "  welcome back  "`)

	responses, err := NewRunner(dir).Run(context.Background(), Payload{Type: HookSessionStart})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(responses) != 1 || responses[0].Message != "welcome back" {
		t.Fatalf("responses = %+v, want the trimmed plain-text message", responses)
	}
}

func TestRunPassesPayloadOnStdin(t *testing.T) {
	requireShell(t)
	dir := t.TempDir()
	captured := filepath.Join(dir, "payload.json")
	writeScript(t, dir, "post_tool_use.sh", "cat > "+captured+"\necho '{}'")

	payload := Payload{
		Type:      HookPostToolUse,
		SessionID: "session-1",
		ToolName:  "bash",
		ToolInput: `{"command":"ls"}`,
		Extra:     map[string]any{"exit_code": float64(0)},
	}
	if _, err := NewRunner(dir).Run(context.Background(), payload); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(captured)
	if err != nil {
		t.Fatalf("read captured payload: %v", err)
	}
	var decoded Payload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode captured payload: %v", err)
	}
	if decoded.SessionID != "session-1" || decoded.ToolName != "bash" {
		t.Fatalf("payload = %+v", decoded)
	}
	if decoded.Extra["exit_code"] != float64(0) {
		t.Fatalf("extra = %+v", decoded.Extra)
	}
}

// The stop hook used to be selected with a "stop*" glob, which also matched the
// stop_failure scripts and ran them on every successful stop.
func TestRunDoesNotRunHooksOfAPrefixedHookType(t *testing.T) {
	requireShell(t)
	dir := t.TempDir()
	writeScript(t, dir, "stop.sh", `echo '{"message":"stop"}'`)
	writeScript(t, dir, "stop_failure.sh", `echo '{"message":"stop_failure"}'`)
	writeScript(t, dir, "subagent_stop.sh", `echo '{"message":"subagent_stop"}'`)

	responses, err := NewRunner(dir).Run(context.Background(), Payload{Type: HookStop})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(responses) != 1 || responses[0].Message != "stop" {
		t.Fatalf("responses = %+v, want only the stop hook", responses)
	}

	responses, err = NewRunner(dir).Run(context.Background(), Payload{Type: HookStopFailure})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(responses) != 1 || responses[0].Message != "stop_failure" {
		t.Fatalf("responses = %+v, want only the stop_failure hook", responses)
	}
}

func TestRunExecutesEveryScriptForAHookInNameOrder(t *testing.T) {
	requireShell(t)
	dir := t.TempDir()
	writeScript(t, dir, "session_end-second.sh", `echo '{"message":"second"}'`)
	writeScript(t, dir, "session_end-first.sh", `echo '{"message":"first"}'`)

	responses, err := NewRunner(dir).Run(context.Background(), Payload{Type: HookSessionEnd})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(responses) != 2 {
		t.Fatalf("responses = %+v, want both scripts", responses)
	}
	if responses[0].Message != "first" || responses[1].Message != "second" {
		t.Fatalf("responses = %+v, want name order", responses)
	}
}

func TestRunSkipsFailingHooks(t *testing.T) {
	requireShell(t)
	dir := t.TempDir()
	writeScript(t, dir, "stop-failing.sh", "exit 3")
	writeScript(t, dir, "stop-working.sh", `echo '{"message":"ok"}'`)

	responses, err := NewRunner(dir).Run(context.Background(), Payload{Type: HookStop})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(responses) != 1 || responses[0].Message != "ok" {
		t.Fatalf("responses = %+v, want only the working hook", responses)
	}
}

func TestRunIgnoresDirectoriesAndMissingHooksDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "session_start.d"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	responses, err := NewRunner(dir).Run(context.Background(), Payload{Type: HookSessionStart})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(responses) != 0 {
		t.Fatalf("responses = %+v, want none", responses)
	}

	missing := filepath.Join(dir, "does-not-exist")
	if _, err := NewRunner(missing).Run(context.Background(), Payload{Type: HookSessionStart}); err != nil {
		t.Fatalf("Run on a missing hooks dir: %v", err)
	}
}

func TestRunHonoursContextCancellation(t *testing.T) {
	requireShell(t)
	dir := t.TempDir()
	writeScript(t, dir, "stop.sh", `echo '{"message":"ok"}'`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	responses, err := NewRunner(dir).Run(ctx, Payload{Type: HookStop})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(responses) != 0 {
		t.Fatalf("responses = %+v, want none from a cancelled context", responses)
	}
}

func TestMatchesHookName(t *testing.T) {
	cases := []struct {
		name     string
		hookType HookType
		want     bool
	}{
		{"stop", HookStop, true},
		{"stop.sh", HookStop, true},
		{"stop-notify.py", HookStop, true},
		{"stop_failure", HookStop, false},
		{"stop_failure.sh", HookStop, false},
		{"stopwatch", HookStop, false},
		{"subagent_stop", HookStop, false},
		{"subagent_stop_failure.sh", HookSubagentStop, false},
		{"pre_tool_use.sh", HookPreToolUse, true},
		{"", HookStop, false},
	}
	for _, tc := range cases {
		if got := matchesHookName(tc.name, tc.hookType); got != tc.want {
			t.Errorf("matchesHookName(%q, %q) = %v, want %v", tc.name, tc.hookType, got, tc.want)
		}
	}
}

func TestDefaultHooksDirIsAbsolute(t *testing.T) {
	if dir := DefaultHooksDir(); dir != "" && !filepath.IsAbs(dir) {
		t.Fatalf("DefaultHooksDir() = %q, want an absolute path", dir)
	}
	if !strings.Contains(DefaultHooksDir(), "hooks") {
		t.Fatalf("DefaultHooksDir() = %q, want it to point at a hooks directory", DefaultHooksDir())
	}
}
