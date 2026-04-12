package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FileHistory tracks file modifications for undo/rewind support.
// Before each write or edit, the original file content is backed up.
type FileHistory struct {
	mu        sync.Mutex
	baseDir   string
	snapshots []FileSnapshot
	tracked   map[string]string // path -> last backup hash
}

// FileSnapshot records a point-in-time checkpoint of all tracked files.
type FileSnapshot struct {
	ID        string
	CreatedAt time.Time
	Files     []FileBackup
}

// FileBackup is a single file's content at a point in time.
type FileBackup struct {
	Path       string
	BackupPath string
	Existed    bool
	Hash       string
}

// FileRewindResult reports the outcome of restoring a snapshot.
type FileRewindResult struct {
	Restored int
	Failed   []string
}

const maxSnapshots = 100

// NewFileHistory creates a file history tracker using the given directory for backup storage.
func NewFileHistory(baseDir string) *FileHistory {
	return &FileHistory{
		baseDir: baseDir,
		tracked: make(map[string]string),
	}
}

// DefaultFileHistoryDir returns the default backup directory under the session dir.
func DefaultFileHistoryDir(sessionDir string) string {
	return filepath.Join(sessionDir, "file-history")
}

// TrackEdit records the current contents of a file before it is modified.
// Should be called before any write or edit operation.
func (h *FileHistory) TrackEdit(filePath string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist yet — track as non-existent
			h.tracked[absPath] = ""
			return nil
		}
		return fmt.Errorf("read file for tracking: %w", err)
	}

	hash := hashContent(data)
	if prev, ok := h.tracked[absPath]; ok && prev == hash {
		return nil // already tracked this version
	}

	backupDir := filepath.Join(h.baseDir, hash[:2])
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}

	backupPath := filepath.Join(backupDir, hash)
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		if err := os.WriteFile(backupPath, data, 0o644); err != nil {
			return fmt.Errorf("write backup: %w", err)
		}
	}

	h.tracked[absPath] = hash
	return nil
}

// MakeSnapshot creates a named checkpoint of all currently tracked files.
func (h *FileHistory) MakeSnapshot(label string) string {
	h.mu.Lock()
	defer h.mu.Unlock()

	snapshot := FileSnapshot{
		ID:        fmt.Sprintf("%s-%d", label, time.Now().UnixMilli()),
		CreatedAt: time.Now(),
	}

	for path, hash := range h.tracked {
		backup := FileBackup{
			Path:    path,
			Hash:    hash,
			Existed: hash != "",
		}
		if hash != "" {
			backup.BackupPath = filepath.Join(h.baseDir, hash[:2], hash)
		}
		snapshot.Files = append(snapshot.Files, backup)
	}

	h.snapshots = append(h.snapshots, snapshot)

	// Evict old snapshots
	if len(h.snapshots) > maxSnapshots {
		h.snapshots = h.snapshots[len(h.snapshots)-maxSnapshots:]
	}

	return snapshot.ID
}

// Rewind restores all files to the state captured in the given snapshot.
func (h *FileHistory) Rewind(snapshotID string) (FileRewindResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	var target *FileSnapshot
	for i := range h.snapshots {
		if h.snapshots[i].ID == snapshotID {
			target = &h.snapshots[i]
			break
		}
	}
	if target == nil {
		return FileRewindResult{}, fmt.Errorf("snapshot %q not found", snapshotID)
	}

	result := FileRewindResult{}
	for _, backup := range target.Files {
		if !backup.Existed {
			// File didn't exist at snapshot time — remove it
			if err := os.Remove(backup.Path); err != nil && !os.IsNotExist(err) {
				result.Failed = append(result.Failed, fmt.Sprintf("%s: remove file: %v", backup.Path, err))
				continue
			}
			result.Restored++
			continue
		}

		data, err := os.ReadFile(backup.BackupPath)
		if err != nil {
			result.Failed = append(result.Failed, fmt.Sprintf("%s: read backup: %v", backup.Path, err))
			continue
		}

		parentDir := filepath.Dir(backup.Path)
		if err := os.MkdirAll(parentDir, 0o755); err != nil {
			result.Failed = append(result.Failed, fmt.Sprintf("%s: create parent dir: %v", backup.Path, err))
			continue
		}

		if err := os.WriteFile(backup.Path, data, 0o644); err != nil {
			result.Failed = append(result.Failed, fmt.Sprintf("%s: write restored file: %v", backup.Path, err))
			continue
		}
		result.Restored++
	}

	return result, nil
}

// LatestSnapshotID returns the ID of the most recent snapshot, or empty string.
func (h *FileHistory) LatestSnapshotID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.snapshots) == 0 {
		return ""
	}
	return h.snapshots[len(h.snapshots)-1].ID
}

// SnapshotCount returns the number of tracked snapshots.
func (h *FileHistory) SnapshotCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.snapshots)
}

// TrackedFileCount returns the number of unique files being tracked.
func (h *FileHistory) TrackedFileCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.tracked)
}

// DiffStats returns simple insertion/deletion line counts between the snapshot and current state.
func (h *FileHistory) DiffStats(snapshotID string) (insertions, deletions int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	var target *FileSnapshot
	for i := range h.snapshots {
		if h.snapshots[i].ID == snapshotID {
			target = &h.snapshots[i]
			break
		}
	}
	if target == nil {
		return 0, 0
	}

	for _, backup := range target.Files {
		currentData, err := os.ReadFile(backup.Path)
		if err != nil {
			if os.IsNotExist(err) && backup.Existed {
				oldData, _ := os.ReadFile(backup.BackupPath)
				deletions += countLines(oldData)
			}
			continue
		}

		if !backup.Existed {
			insertions += countLines(currentData)
			continue
		}

		oldData, err := os.ReadFile(backup.BackupPath)
		if err != nil {
			continue
		}

		oldLines := strings.Count(string(oldData), "\n")
		newLines := strings.Count(string(currentData), "\n")
		if newLines > oldLines {
			insertions += newLines - oldLines
		} else {
			deletions += oldLines - newLines
		}
	}
	return
}

func hashContent(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func countLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	return strings.Count(string(data), "\n") + 1
}

// globalFileHistory is the package-level file history tracker, set during initialization.
var globalFileHistory struct {
	mu sync.RWMutex
	h  *FileHistory
}

// SetGlobalFileHistory installs the active file history tracker.
func SetGlobalFileHistory(h *FileHistory) {
	globalFileHistory.mu.Lock()
	defer globalFileHistory.mu.Unlock()
	globalFileHistory.h = h
}

// GetGlobalFileHistory returns the active file history tracker, or nil.
func GetGlobalFileHistory() *FileHistory {
	globalFileHistory.mu.RLock()
	defer globalFileHistory.mu.RUnlock()
	return globalFileHistory.h
}

// trackFileBeforeWrite records the current state of a file before modification.
func trackFileBeforeWrite(path string) {
	if h := GetGlobalFileHistory(); h != nil {
		_ = h.TrackEdit(path)
	}
}
