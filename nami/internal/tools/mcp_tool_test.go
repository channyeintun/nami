package tools

import (
	"testing"

	mcppkg "github.com/channyeintun/nami/internal/mcp"
)

// MCP tools that act on something, such as a browser's navigate, click, and
// type, have to run in the order the model issued them. Only tools that change
// nothing may join a parallel batch.
func TestMCPToolRunsInParallelOnlyWhenItChangesNothing(t *testing.T) {
	cases := []struct {
		name         string
		permission   mcppkg.ToolPermission
		readOnlyHint bool
		want         ConcurrencyDecision
	}{
		{name: "read permission", permission: mcppkg.ToolPermissionRead, want: ConcurrencyParallel},
		{name: "annotated read-only", permission: mcppkg.ToolPermissionExecute, readOnlyHint: true, want: ConcurrencyParallel},
		{name: "write permission", permission: mcppkg.ToolPermissionWrite, want: ConcurrencySerial},
		{name: "execute permission", permission: mcppkg.ToolPermissionExecute, want: ConcurrencySerial},
		{name: "untrusted default", permission: "", want: ConcurrencySerial},
	}
	for _, tc := range cases {
		tool := NewMCPTool(nil, mcppkg.DiscoveredTool{
			ServerName: "browser",
			Permission: tc.permission,
			Tool:       mcppkg.ToolDescriptor{Name: "act", ReadOnlyHint: tc.readOnlyHint},
		})
		if got := tool.Concurrency(ToolInput{}); got != tc.want {
			t.Errorf("%s: Concurrency = %v, want %v", tc.name, got, tc.want)
		}
	}
}
