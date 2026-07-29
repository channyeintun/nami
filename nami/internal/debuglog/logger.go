package debuglog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	SchemaVersion = 1
	DefaultPath   = "debug.log"
)

// Enabled is preserved for compatibility with existing call sites.
// New code should prefer IsEnabled.
var Enabled bool

const maxLogSize = 50 * 1024 * 1024 // 50 MB

type EventError struct {
	Message string `json:"message"`
	Kind    string `json:"kind,omitempty"`
}

type Envelope struct {
	SchemaVersion int            `json:"schema_version"`
	TS            string         `json:"ts"`
	SessionID     string         `json:"session_id,omitempty"`
	Source        string         `json:"source"`
	Component     string         `json:"component"`
	Category      string         `json:"category,omitempty"`
	Event         string         `json:"event"`
	Level         string         `json:"level"`
	Seq           uint64         `json:"seq"`
	GID           int            `json:"gid,omitempty"`
	TraceID       string         `json:"trace_id,omitempty"`
	SpanID        string         `json:"span_id,omitempty"`
	Metrics       map[string]any `json:"metrics,omitempty"`
	Data          map[string]any `json:"data,omitempty"`
	Error         *EventError    `json:"error,omitempty"`
}

type Status struct {
	Enabled    bool
	SessionID  string
	SessionDir string
	Path       string
	Written    int64
	Seq        uint64
}

var (
	mu         sync.Mutex
	logFile    *os.File
	written    int64
	seq        uint64
	sessionID  string
	sessionDir string
	logPath    string
)

func ConfigureSession(id string, dir string) error {
	mu.Lock()
	defer mu.Unlock()

	wasEnabled := Enabled
	if sessionID == id && sessionDir == dir {
		return nil
	}

	if err := closeLocked(); err != nil {
		return err
	}
	sessionID = strings.TrimSpace(id)
	sessionDir = strings.TrimSpace(dir)
	logPath = ""
	written = 0
	seq = 0
	Enabled = false

	if wasEnabled {
		if err := openLocked(); err != nil {
			fmt.Fprintf(os.Stderr, "debuglog: open %s: %v\n", filepath.Join(sessionDir, DefaultPath), err)
			return err
		}
	}
	return nil
}

func Enable() (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if Enabled && logFile != nil {
		return logPath, nil
	}
	if err := openLocked(); err != nil {
		return "", err
	}
	return logPath, nil
}

func Disable() error {
	mu.Lock()
	defer mu.Unlock()
	Enabled = false
	return closeLocked()
}

func IsEnabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return Enabled && logFile != nil
}

func CurrentPath() string {
	mu.Lock()
	defer mu.Unlock()
	return logPath
}

func CurrentStatus() Status {
	mu.Lock()
	defer mu.Unlock()
	return Status{
		Enabled:    Enabled && logFile != nil,
		SessionID:  sessionID,
		SessionDir: sessionDir,
		Path:       logPath,
		Written:    written,
		Seq:        seq,
	}
}

// Close flushes and closes the log file.
func Close() {
	mu.Lock()
	defer mu.Unlock()
	_ = closeLocked()
	Enabled = false
}

func openLocked() error {
	if strings.TrimSpace(sessionDir) == "" {
		return errors.New("session directory is not configured")
	}
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		Enabled = false
		return err
	}
	path := filepath.Join(sessionDir, DefaultPath)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		Enabled = false
		return err
	}
	info, statErr := f.Stat()
	if statErr != nil {
		_ = f.Close()
		Enabled = false
		return statErr
	}
	logFile = f
	logPath = path
	written = info.Size()
	Enabled = true
	fmt.Fprintf(os.Stderr, "debuglog: logging to %s\n", path)
	return nil
}

func closeLocked() error {
	if logFile == nil {
		return nil
	}
	err := logFile.Close()
	logFile = nil
	return err
}

func Emit(entry Envelope) {
	mu.Lock()
	defer mu.Unlock()
	if !Enabled || logFile == nil || written >= maxLogSize {
		return
	}

	seq++
	entry.SchemaVersion = SchemaVersion
	entry.TS = time.Now().UTC().Format(time.RFC3339Nano)
	entry.SessionID = sessionID
	entry.Seq = seq
	entry.GID = goroutineID()
	if strings.TrimSpace(entry.Source) == "" {
		entry.Source = "engine"
	}
	if strings.TrimSpace(entry.Component) == "" {
		entry.Component = "debug"
	}
	if strings.TrimSpace(entry.Category) == "" {
		entry.Category = entry.Component
	}
	if strings.TrimSpace(entry.Level) == "" {
		entry.Level = "debug"
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	data = append(data, '\n')
	n, _ := logFile.Write(data)
	written += int64(n)
}

// Log appends one structured JSONL line.
func Log(category, event string, fields map[string]any) {
	data, metrics, eventErr := normalizeFields(fields)
	Emit(Envelope{
		Source:    "engine",
		Component: strings.TrimSpace(category),
		Category:  strings.TrimSpace(category),
		Event:     strings.TrimSpace(event),
		Level:     logLevelForError(eventErr),
		Metrics:   metrics,
		Data:      data,
		Error:     eventErr,
	})
}

func normalizeFields(fields map[string]any) (map[string]any, map[string]any, *EventError) {
	if len(fields) == 0 {
		return nil, nil, nil
	}

	data := make(map[string]any, len(fields))
	metrics := make(map[string]any)
	var eventErr *EventError

	// error and error_kind both land on the same struct. Assigning fields
	// rather than replacing it keeps whichever key the map yields second from
	// discarding the other.
	errorField := func(assign func(*EventError, string)) func(any) {
		return func(value any) {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text == "" {
				return
			}
			if eventErr == nil {
				eventErr = &EventError{}
			}
			assign(eventErr, text)
		}
	}
	setMessage := errorField(func(e *EventError, text string) { e.Message = text })
	setKind := errorField(func(e *EventError, text string) { e.Kind = text })

	for key, value := range fields {
		switch key {
		case "error":
			setMessage(value)
		case "error_kind":
			setKind(value)
		case "bytes", "duration_ms", "duration_ns", "elapsed_ms":
			metrics[key] = value
		default:
			data[key] = value
		}
	}

	if len(data) == 0 {
		data = nil
	}
	if len(metrics) == 0 {
		metrics = nil
	}
	if eventErr != nil && strings.TrimSpace(eventErr.Message) == "" && strings.TrimSpace(eventErr.Kind) == "" {
		eventErr = nil
	}
	return data, metrics, eventErr
}

func logLevelForError(eventErr *EventError) string {
	if eventErr != nil && strings.TrimSpace(eventErr.Message) != "" {
		return "error"
	}
	return "debug"
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
