package tools

import "context"

// PermissionLevel classifies the default permission posture for a tool.
type PermissionLevel int

const (
	PermissionReadOnly PermissionLevel = iota // auto-approve
	PermissionWrite                           // ask user
	PermissionExecute                         // ask user + security check
)

// ToolInput holds the parsed input for a tool invocation.
type ToolInput struct {
	Name   string
	Params map[string]any
	Raw    string // original JSON string
}

// ToolOutput holds a tool's result.
type ToolOutput struct {
	Output    string
	IsError   bool
	Truncated bool
	SpillPath string // non-empty if result was spilled to disk
}

// Tool is the interface every tool must implement.
type Tool interface {
	// Name returns the tool's identifier.
	Name() string

	// Description returns a human-readable description for the model.
	Description() string

	// InputSchema returns the JSON Schema for the tool's parameters.
	InputSchema() any

	// Permission returns the default permission level.
	Permission() PermissionLevel

	// IsConcurrencySafe returns true if this invocation can run in parallel.
	IsConcurrencySafe(input ToolInput) bool

	// Execute runs the tool with the given input.
	Execute(ctx context.Context, input ToolInput) (ToolOutput, error)
}

// MaxResultSizeChars is the default per-tool result budget.
const MaxResultSizeChars = 100_000

// PreviewChars is how many chars to keep inline when spilling.
const PreviewChars = 2000
