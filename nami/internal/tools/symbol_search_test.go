package tools

import (
	"context"
	"strings"
	"testing"
)

func runSymbolSearch(t *testing.T, symbol, path string) ToolOutput {
	t.Helper()
	output, err := NewSymbolSearchTool().Execute(context.Background(), ToolInput{Params: map[string]any{"symbol": symbol, "path": path}})
	if err != nil {
		t.Fatalf("symbol_search %q: %v", symbol, err)
	}
	return output
}

// Generated code often embeds data in one enormous line. The scan has to get
// past it: definitions after the line must still be found, and a match on the
// long line itself must not dump the whole line into the result.
func TestSymbolSearchReadsPastVeryLongLines(t *testing.T) {
	workspace := inWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "bindata.go", strings.Join([]string{
		"package assets",
		"",
		"var Blob = \"" + strings.Repeat("A", 200_000) + "\"",
		"",
		"func Target() {}",
		"",
	}, "\n"))

	after := runSymbolSearch(t, "Target", path)
	if want := path + ":5 [func] func Target() {}"; after.Output != want {
		t.Fatalf("output = %q, want %q", after.Output, want)
	}

	long := runSymbolSearch(t, "Blob", path)
	if !strings.HasPrefix(long.Output, path+":3 [var] var Blob = \"AAAA") {
		t.Fatalf("output = %.120q, want the var Blob definition on line 3", long.Output)
	}
	if len(long.Output) > fileReadMaxLineBytes+len(path)+32 {
		t.Fatalf("output is %d bytes; the long line was not clipped", len(long.Output))
	}
}
