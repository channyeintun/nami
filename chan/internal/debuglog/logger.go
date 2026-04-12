package debuglog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// Enabled is set once at startup from GOCODE_DEBUG env or --debug flag.
var Enabled bool

const maxLogSize = 50 * 1024 * 1024 // 50 MB

var (
	mu      sync.Mutex
	logFile *os.File
	written int64
)

// Init opens debug.log in the given session directory.
// Must be called after Enabled is set to true.
func Init(sessionDir string) {
	if !Enabled {
		return
	}
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "debuglog: mkdir %s: %v\n", sessionDir, err)
		Enabled = false
		return
	}
	path := filepath.Join(sessionDir, "debug.log")
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "debuglog: create %s: %v\n", path, err)
		Enabled = false
		return
	}
	logFile = f
	written = 0
	fmt.Fprintf(os.Stderr, "debuglog: logging to %s\n", path)
}

// Close flushes and closes the log file.
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if logFile != nil {
		logFile.Close()
		logFile = nil
	}
}

// Log appends one JSONL line.
func Log(category, event string, fields map[string]any) {
	if !Enabled {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if logFile == nil || written >= maxLogSize {
		return
	}

	entry := make(map[string]any, len(fields)+4)
	entry["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	entry["gid"] = goroutineID()
	entry["cat"] = category
	entry["evt"] = event
	for k, v := range fields {
		entry[k] = v
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	data = append(data, '\n')
	n, _ := logFile.Write(data)
	written += int64(n)
}

// goroutineID extracts the current goroutine ID from the runtime stack.
func goroutineID() int {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// Format: "goroutine 123 [..."
	var id int
	for i := len("goroutine "); i < n; i++ {
		c := buf[i]
		if c < '0' || c > '9' {
			break
		}
		id = id*10 + int(c-'0')
	}
	return id
}
