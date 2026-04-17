package tools

import (
	"os"
	"sync"
	"time"
)

type FileReadState struct {
	mu      sync.RWMutex
	entries map[fileReadStateKey]fileReadStateEntry
	metrics []FileReadMetric
}

type fileReadStateKey struct {
	path   string
	offset int
	limit  int
}

type fileReadStateEntry struct {
	size    int64
	modTime time.Time
}

type FileReadMetric struct {
	RequestedOffset     int
	RequestedLimit      int
	LinesReturned       int
	BytesReturned       int
	Truncated           bool
	UnchangedHit        bool
	LegacyParamRejected bool
}

const maxFileReadMetrics = 256

func NewFileReadState() *FileReadState {
	return &FileReadState{entries: make(map[fileReadStateKey]fileReadStateEntry), metrics: make([]FileReadMetric, 0, maxFileReadMetrics)}
}

func (s *FileReadState) SeenUnchanged(path string, offset, limit int, info os.FileInfo) bool {
	if s == nil || info == nil {
		return false
	}
	key := fileReadStateKey{path: path, offset: offset, limit: limit}
	s.mu.RLock()
	entry, ok := s.entries[key]
	s.mu.RUnlock()
	if !ok {
		return false
	}
	return entry.size == info.Size() && entry.modTime.Equal(info.ModTime())
}

func (s *FileReadState) Remember(path string, offset, limit int, info os.FileInfo) {
	if s == nil || info == nil {
		return
	}
	key := fileReadStateKey{path: path, offset: offset, limit: limit}
	s.mu.Lock()
	s.entries[key] = fileReadStateEntry{size: info.Size(), modTime: info.ModTime()}
	s.mu.Unlock()
}

func (s *FileReadState) Invalidate(path string) {
	if s == nil || path == "" {
		return
	}
	s.mu.Lock()
	for key := range s.entries {
		if key.path == path {
			delete(s.entries, key)
		}
	}
	s.mu.Unlock()
}

func (s *FileReadState) RecordMetric(metric FileReadMetric) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if len(s.metrics) == maxFileReadMetrics {
		copy(s.metrics, s.metrics[1:])
		s.metrics = s.metrics[:maxFileReadMetrics-1]
	}
	s.metrics = append(s.metrics, metric)
	s.mu.Unlock()
}

func (s *FileReadState) MetricsSnapshot() []FileReadMetric {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	metrics := make([]FileReadMetric, len(s.metrics))
	copy(metrics, s.metrics)
	return metrics
}

var globalFileReadState struct {
	mu sync.RWMutex
	s  *FileReadState
}

func SetGlobalFileReadState(state *FileReadState) {
	globalFileReadState.mu.Lock()
	defer globalFileReadState.mu.Unlock()
	globalFileReadState.s = state
}

func GetGlobalFileReadState() *FileReadState {
	globalFileReadState.mu.RLock()
	defer globalFileReadState.mu.RUnlock()
	return globalFileReadState.s
}

func invalidateFileReadState(path string) {
	if state := GetGlobalFileReadState(); state != nil {
		state.Invalidate(path)
	}
}

func recordFileReadMetric(metric FileReadMetric) {
	if state := GetGlobalFileReadState(); state != nil {
		state.RecordMetric(metric)
	}
}
