package debuglog

import "runtime"

// LogGoroutineCount logs the current number of goroutines.
func LogGoroutineCount() {
	if !Enabled {
		return
	}
	Log("goroutine", "count", map[string]any{
		"count": runtime.NumGoroutine(),
	})
}
