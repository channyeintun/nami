package artifacts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LocalStore implements Service using the local filesystem.
type LocalStore struct {
	baseDir string
}

// NewLocalStore creates a local filesystem artifact store.
func NewLocalStore(baseDir string) *LocalStore {
	return &LocalStore{baseDir: baseDir}
}

func (s *LocalStore) Save(_ context.Context, req SaveRequest) (ArtifactVersion, error) {
	id := generateID()
	artDir := filepath.Join(s.baseDir, string(req.Kind), id)
	if err := os.MkdirAll(artDir, 0o755); err != nil {
		return ArtifactVersion{}, fmt.Errorf("create artifact dir: %w", err)
	}

	// Write content
	contentPath := filepath.Join(artDir, "v1.content")
	if err := os.WriteFile(contentPath, req.Content, 0o644); err != nil {
		return ArtifactVersion{}, fmt.Errorf("write content: %w", err)
	}

	// Write metadata
	art := Artifact{
		ID:          id,
		Kind:        req.Kind,
		Scope:       req.Scope,
		Title:       req.Title,
		MimeType:    req.MimeType,
		Source:      req.Source,
		CreatedAt:   time.Now(),
		Version:     1,
		Metadata:    req.Metadata,
		ContentPath: contentPath,
	}
	metaData, err := json.Marshal(art)
	if err != nil {
		return ArtifactVersion{}, fmt.Errorf("marshal metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(artDir, "meta.json"), metaData, 0o644); err != nil {
		return ArtifactVersion{}, fmt.Errorf("write metadata: %w", err)
	}

	return ArtifactVersion{
		ArtifactID:  id,
		Version:     1,
		ContentPath: contentPath,
		CreatedAt:   art.CreatedAt,
	}, nil
}

func (s *LocalStore) Load(_ context.Context, req LoadRequest) (Artifact, error) {
	// Search for artifact by ID across all kind directories
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return Artifact{}, fmt.Errorf("read base dir: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		metaPath := filepath.Join(s.baseDir, entry.Name(), req.ID, "meta.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var art Artifact
		if err := json.Unmarshal(data, &art); err != nil {
			continue
		}
		return art, nil
	}
	return Artifact{}, fmt.Errorf("artifact not found: %s", req.ID)
}

func (s *LocalStore) List(_ context.Context, req ListRequest) ([]ArtifactRef, error) {
	var refs []ArtifactRef
	kinds, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, kindDir := range kinds {
		if !kindDir.IsDir() {
			continue
		}
		if req.Kind != "" && Kind(kindDir.Name()) != req.Kind {
			continue
		}
		artDirs, err := os.ReadDir(filepath.Join(s.baseDir, kindDir.Name()))
		if err != nil {
			continue
		}
		for _, artDir := range artDirs {
			metaPath := filepath.Join(s.baseDir, kindDir.Name(), artDir.Name(), "meta.json")
			data, err := os.ReadFile(metaPath)
			if err != nil {
				continue
			}
			var art Artifact
			if err := json.Unmarshal(data, &art); err != nil {
				continue
			}
			if req.Scope != "" && art.Scope != req.Scope {
				continue
			}
			refs = append(refs, ArtifactRef{ID: art.ID, Kind: art.Kind, Title: art.Title})
		}
	}
	return refs, nil
}

func (s *LocalStore) Delete(_ context.Context, req DeleteRequest) error {
	kinds, _ := os.ReadDir(s.baseDir)
	for _, kindDir := range kinds {
		artPath := filepath.Join(s.baseDir, kindDir.Name(), req.ID)
		if _, err := os.Stat(artPath); err == nil {
			return os.RemoveAll(artPath)
		}
	}
	return fmt.Errorf("artifact not found: %s", req.ID)
}

func (s *LocalStore) Versions(_ context.Context, req VersionsRequest) ([]ArtifactVersion, error) {
	// Simplified: returns only latest version for now
	art, err := s.Load(context.Background(), LoadRequest{ID: req.ID})
	if err != nil {
		return nil, err
	}
	return []ArtifactVersion{{
		ArtifactID:  art.ID,
		Version:     art.Version,
		ContentPath: art.ContentPath,
		CreatedAt:   art.CreatedAt,
	}}, nil
}

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
