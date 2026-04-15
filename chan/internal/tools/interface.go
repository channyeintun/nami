package tools

import (
	"context"

	artifactspkg "github.com/channyeintun/chan/internal/artifacts"
)

// PermissionLevel classifies the default permission posture for a tool.
type PermissionLevel int

const (
	PermissionReadOnly PermissionLevel = iota // auto-approve
	PermissionWrite                           // ask user
	PermissionExecute                         // ask user + security check
)

// ConcurrencyDecision classifies how a tool invocation should be scheduled.
type ConcurrencyDecision int

const (
	ConcurrencySerial ConcurrencyDecision = iota
	ConcurrencyParallel
)

// ToolInput holds the parsed input for a tool invocation.
type ToolInput struct {
	Name   string
	Params map[string]any
	Raw    string // original JSON string
}

// ToolOutput holds a tool's result.
type ToolOutput struct {
	Output      string
	IsError     bool
	Truncated   bool
	SpillPath   string // non-empty if result was spilled to disk
	FilePath    string
	Preview     string
	Insertions  int
	Deletions   int
	Diagnostics string
	ErrorKind   string
	ErrorHint   string
	Artifacts   []ArtifactMutation
}

// PermissionTarget describes the primary object a tool acts on for approval UI.
type PermissionTarget struct {
	Kind       string
	Value      string
	WorkingDir string
}

// ArtifactMutation describes an artifact created or updated during tool execution.
type ArtifactMutation struct {
	Artifact artifactspkg.Artifact
	Content  string
	Created  bool
	Focused  bool // true if this artifact should become the primary focus in the UI
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

	// Concurrency reports whether this invocation must run serially or can join a parallel batch.
	Concurrency(input ToolInput) ConcurrencyDecision

	// Execute runs the tool with the given input.
	Execute(ctx context.Context, input ToolInput) (ToolOutput, error)
}

// SemanticValidator allows a tool to reject invalid or low-value calls before
// permission resolution and execution.
type SemanticValidator interface {
	Validate(input ToolInput) error
}

// PermissionTargetProvider lets a tool override the generic permission target
// heuristics used for approval prompts.
type PermissionTargetProvider interface {
	PermissionTarget(input ToolInput) PermissionTarget
}

// MaxResultSizeChars is the default per-tool result budget.
const MaxResultSizeChars = 100_000

// PreviewChars is how many chars to keep inline when spilling.
const PreviewChars = 2000
