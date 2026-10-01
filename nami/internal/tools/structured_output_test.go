package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// findingsSchema is the kind of schema a workflow step returns its result in.
const findingsSchema = `{
	"type": "object",
	"properties": {
		"findings": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": {"file": {"type": "string"}, "line": {"type": "integer"}},
				"required": ["file", "line"]
			}
		},
		"summary": {"type": "string"}
	},
	"required": ["findings"]
}`

func newFindingsTool(t *testing.T, onRecorded func()) *StructuredOutputTool {
	t.Helper()
	tool, err := NewStructuredOutputTool(json.RawMessage(findingsSchema), onRecorded)
	if err != nil {
		t.Fatalf("NewStructuredOutputTool: %v", err)
	}
	return tool
}

// decodeInput parses a call's input the way the engine does.
func decodeInput(t *testing.T, raw string) ToolInput {
	t.Helper()
	params := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return ToolInput{Name: StructuredOutputToolName, Params: params, Raw: raw}
}

// A step that found nothing answers with an empty list, which the schema
// allows. The tool has to accept it, and still turn away an object the schema
// rejects, with an error the child can act on.
func TestStructuredOutputValidatesAgainstTheWholeSchema(t *testing.T) {
	tool := newFindingsTool(t, nil)
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "empty required array", input: `{"findings": []}`},
		{name: "blank optional string", input: `{"findings": [], "summary": ""}`},
		{name: "matching items", input: `{"findings": [{"file": "a.go", "line": 3}]}`},
		{name: "missing required property", input: `{"summary": "none"}`, wantErr: true},
		{name: "wrong type", input: `{"findings": "none"}`, wantErr: true},
		{name: "nested mismatch", input: `{"findings": [{"file": "a.go", "line": "three"}]}`, wantErr: true},
		{name: "fractional integer", input: `{"findings": [{"file": "a.go", "line": 3.5}]}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tool.Validate(decodeInput(t, tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Validate(%s) accepted input the schema rejects", tt.input)
				}
				if !strings.Contains(err.Error(), "call structured_output again") {
					t.Fatalf("error %q does not tell the child to retry", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate(%s): %v", tt.input, err)
			}
		})
	}
}

// nami's own schema check rejects an empty required array, which is right for
// its tools and wrong for a user's schema. ValidateToolCall must leave
// structured_output to its own, complete check.
func TestValidateToolCallSkipsThePartialCheckForStructuredOutput(t *testing.T) {
	tool := newFindingsTool(t, nil)
	input := decodeInput(t, `{"findings": []}`)
	if err := validateRequiredParams(tool.Name(), input.Params, tool.InputSchema()); err == nil {
		t.Fatal("the partial check accepts an empty required array, so this test no longer guards anything")
	}
	if err := ValidateToolCall(tool, input); err != nil {
		t.Fatalf("ValidateToolCall rejected a valid result: %v", err)
	}
	if err := ValidateToolCall(tool, decodeInput(t, `{"findings": "none"}`)); err == nil {
		t.Fatal("ValidateToolCall accepted a result the schema rejects")
	}
}

// A valid call is the child's answer: it is recorded and the engine is told,
// so it can end the run.
func TestStructuredOutputRecordsTheResult(t *testing.T) {
	recorded := 0
	tool := newFindingsTool(t, func() { recorded++ })
	if _, ok := tool.Value(); ok {
		t.Fatal("a tool that was never called has a value")
	}

	if _, err := tool.Execute(t.Context(), decodeInput(t, `{"findings": "none"}`)); err == nil {
		t.Fatal("Execute recorded a result the schema rejects")
	}
	if _, ok := tool.Value(); ok || recorded != 0 {
		t.Fatalf("a rejected call was recorded (callbacks %d)", recorded)
	}

	if _, err := tool.Execute(t.Context(), decodeInput(t, `{"findings": [{"file": "a.go", "line": 3}]}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	value, ok := tool.Value()
	if !ok || recorded != 1 {
		t.Fatalf("value recorded = %v, callbacks = %d; want a value and one callback", ok, recorded)
	}
	if string(value) != `{"findings":[{"file":"a.go","line":3}]}` {
		t.Fatalf("value = %s", value)
	}
}

func TestStructuredOutputToolRejectsUnusableSchemas(t *testing.T) {
	tests := []struct {
		name   string
		schema string
	}{
		{name: "not JSON", schema: `{"type":`},
		{name: "not an object", schema: `["findings"]`},
		{name: "null", schema: `null`},
		// Providers take tool parameters only as an object.
		{name: "array root", schema: `{"type": "array"}`},
		{name: "no type", schema: `{"properties": {}}`},
		{name: "invalid schema", schema: `{"type": "object", "properties": {"x": {"type": 7}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewStructuredOutputTool(json.RawMessage(tt.schema), nil); err == nil {
				t.Fatalf("NewStructuredOutputTool(%s) succeeded, want an error", tt.schema)
			}
		})
	}
}
