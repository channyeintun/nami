package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/channyeintun/gocode/internal/api"
)

// Metadata holds session state for persistence and resume.
type Metadata struct {
	SessionID    string    `json:"session_id"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Mode         string    `json:"mode"`
	Model        string    `json:"model"`
	CWD          string    `json:"cwd"`
	Branch       string    `json:"branch"`
	TotalCostUSD float64   `json:"total_cost_usd"`
	Title        string    `json:"title,omitempty"`
}

// Store handles session transcript persistence.
type Store struct {
	baseDir string
}

// NewStore creates a session store at the given base directory.
func NewStore(baseDir string) *Store {
	return &Store{baseDir: baseDir}
}

// DefaultBaseDir returns ~/.config/gocode/sessions/.
func DefaultBaseDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "gocode", "sessions")
}

// SessionDir returns the directory for a specific session.
func (s *Store) SessionDir(sessionID string) string {
	return filepath.Join(s.baseDir, sessionID)
}

// SaveMetadata persists session metadata.
func (s *Store) SaveMetadata(meta Metadata) error {
	dir := s.SessionDir(meta.SessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "metadata.json"), data, 0o644)
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

	f, err := os.Create(filepath.Join(dir, "transcript.ndjson"))
	if err != nil {
		return err
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	for _, msg := range messages {
		if err := encoder.Encode(msg); err != nil {
			return err
		}
	}
	return nil
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
