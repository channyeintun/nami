package debuglog

import (
	"runtime"
	"strings"
)

// LogGoroutineCount logs the current number of goroutines.
func LogGoroutineCount() {
	if !Enabled {
		return
	}
	Log("goroutine", "count", map[string]any{
		"count": runtime.NumGoroutine(),
	})
}

// SnapshotGoroutines returns a formatted goroutine dump filtered to gocode frames.
func SnapshotGoroutines() string {
	buf := make([]byte, 1<<20) // 1 MB
	n := runtime.Stack(buf, true)
	full := string(buf[:n])

	// Filter to goroutines that contain gocode frames.
	var filtered []string
	for _, block := range strings.Split(full, "\n\n") {
		if strings.Contains(block, "gocode") {
			filtered = append(filtered, block)
		}
	}
	return strings.Join(filtered, "\n\n")
}

// LogGoroutineSnapshot logs a full goroutine dump.
func LogGoroutineSnapshot(label string) {
	if !Enabled {
		return
	}
	snap := SnapshotGoroutines()
	// Truncate to avoid massive log entries.
	if len(snap) > 8192 {
		snap = snap[:8192] + "...(truncated)"
	}
	Log("goroutine", "snapshot", map[string]any{
		"label": label,
		"count": runtime.NumGoroutine(),
		"stack": snap,
	})
}
