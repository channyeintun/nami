package tools

import (
	"context"
	"encoding/json"
	"testing"
)

// Generated Go files carry //line directives that point positions at the
// source they were generated from. The tools report the real file path, so
// the line and column must be the real ones too.
const lineDirectiveSource = `package gen

//line grammar.y:100
func Parse() int {
	return helper()
}

func helper() int { return 1 }
`

func TestGoDefinitionReportsPhysicalPositions(t *testing.T) {
	workspace := inWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "parser.go", lineDirectiveSource)

	output, err := NewGoDefinitionTool().Execute(context.Background(), ToolInput{Params: map[string]any{"symbol": "Parse", "path": path}})
	if err != nil {
		t.Fatalf("go_definition: %v", err)
	}
	var matches []goDefinitionMatch
	if err := json.Unmarshal([]byte(output.Output), &matches); err != nil {
		t.Fatalf("output %q: %v", output.Output, err)
	}
	if len(matches) != 1 || matches[0].Path != path || matches[0].Line != 4 || matches[0].Column != 6 {
		t.Fatalf("matches = %+v, want Parse at %s:4:6", matches, path)
	}
}

func TestGoReferencesReportsPhysicalPositions(t *testing.T) {
	workspace := inWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "parser.go", lineDirectiveSource)

	output, err := NewGoReferencesTool().Execute(context.Background(), ToolInput{Params: map[string]any{"symbol": "helper", "path": path}})
	if err != nil {
		t.Fatalf("go_references: %v", err)
	}
	var matches []goReferenceMatch
	if err := json.Unmarshal([]byte(output.Output), &matches); err != nil {
		t.Fatalf("output %q: %v", output.Output, err)
	}
	if len(matches) != 1 || matches[0].Line != 5 || matches[0].Source != "return helper()" {
		t.Fatalf("matches = %+v, want the call on line 5", matches)
	}
}
