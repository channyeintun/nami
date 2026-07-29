package permissions

import (
	"testing"

	"github.com/channyeintun/nami/internal/tools"
)

func bashInput(command string) tools.ToolInput {
	return tools.ToolInput{Name: "bash", Params: map[string]any{"command": command}}
}

func assessBash(command string) RiskAssessment {
	return AssessRisk("bash", bashInput(command), tools.PermissionWrite)
}

func TestAssessRiskTreatsInspectionCommandsAsRead(t *testing.T) {
	for _, command := range []string{"ls -la", "git status", "cat go.mod", "find . -name '*.go'"} {
		if got := assessBash(command); got.Level != "read" {
			t.Errorf("%q assessed as %+v, want read", command, got)
		}
	}
}

// A "read" assessment auto-approves without prompting, so any command that
// mutates the workspace must not reach that level.
func TestAssessRiskDoesNotAutoApproveMutatingCommands(t *testing.T) {
	for _, command := range []string{
		"find . -delete",
		"find . -fprint /tmp/out",
		"git branch -D feature",
		"git tag -d v1.0.0",
		"rm -rf build",
		"go build ./...",
	} {
		got := assessBash(command)
		if got.Level == "read" {
			t.Errorf("%q assessed as read and would auto-approve: %+v", command, got)
		}
		if isSessionSafeAutoApprove(got) && got.Level == "read" {
			t.Errorf("%q is session-safe auto-approvable: %+v", command, got)
		}
	}
}

func TestAssessRiskFlagsDestructiveCommands(t *testing.T) {
	got := assessBash("rm -rf build")
	if got.Level != "destructive" {
		t.Fatalf("assessment = %+v, want destructive", got)
	}
	// Destructive commands must never be persistently allowed.
	if !got.DisallowPersistentAllow {
		t.Fatalf("assessment = %+v, want DisallowPersistentAllow", got)
	}
	if isSessionSafeAutoApprove(got) {
		t.Fatal("destructive commands must not be session-safe")
	}
}

func TestAssessRiskTreatsBackgroundCommandsAsExecute(t *testing.T) {
	input := tools.ToolInput{
		Name:   "bash",
		Params: map[string]any{"command": "ls -la", "background": true},
	}
	// Backgrounding outlives the turn, so even a read-only command escalates.
	if got := AssessRisk("bash", input, tools.PermissionWrite); got.Level != "execute" {
		t.Fatalf("assessment = %+v, want execute", got)
	}
}

func TestIsSessionSafeAutoApprove(t *testing.T) {
	safe := []RiskAssessment{{Level: ""}, {Level: "read"}, {Level: "write"}, {Level: "execute"}}
	for _, risk := range safe {
		if !isSessionSafeAutoApprove(risk) {
			t.Errorf("%+v should be session-safe", risk)
		}
	}
	unsafe := []RiskAssessment{
		{Level: "high"},
		{Level: "destructive"},
		{Level: "read", DisallowPersistentAllow: true},
	}
	for _, risk := range unsafe {
		if isSessionSafeAutoApprove(risk) {
			t.Errorf("%+v should not be session-safe", risk)
		}
	}
}
