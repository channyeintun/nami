package cost

import (
	"sync"
	"time"
)

// ModelUsageEntry tracks per-model usage.
type ModelUsageEntry struct {
	ModelName       string
	InputTokens     int
	OutputTokens    int
	CacheReadTokens int
	CostUSD         float64
	CallCount       int
	ContextWindow   int
}

// Tracker accumulates session-wide cost and usage data.
type Tracker struct {
	mu                       sync.Mutex
	TotalCostUSD             float64
	TotalInputTokens         int
	TotalOutputTokens        int
	TotalCacheReadTokens     int
	TotalCacheCreationTokens int
	TotalAPIDuration         time.Duration
	TotalToolDuration        time.Duration
	TotalLinesAdded          int
	TotalLinesRemoved        int
	ModelUsage               map[string]*ModelUsageEntry
}

// NewTracker creates an empty cost tracker.
func NewTracker() *Tracker {
	return &Tracker{
		ModelUsage: make(map[string]*ModelUsageEntry),
	}
}

// RecordAPICall records usage from a model API call.
func (t *Tracker) RecordAPICall(model string, inputTokens, outputTokens, cacheRead, cacheCreation int, duration time.Duration, costUSD float64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.TotalInputTokens += inputTokens
	t.TotalOutputTokens += outputTokens
	t.TotalCacheReadTokens += cacheRead
	t.TotalCacheCreationTokens += cacheCreation
	t.TotalAPIDuration += duration
	t.TotalCostUSD += costUSD

	entry, ok := t.ModelUsage[model]
	if !ok {
		entry = &ModelUsageEntry{ModelName: model}
		t.ModelUsage[model] = entry
	}
	entry.InputTokens += inputTokens
	entry.OutputTokens += outputTokens
	entry.CacheReadTokens += cacheRead
	entry.CostUSD += costUSD
	entry.CallCount++
}

// RecordToolDuration records time spent executing a tool.
func (t *Tracker) RecordToolDuration(d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.TotalToolDuration += d
}

// RecordLineChanges records lines added/removed from file edits.
func (t *Tracker) RecordLineChanges(added, removed int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.TotalLinesAdded += added
	t.TotalLinesRemoved += removed
}

// Snapshot returns a snapshot of the current cost state.
type TrackerSnapshot struct {
	TotalCostUSD             float64
	TotalInputTokens         int
	TotalOutputTokens        int
	TotalCacheReadTokens     int
	TotalCacheCreationTokens int
	TotalAPIDuration         time.Duration
	TotalToolDuration        time.Duration
	TotalLinesAdded          int
	TotalLinesRemoved        int
	ModelUsage               map[string]*ModelUsageEntry
}

// Snapshot returns a copy of the current cost state (safe for concurrent reads).
func (t *Tracker) Snapshot() TrackerSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	snap := TrackerSnapshot{
		TotalCostUSD:             t.TotalCostUSD,
		TotalInputTokens:         t.TotalInputTokens,
		TotalOutputTokens:        t.TotalOutputTokens,
		TotalCacheReadTokens:     t.TotalCacheReadTokens,
		TotalCacheCreationTokens: t.TotalCacheCreationTokens,
		TotalAPIDuration:         t.TotalAPIDuration,
		TotalToolDuration:        t.TotalToolDuration,
		TotalLinesAdded:          t.TotalLinesAdded,
		TotalLinesRemoved:        t.TotalLinesRemoved,
		ModelUsage:               make(map[string]*ModelUsageEntry, len(t.ModelUsage)),
	}
	for k, v := range t.ModelUsage {
		entry := *v
		snap.ModelUsage[k] = &entry
	}
	return snap
}
