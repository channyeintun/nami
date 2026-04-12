package artifacts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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
	now := time.Now()
	id := strings.TrimSpace(req.ID)
	if id != "" {
		var err error
		id, err = sanitizeArtifactID(id)
		if err != nil {
			return ArtifactVersion{}, err
		}
	}
	createdAt := now
	version := 1
	artDir := ""

	if id != "" {
		existing, existingDir, err := s.findByID(id)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return ArtifactVersion{}, fmt.Errorf("load existing artifact: %w", err)
		}
		if err == nil {
			if req.Kind != "" && req.Kind != existing.Kind {
				return ArtifactVersion{}, fmt.Errorf("artifact kind mismatch: have %q want %q", existing.Kind, req.Kind)
			}
			createdAt = existing.CreatedAt
			version = existing.Version + 1
			artDir = existingDir
			if req.Kind == "" {
				req.Kind = existing.Kind
			}
			if req.Scope == "" {
				req.Scope = existing.Scope
			}
			if strings.TrimSpace(req.Title) == "" {
				req.Title = existing.Title
			}
			if strings.TrimSpace(req.MimeType) == "" {
				req.MimeType = existing.MimeType
			}
			if strings.TrimSpace(req.Source) == "" {
				req.Source = existing.Source
			}
			if req.Metadata == nil {
				req.Metadata = cloneMetadata(existing.Metadata)
			} else {
				req.Metadata = mergeMetadata(existing.Metadata, req.Metadata)
			}
		}
	}

	if id == "" {
		id = generateID()
	}
	if req.Kind == "" {
		return ArtifactVersion{}, fmt.Errorf("artifact kind is required")
	}
	if req.Scope == "" {
		req.Scope = ScopeSession
	}
	if strings.TrimSpace(req.Title) == "" {
		req.Title = string(req.Kind)
	}
	if strings.TrimSpace(req.MimeType) == "" {
		req.MimeType = MarkdownMimeType
	}
	if artDir == "" {
		artDir = filepath.Join(s.baseDir, string(req.Kind), id)
	}

	if err := os.MkdirAll(artDir, 0o755); err != nil {
		return ArtifactVersion{}, fmt.Errorf("create artifact dir: %w", err)
	}

	contentPath := filepath.Join(artDir, fmt.Sprintf("v%d.md", version))
	if err := os.WriteFile(contentPath, req.Content, 0o644); err != nil {
		return ArtifactVersion{}, fmt.Errorf("write content: %w", err)
	}

	art := Artifact{
		ID:          id,
		Kind:        req.Kind,
		Scope:       req.Scope,
		Title:       req.Title,
		MimeType:    req.MimeType,
		Source:      req.Source,
		CreatedAt:   createdAt,
		UpdatedAt:   now,
		Version:     version,
		Metadata:    cloneMetadata(req.Metadata),
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
		Version:     version,
		ContentPath: contentPath,
		CreatedAt:   createdAt,
		UpdatedAt:   now,
	}, nil
}

func (s *LocalStore) Load(_ context.Context, req LoadRequest) (Artifact, error) {
	id, err := sanitizeArtifactID(req.ID)
	if err != nil {
		return Artifact{}, err
	}
	art, artDir, err := s.findByID(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Artifact{}, fmt.Errorf("artifact not found: %s", id)
		}
		return Artifact{}, err
	}

	if req.Version <= 0 || req.Version == art.Version {
		return art, nil
	}

	contentPath := filepath.Join(artDir, fmt.Sprintf("v%d.md", req.Version))
	info, err := os.Stat(contentPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Artifact{}, fmt.Errorf("artifact version not found: %s@v%d", id, req.Version)
		}
		return Artifact{}, fmt.Errorf("stat content version: %w", err)
	}
	art.Version = req.Version
	art.UpdatedAt = info.ModTime()
	art.ContentPath = contentPath
	return art, nil
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
			art, err := loadMetadata(metaPath)
			if err != nil {
				continue
			}
			if req.Scope != "" && art.Scope != req.Scope {
				continue
			}
			refs = append(refs, ArtifactRef{
				ID:        art.ID,
				Kind:      art.Kind,
				Scope:     art.Scope,
				Title:     art.Title,
				Version:   art.Version,
				UpdatedAt: art.UpdatedAt,
			})
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].UpdatedAt.Equal(refs[j].UpdatedAt) {
			return refs[i].Title < refs[j].Title
		}
		return refs[i].UpdatedAt.After(refs[j].UpdatedAt)
	})
	return refs, nil
}

func (s *LocalStore) Delete(_ context.Context, req DeleteRequest) error {
	id, err := sanitizeArtifactID(req.ID)
	if err != nil {
		return err
	}
	kinds, _ := os.ReadDir(s.baseDir)
	for _, kindDir := range kinds {
		artPath := filepath.Join(s.baseDir, kindDir.Name(), id)
		if _, err := os.Stat(artPath); err == nil {
			return os.RemoveAll(artPath)
		}
	}
	return fmt.Errorf("artifact not found: %s", id)
}

func (s *LocalStore) Versions(_ context.Context, req VersionsRequest) ([]ArtifactVersion, error) {
	id, err := sanitizeArtifactID(req.ID)
	if err != nil {
		return nil, err
	}
	art, artDir, err := s.findByID(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("artifact not found: %s", id)
		}
		return nil, err
	}

	entries, err := os.ReadDir(artDir)
	if err != nil {
		return nil, fmt.Errorf("read artifact dir: %w", err)
	}

	versions := make([]ArtifactVersion, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "v") || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		versionNumber, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(entry.Name(), "v"), ".md"))
		if err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		versions = append(versions, ArtifactVersion{
			ArtifactID:  art.ID,
			Version:     versionNumber,
			ContentPath: filepath.Join(artDir, entry.Name()),
			CreatedAt:   art.CreatedAt,
			UpdatedAt:   info.ModTime(),
		})
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("artifact not found: %s", id)
	}
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].Version > versions[j].Version
	})
	return versions, nil
}

func (s *LocalStore) findByID(id string) (Artifact, string, error) {
	id, err := sanitizeArtifactID(id)
	if err != nil {
		return Artifact{}, "", err
	}

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Artifact{}, "", os.ErrNotExist
		}
		return Artifact{}, "", fmt.Errorf("read base dir: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		artDir := filepath.Join(s.baseDir, entry.Name(), id)
		art, err := loadMetadata(filepath.Join(artDir, "meta.json"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			continue
		}
		return art, artDir, nil
	}
	return Artifact{}, "", os.ErrNotExist
}

func loadMetadata(metaPath string) (Artifact, error) {
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return Artifact{}, err
	}
	var art Artifact
	if err := json.Unmarshal(data, &art); err != nil {
		return Artifact{}, err
	}
	return art, nil
}

func cloneMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return nil
	}
	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

func mergeMetadata(base map[string]any, overrides map[string]any) map[string]any {
	merged := cloneMetadata(base)
	if merged == nil {
		merged = make(map[string]any, len(overrides))
	}
	for key, value := range overrides {
		merged[key] = value
	}
	return merged
}

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func sanitizeArtifactID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("artifact id is required")
	}
	if filepath.IsAbs(id) {
		return "", fmt.Errorf("invalid artifact id %q", id)
	}
	cleaned := filepath.Clean(id)
	if cleaned == "." || cleaned == ".." || cleaned != id {
		return "", fmt.Errorf("invalid artifact id %q", id)
	}
	if strings.Contains(id, "/") || strings.Contains(id, `\\`) {
		return "", fmt.Errorf("invalid artifact id %q", id)
	}
	return id, nil
}
