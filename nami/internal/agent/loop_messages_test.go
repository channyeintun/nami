package agent

import (
	"testing"

	"github.com/channyeintun/nami/internal/api"
)

func TestLatestToolOutput(t *testing.T) {
	toolMessage := func(output string) api.Message {
		return api.Message{Role: api.RoleTool, Content: output, ToolResult: &api.ToolResult{Output: output}}
	}
	tests := []struct {
		name     string
		messages []api.Message
		want     string
	}{
		{
			name: "latest tool batch",
			messages: []api.Message{
				toolMessage("older output"),
				{Role: api.RoleAssistant, Content: "next step"},
				toolMessage("first result"),
				toolMessage("second result"),
			},
			want: "second result\nfirst result\n",
		},
		{
			name: "results followed by the loop's retry nudge",
			messages: []api.Message{
				{Role: api.RoleAssistant, ToolCalls: []api.ToolCall{{ID: "call-1"}}},
				toolMessage("patch failed: context mismatch in loop.go"),
				{Role: api.RoleUser, Content: "[system] Your previous file edit failed with the same error again."},
			},
			want: "patch failed: context mismatch in loop.go\n",
		},
		{
			name: "no tool output since the last assistant reply",
			messages: []api.Message{
				toolMessage("older output"),
				{Role: api.RoleAssistant, Content: "all done"},
				{Role: api.RoleUser, Content: "thanks, now tidy up"},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := latestToolOutput(tt.messages); got != tt.want {
				t.Fatalf("latestToolOutput() = %q, want %q", got, tt.want)
			}
		})
	}
}
