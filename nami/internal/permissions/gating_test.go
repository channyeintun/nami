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

// Editing a sensitive file such as .env must prompt every time, whichever
// spelling of the path a write tool uses and however it nests it. The check
// used to read only file_path, so replace_string_in_file's and notebook_edit's
// filePath, and every path inside multi_replace_string_in_file, went unchecked
// and could be auto-approved.
func TestAssessRiskFindsSensitiveFilesUnderEveryPathSpelling(t *testing.T) {
	cases := []struct {
		name   string
		tool   string
		params map[string]any
	}{
		{name: "filePath", tool: "replace_string_in_file", params: map[string]any{"filePath": ".env", "oldString": "A", "newString": "B"}},
		{name: "file_path", tool: "create_file", params: map[string]any{"file_path": ".git/hooks/pre-commit", "content": "#!/bin/sh"}},
		{name: "notebook", tool: "notebook_edit", params: map[string]any{"filePath": ".git/config", "operation": "insert"}},
		{name: "nested replacement", tool: "multi_replace_string_in_file", params: map[string]any{
			"explanation": "tweak",
			"replacements": []any{
				map[string]any{"filePath": "main.go", "oldString": "a", "newString": "b"},
				map[string]any{"filePath": ".git/config", "oldString": "a", "newString": "b"},
			},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AssessRisk(tc.tool, tools.ToolInput{Name: tc.tool, Params: tc.params}, tools.PermissionWrite)
			if got.Level != "high" {
				t.Fatalf("assessed as %+v, want high", got)
			}
		})
	}

	ordinary := AssessRisk("multi_replace_string_in_file", tools.ToolInput{Params: map[string]any{
		"replacements": []any{map[string]any{"filePath": "main.go", "oldString": "a", "newString": "b"}},
	}}, tools.PermissionWrite)
	if ordinary.Level != "write" {
		t.Fatalf("ordinary edit assessed as %+v, want write", ordinary)
	}
}
