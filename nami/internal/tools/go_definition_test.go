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

func TestGoDefinitionSignatures(t *testing.T) {
	workspace := inWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "sig.go", `package sig

import "context"

type Server struct{}

// Start is documented.
func (s *Server) Start(ctx context.Context) error {
	return nil
}

func (Server) Name() string { return "" }

func Map[T any, U any](items []T, fn func(T) U) []U {
	return nil
}

func Parse() int { return 0 }

type List[T any] []T

type Alias = int
`)

	cases := map[string]string{
		"Start": "func (s *Server) Start(ctx context.Context) error",
		"Name":  "func (Server) Name() string",
		"Map":   "func Map[T any, U any](items []T, fn func(T) U) []U",
		"Parse": "func Parse() int",
		"List":  "type List[T any] []T",
		"Alias": "type Alias = int",
	}
	for symbol, want := range cases {
		t.Run(symbol, func(t *testing.T) {
			output, err := NewGoDefinitionTool().Execute(context.Background(), ToolInput{Params: map[string]any{"symbol": symbol, "path": path}})
			if err != nil {
				t.Fatalf("go_definition: %v", err)
			}
			var matches []goDefinitionMatch
			if err := json.Unmarshal([]byte(output.Output), &matches); err != nil {
				t.Fatalf("output %q: %v", output.Output, err)
			}
			if len(matches) != 1 || matches[0].Signature != want {
				t.Fatalf("matches = %+v, want signature %q", matches, want)
			}
		})
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
