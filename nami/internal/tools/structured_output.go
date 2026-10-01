package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"

	workflowpkg "github.com/channyeintun/nami/internal/workflow"
)

// StructuredOutputToolName is the tool a child agent with an output schema
// calls to deliver its result.
const StructuredOutputToolName = "structured_output"

// StructuredOutputTool is how a child agent returns a result that a program,
// not a person, reads. Its input schema is the result's JSON Schema, so the
// call's input is the result itself. Each child gets its own tool, because
// the tool keeps that child's result.
type StructuredOutputTool struct {
	inputSchema map[string]any
	schema      *jsonschema.Resolved
	onRecorded  func()

	mu       sync.Mutex
	value    json.RawMessage
	recorded bool
}

// NewStructuredOutputTool makes the structured_output tool for one child. The
// schema's root must be an object schema, because providers take a tool's
// parameters only as an object. onRecorded, when set, runs after every valid
// call; the engine uses it to end the child's run.
func NewStructuredOutputTool(schema json.RawMessage, onRecorded func()) (*StructuredOutputTool, error) {
	var inputSchema map[string]any
	if err := json.Unmarshal(schema, &inputSchema); err != nil {
		return nil, fmt.Errorf("output schema must be a JSON object: %w", err)
	}
	if inputSchema["type"] != "object" {
		return nil, errors.New(`output schema must have type "object" at its root`)
	}
	resolved, err := workflowpkg.ResolveSchema(schema)
	if err != nil {
		return nil, fmt.Errorf("output schema is not a valid JSON Schema: %w", err)
	}
	return &StructuredOutputTool{inputSchema: inputSchema, schema: resolved, onRecorded: onRecorded}, nil
}

func (t *StructuredOutputTool) Name() string {
	return StructuredOutputToolName
}

func (t *StructuredOutputTool) Description() string {
	return "Deliver your final result. The input is the result itself: an object that matches this tool's schema. Call it exactly once, when your work is done; a valid call ends your run. If the call reports a schema mismatch, fix the object and call it again."
}

func (t *StructuredOutputTool) InputSchema() any {
	return t.inputSchema
}

// Permission is read-only: the call changes nothing outside the child, and a
// read-only Explore child must be able to make it.
func (t *StructuredOutputTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *StructuredOutputTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

// Validate checks the input against the whole schema. A mismatch comes back
// to the child as the call's error, so it can fix the object and retry.
func (t *StructuredOutputTool) Validate(input ToolInput) error {
	if err := t.schema.Validate(structuredOutputInstance(input)); err != nil {
		return fmt.Errorf("structured_output input does not match the schema, so fix the object and call structured_output again: %w", err)
	}
	return nil
}

// ValidatesCompleteSchema marks Validate as a check against the whole schema,
// which nami's partial schema check would only get in the way of: a user's
// schema may allow an empty required array or a blank required string.
func (t *StructuredOutputTool) ValidatesCompleteSchema() bool {
	return true
}

func (t *StructuredOutputTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	// Checked again here so that no path into the tool can record a value
	// the schema rejects.
	if err := t.Validate(input); err != nil {
		return ToolOutput{}, err
	}
	value, err := json.Marshal(structuredOutputInstance(input))
	if err != nil {
		return ToolOutput{}, fmt.Errorf("encode structured output: %w", err)
	}
	t.mu.Lock()
	t.value = value
	t.recorded = true
	t.mu.Unlock()
	if t.onRecorded != nil {
		t.onRecorded()
	}
	return ToolOutput{Output: "Result recorded. Your run is complete."}, nil
}

// Value returns the result the child recorded, and whether it recorded one.
// A child that called the tool more than once is taken at its last call.
func (t *StructuredOutputTool) Value() (json.RawMessage, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.recorded {
		return nil, false
	}
	return append(json.RawMessage(nil), t.value...), true
}

// structuredOutputInstance is the call's input as a JSON value. A call with
// no input at all is the empty object, which the schema then judges.
func structuredOutputInstance(input ToolInput) map[string]any {
	if input.Params == nil {
		return map[string]any{}
	}
	return input.Params
}
