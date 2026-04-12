package artifacts

import "context"

// SaveRequest is the input to Service.Save.
type SaveRequest struct {
	ID       string
	Kind     Kind
	Scope    Scope
	Title    string
	MimeType string
	Source   string
	Content  []byte
	Metadata map[string]any
}

// LoadRequest is the input to Service.Load.
type LoadRequest struct {
	ID      string
	Version int // 0 = latest
}

// ListRequest is the input to Service.List.
type ListRequest struct {
	Kind  Kind  // optional filter
	Scope Scope // optional filter
}

// DeleteRequest is the input to Service.Delete.
type DeleteRequest struct {
	ID string
}

// VersionsRequest is the input to Service.Versions.
type VersionsRequest struct {
	ID string
}

// Service is the artifact storage interface.
type Service interface {
	Save(ctx context.Context, req SaveRequest) (ArtifactVersion, error)
	Load(ctx context.Context, req LoadRequest) (Artifact, error)
	List(ctx context.Context, req ListRequest) ([]ArtifactRef, error)
	Delete(ctx context.Context, req DeleteRequest) error
	Versions(ctx context.Context, req VersionsRequest) ([]ArtifactVersion, error)
}
