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
	MemoryRecallCostUSD      float64
	MemoryRecallInputTokens  int
	MemoryRecallOutputTokens int
	ChildAgentCostUSD        float64
	ChildAgentInputTokens    int
	ChildAgentOutputTokens   int
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

// RecordMemoryRecallCall records usage from the memory side-query while still contributing to aggregate totals.
func (t *Tracker) RecordMemoryRecallCall(model string, inputTokens, outputTokens, cacheRead, cacheCreation int, duration time.Duration, costUSD float64) {
	t.RecordAPICall(model, inputTokens, outputTokens, cacheRead, cacheCreation, duration, costUSD)

	t.mu.Lock()
	defer t.mu.Unlock()
	t.MemoryRecallCostUSD += costUSD
	t.MemoryRecallInputTokens += inputTokens
	t.MemoryRecallOutputTokens += outputTokens
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
	MemoryRecallCostUSD      float64
	MemoryRecallInputTokens  int
	MemoryRecallOutputTokens int
	ChildAgentCostUSD        float64
	ChildAgentInputTokens    int
	ChildAgentOutputTokens   int
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
		MemoryRecallCostUSD:      t.MemoryRecallCostUSD,
		MemoryRecallInputTokens:  t.MemoryRecallInputTokens,
		MemoryRecallOutputTokens: t.MemoryRecallOutputTokens,
		ChildAgentCostUSD:        t.ChildAgentCostUSD,
		ChildAgentInputTokens:    t.ChildAgentInputTokens,
		ChildAgentOutputTokens:   t.ChildAgentOutputTokens,
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

// MergeSnapshot adds the values from another snapshot into this tracker.
func (t *Tracker) MergeSnapshot(snapshot TrackerSnapshot) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.mergeSnapshotLocked(snapshot)
}

// RecordChildAgentSnapshot merges child-agent usage into aggregate totals and preserves a separate child-agent subtotal.
func (t *Tracker) RecordChildAgentSnapshot(snapshot TrackerSnapshot) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.mergeSnapshotLocked(snapshot)
	// Add only the child's own cost (excluding its own nested children) so that
	// ChildAgent* fields are not double-counted: mergeSnapshotLocked already
	// added snapshot.ChildAgentCostUSD via the ChildAgent* passthrough.
	ownCost := snapshot.TotalCostUSD - snapshot.ChildAgentCostUSD
	t.ChildAgentCostUSD += ownCost
	ownInput := snapshot.TotalInputTokens - snapshot.ChildAgentInputTokens
	t.ChildAgentInputTokens += ownInput
	ownOutput := snapshot.TotalOutputTokens - snapshot.ChildAgentOutputTokens
	t.ChildAgentOutputTokens += ownOutput
}

func (t *Tracker) mergeSnapshotLocked(snapshot TrackerSnapshot) {

	t.TotalCostUSD += snapshot.TotalCostUSD
	t.TotalInputTokens += snapshot.TotalInputTokens
	t.TotalOutputTokens += snapshot.TotalOutputTokens
	t.TotalCacheReadTokens += snapshot.TotalCacheReadTokens
	t.TotalCacheCreationTokens += snapshot.TotalCacheCreationTokens
	t.MemoryRecallCostUSD += snapshot.MemoryRecallCostUSD
	t.MemoryRecallInputTokens += snapshot.MemoryRecallInputTokens
	t.MemoryRecallOutputTokens += snapshot.MemoryRecallOutputTokens
	t.ChildAgentCostUSD += snapshot.ChildAgentCostUSD
	t.ChildAgentInputTokens += snapshot.ChildAgentInputTokens
	t.ChildAgentOutputTokens += snapshot.ChildAgentOutputTokens
	t.TotalAPIDuration += snapshot.TotalAPIDuration
	t.TotalToolDuration += snapshot.TotalToolDuration
	t.TotalLinesAdded += snapshot.TotalLinesAdded
	t.TotalLinesRemoved += snapshot.TotalLinesRemoved

	for name, entry := range snapshot.ModelUsage {
		current, ok := t.ModelUsage[name]
		if !ok {
			copied := *entry
			t.ModelUsage[name] = &copied
			continue
		}
		current.InputTokens += entry.InputTokens
		current.OutputTokens += entry.OutputTokens
		current.CacheReadTokens += entry.CacheReadTokens
		current.CostUSD += entry.CostUSD
		current.CallCount += entry.CallCount
		if entry.ContextWindow > current.ContextWindow {
			current.ContextWindow = entry.ContextWindow
		}
	}
}
