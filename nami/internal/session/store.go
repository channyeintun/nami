package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/config"
	"github.com/channyeintun/nami/internal/ipc"
)

// Metadata holds session state for persistence and resume.
type Metadata struct {
	SessionID     string    `json:"session_id"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Mode          string    `json:"mode"`
	Model         string    `json:"model"`
	SubagentModel string    `json:"subagent_model,omitempty"`
	CWD           string    `json:"cwd"`
	Branch        string    `json:"branch"`
	TotalCostUSD  float64   `json:"total_cost_usd"`
	Title         string    `json:"title,omitempty"`
}

// Store handles session transcript persistence.
type Store struct {
	baseDir string
	// metadataMu serializes metadata read-modify-write cycles so concurrent
	// writers (e.g. the async title generator) cannot drop each other's fields.
	metadataMu sync.Mutex
}

// NewStore creates a session store at the given base directory.
func NewStore(baseDir string) *Store {
	return &Store{baseDir: baseDir}
}

// DefaultBaseDir returns the platform-correct session root.
func DefaultBaseDir() string {
	return config.SessionsDir()
}

// SessionDir returns the directory for a specific session.
func (s *Store) SessionDir(sessionID string) string {
	return filepath.Join(s.baseDir, sessionID)
}

// SaveMetadata persists session metadata.
func (s *Store) SaveMetadata(meta Metadata) error {
	s.metadataMu.Lock()
	defer s.metadataMu.Unlock()
	return s.saveMetadataLocked(meta)
}

// UpdateMetadata loads the existing metadata (a zero value with SessionID set
// when none exists), applies update, and persists the result as one atomic
// read-modify-write cycle.
func (s *Store) UpdateMetadata(sessionID string, update func(Metadata) Metadata) error {
	s.metadataMu.Lock()
	defer s.metadataMu.Unlock()
	existing, err := s.LoadMetadata(sessionID)
	if err != nil {
		existing = Metadata{SessionID: sessionID}
	}
	return s.saveMetadataLocked(update(existing))
}

func (s *Store) saveMetadataLocked(meta Metadata) error {
	dir := s.SessionDir(meta.SessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	return writeFileAtomic(filepath.Join(dir, "metadata.json"), data, 0o644)
}

// LoadMetadata reads session metadata.
func (s *Store) LoadMetadata(sessionID string) (Metadata, error) {
	data, err := os.ReadFile(filepath.Join(s.SessionDir(sessionID), "metadata.json"))
	if err != nil {
		return Metadata{}, fmt.Errorf("read metadata: %w", err)
	}
	var meta Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return Metadata{}, fmt.Errorf("unmarshal metadata: %w", err)
	}
	return meta, nil
}

// AppendTranscript appends a message to the session transcript (NDJSON).
func (s *Store) AppendTranscript(sessionID string, msg api.Message) error {
	dir := s.SessionDir(sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "transcript.ndjson"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "%s\n", data)
	return err
}

// SaveTranscript rewrites the full transcript for a session as NDJSON.
func (s *Store) SaveTranscript(sessionID string, messages []api.Message) error {
	dir := s.SessionDir(sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	path := filepath.Join(dir, "transcript.ndjson")
	tmp, err := os.CreateTemp(dir, ".transcript.ndjson-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	encoder := json.NewEncoder(tmp)
	for _, msg := range messages {
		if err := encoder.Encode(msg); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// SaveConversationTimeline persists the hydrated conversation timeline for a session.
func (s *Store) SaveConversationTimeline(sessionID string, payload ipc.ConversationHydratedPayload) error {
	dir := s.SessionDir(sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal conversation timeline: %w", err)
	}
	return writeFileAtomic(filepath.Join(dir, "timeline.json"), data, 0o644)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// LoadConversationTimeline reads the hydrated conversation timeline for a session.
func (s *Store) LoadConversationTimeline(sessionID string) (ipc.ConversationHydratedPayload, error) {
	data, err := os.ReadFile(filepath.Join(s.SessionDir(sessionID), "timeline.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ipc.ConversationHydratedPayload{}, nil
		}
		return ipc.ConversationHydratedPayload{}, fmt.Errorf("read conversation timeline: %w", err)
	}
	var payload ipc.ConversationHydratedPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return ipc.ConversationHydratedPayload{}, fmt.Errorf("unmarshal conversation timeline: %w", err)
	}
	return payload, nil
}

// ListSessions returns all available session IDs, most recent first.
func (s *Store) ListSessions() ([]Metadata, error) {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var sessions []Metadata
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		meta, err := s.LoadMetadata(entry.Name())
		if err != nil {
			continue
		}
		sessions = append(sessions, meta)
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}

// LoadTranscript reads all persisted transcript messages for a session.
func (s *Store) LoadTranscript(sessionID string) ([]api.Message, error) {
	path := filepath.Join(s.SessionDir(sessionID), "transcript.ndjson")
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	messages := make([]api.Message, 0, 64)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg api.Message
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, fmt.Errorf("decode transcript message: %w", err)
		}
		messages = append(messages, msg)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan transcript: %w", err)
	}

	return messages, nil
}
