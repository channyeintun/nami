package timing

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CheckpointRecorder captures named timestamps relative to a start time.
type CheckpointRecorder struct {
	mu          sync.Mutex
	startedAt   time.Time
	checkpoints map[string]time.Time
}

// Snapshot is an immutable view of recorded checkpoints.
type Snapshot struct {
	StartedAt   time.Time
	Checkpoints map[string]time.Time
}

// Record is a structured JSONL timing entry.
type Record struct {
	Kind        string           `json:"kind"`
	Metric      string           `json:"metric"`
	SessionID   string           `json:"session_id"`
	TurnID      int              `json:"turn_id,omitempty"`
	StartedAt   time.Time        `json:"started_at"`
	EndedAt     time.Time        `json:"ended_at"`
	DurationMS  int64            `json:"duration_ms"`
	Checkpoints map[string]any   `json:"checkpoints,omitempty"`
	DurationsMS map[string]int64 `json:"durations_ms,omitempty"`
	Metadata    map[string]any   `json:"metadata,omitempty"`
}

// Logger appends timing records to a session-local JSONL file.
type Logger struct {
	mu   sync.Mutex
	path string
}

// NewCheckpointRecorder creates a recorder anchored to startedAt.
func NewCheckpointRecorder(startedAt time.Time) *CheckpointRecorder {
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	return &CheckpointRecorder{
		startedAt:   startedAt,
		checkpoints: make(map[string]time.Time),
	}
}

// Mark records the current time for a named checkpoint. Repeated names are ignored.
func (r *CheckpointRecorder) Mark(name string) bool {
	return r.MarkAt(name, time.Now())
}

// MarkAt records a specific time for a named checkpoint. Repeated names are ignored.
func (r *CheckpointRecorder) MarkAt(name string, at time.Time) bool {
	if r == nil || name == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.checkpoints[name]; exists {
		return false
	}
	r.checkpoints[name] = at
	return true
}

// Snapshot returns a copy of the recorder state.
func (r *CheckpointRecorder) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	checkpoints := make(map[string]time.Time, len(r.checkpoints))
	for name, at := range r.checkpoints {
		checkpoints[name] = at
	}
	return Snapshot{
		StartedAt:   r.startedAt,
		Checkpoints: checkpoints,
	}
}

// NewSessionLogger writes structured timing records to timings.ndjson in sessionDir.
func NewSessionLogger(sessionDir string) *Logger {
	return &Logger{path: filepath.Join(sessionDir, "timings.ndjson")}
}

// AppendSnapshot converts a checkpoint snapshot into a record and appends it.
func (l *Logger) AppendSnapshot(kind, metric, sessionID string, turnID int, recorder *CheckpointRecorder, metadata map[string]any) error {
	if l == nil || recorder == nil {
		return nil
	}

	snapshot := recorder.Snapshot()
	endedAt := snapshot.StartedAt
	checkpointPayload := make(map[string]any, len(snapshot.Checkpoints))
	durations := make(map[string]int64, len(snapshot.Checkpoints))
	for name, at := range snapshot.Checkpoints {
		checkpointPayload[name] = at.UTC().Format(time.RFC3339Nano)
		durations[name] = at.Sub(snapshot.StartedAt).Milliseconds()
		if at.After(endedAt) {
			endedAt = at
		}
	}

	record := Record{
		Kind:        kind,
		Metric:      metric,
		SessionID:   sessionID,
		TurnID:      turnID,
		StartedAt:   snapshot.StartedAt.UTC(),
		EndedAt:     endedAt.UTC(),
		DurationMS:  endedAt.Sub(snapshot.StartedAt).Milliseconds(),
		Checkpoints: checkpointPayload,
		DurationsMS: durations,
		Metadata:    metadata,
	}
	return l.Append(record)
}

// Append writes a timing record as one JSONL line.
func (l *Logger) Append(record Record) error {
	if l == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return fmt.Errorf("create timing dir: %w", err)
	}

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal timing record: %w", err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open timing log: %w", err)
	}
	defer f.Close()

	if _, err := fmt.Fprintf(f, "%s\n", data); err != nil {
		return fmt.Errorf("append timing log: %w", err)
	}
	return nil
}
