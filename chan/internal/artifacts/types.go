package artifacts

import "time"

// Kind identifies an artifact type.
type Kind string

const (
	KindTaskList           Kind = "task-list"
	KindImplementationPlan Kind = "implementation-plan"
	KindWalkthrough        Kind = "walkthrough"
	KindToolLog            Kind = "tool-log"
	KindSearchReport       Kind = "search-report"
	KindDiffPreview        Kind = "diff-preview"
	KindDiagram            Kind = "diagram"
	KindCodegenOutput      Kind = "codegen-output"
	KindCompactSummary     Kind = "compact-summary"
	KindKnowledgeItem      Kind = "knowledge-item"
)

// Scope defines the artifact's lifetime.
type Scope string

const (
	ScopeSession Scope = "session"
	ScopeUser    Scope = "user"
)

// Artifact is a stored work product.
type Artifact struct {
	ID          string         `json:"id"`
	Kind        Kind           `json:"kind"`
	Scope       Scope          `json:"scope"`
	Title       string         `json:"title"`
	MimeType    string         `json:"mime_type"`
	Source      string         `json:"source"` // tool or producer name
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	Version     int            `json:"version"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	ContentPath string         `json:"content_path"` // path to content file
}

// ArtifactVersion tracks a specific version of an artifact.
type ArtifactVersion struct {
	ArtifactID  string    `json:"artifact_id"`
	Version     int       `json:"version"`
	ContentPath string    `json:"content_path"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ArtifactRef is a lightweight reference to an artifact.
type ArtifactRef struct {
	ID        string    `json:"id"`
	Kind      Kind      `json:"kind"`
	Scope     Scope     `json:"scope"`
	Title     string    `json:"title"`
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}
