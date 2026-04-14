package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	attemptLogFilename        = "attempt_log.ndjson"
	maxAttemptLogEntries      = 20
	maxAttemptLogPromptTokens = 600
)

// AttemptEntry records one failed or blocked tool attempt for the current session.
type AttemptEntry struct {
	Command        string    `json:"command,omitempty"`
	ErrorSignature string    `json:"error_signature,omitempty"`
	BlockedPath    string    `json:"blocked_path,omitempty"`
	DoNotRetry     bool      `json:"do_not_retry,omitempty"`
	RecordedAt     time.Time `json:"recorded_at"`
}

// AttemptLog persists session-scoped failed attempt records.
// The log expires when the session directory is deleted.
type AttemptLog struct {
	sessionDir string
}

// NewAttemptLog creates an AttemptLog rooted at the given session directory.
// The directory need not exist yet; it is created on first write.
func NewAttemptLog(sessionDir string) *AttemptLog {
	return &AttemptLog{sessionDir: sessionDir}
}

// Record appends one entry to the attempt log.
func (l *AttemptLog) Record(entry AttemptEntry) error {
	if l == nil || l.sessionDir == "" {
		return nil
	}
	if entry.RecordedAt.IsZero() {
		entry.RecordedAt = time.Now()
	}
	if err := os.MkdirAll(l.sessionDir, 0o755); err != nil {
		return fmt.Errorf("attempt log: create session dir: %w", err)
	}
	path := filepath.Join(l.sessionDir, attemptLogFilename)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("attempt log: open: %w", err)
	}
	defer f.Close()
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("attempt log: marshal: %w", err)
	}
	_, err = fmt.Fprintf(f, "%s\n", data)
	return err
}

// Load reads all entries from the attempt log for the current session.
// Returns nil, nil when no log file exists.
func (l *AttemptLog) Load() ([]AttemptEntry, error) {
	if l == nil || l.sessionDir == "" {
		return nil, nil
	}
	path := filepath.Join(l.sessionDir, attemptLogFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("attempt log: read: %w", err)
	}

	var entries []AttemptEntry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry AttemptEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue // skip malformed lines
		}
		entries = append(entries, entry)
	}

	// Keep only the most recent entries.
	if len(entries) > maxAttemptLogEntries {
		entries = entries[len(entries)-maxAttemptLogEntries:]
	}
	return entries, nil
}

// FormatPromptSection renders the attempt log into a compact system prompt section
// within the given byte budget. Returns an empty string when there are no entries.
func FormatAttemptLogSection(entries []AttemptEntry) string {
	if len(entries) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<session_attempt_log>\n")
	b.WriteString("The following commands or paths failed earlier in this session. ")
	b.WriteString("Do not retry them unless the underlying cause has changed.\n")

	for _, entry := range entries {
		line := formatAttemptEntry(entry)
		if line == "" {
			continue
		}
		if b.Len()+len(line)+1 > maxAttemptLogPromptTokens*4 {
			b.WriteString("[additional failed attempts omitted]\n")
			break
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("</session_attempt_log>")
	result := strings.TrimSpace(b.String())
	if result == "<session_attempt_log>\n</session_attempt_log>" {
		return ""
	}
	return result
}

func formatAttemptEntry(entry AttemptEntry) string {
	parts := make([]string, 0, 3)
	if entry.Command != "" {
		parts = append(parts, "command: "+entry.Command)
	}
	if entry.ErrorSignature != "" {
		parts = append(parts, "error: "+entry.ErrorSignature)
	}
	if entry.BlockedPath != "" {
		parts = append(parts, "path: "+entry.BlockedPath)
	}
	if len(parts) == 0 {
		return ""
	}
	prefix := "- "
	if entry.DoNotRetry {
		prefix = "- [do-not-retry] "
	}
	return prefix + strings.Join(parts, "; ")
}
