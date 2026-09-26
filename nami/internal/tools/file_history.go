package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FileHistory tracks file modifications for undo/rewind support.
//
// A snapshot records the state files were in when it was taken. Files the
// session had already modified are backed up when the snapshot is made. A file
// the session first modifies later is backed up just before that write: the
// session has not written it since the snapshot, so its content is still what
// it was then.
type FileHistory struct {
	mu        sync.Mutex
	baseDir   string
	snapshots []FileSnapshot
	tracked   map[string]struct{} // absolute paths the session has modified
}

// FileSnapshot records the state of files at a point in time.
type FileSnapshot struct {
	ID        string
	CreatedAt time.Time
	Files     map[string]FileBackup // keyed by absolute path
}

// FileBackup is a single file's content at a point in time.
type FileBackup struct {
	Path       string
	BackupPath string
	Existed    bool
	Hash       string
	Mode       fs.FileMode
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
		tracked: make(map[string]struct{}),
	}
}

// DefaultFileHistoryDir returns the default backup directory under the session dir.
func DefaultFileHistoryDir(sessionDir string) string {
	return filepath.Join(sessionDir, "file-history")
}

// TrackEdit must be called before a file is modified. It marks the file as
// modified by the session and backs it up into every snapshot that does not
// cover it yet.
func (h *FileHistory) TrackEdit(filePath string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return err
	}
	h.tracked[absPath] = struct{}{}

	var backup *FileBackup
	for i := range h.snapshots {
		if _, covered := h.snapshots[i].Files[absPath]; covered {
			continue
		}
		if backup == nil {
			current, err := h.backupFile(absPath)
			if err != nil {
				return err
			}
			backup = &current
		}
		h.snapshots[i].Files[absPath] = *backup
	}
	return nil
}

// MakeSnapshot records the current state of every file the session has
// modified and returns the snapshot's id.
func (h *FileHistory) MakeSnapshot(label string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	snapshot := FileSnapshot{
		ID:        fmt.Sprintf("%s-%d", label, time.Now().UnixMilli()),
		CreatedAt: time.Now(),
		Files:     make(map[string]FileBackup, len(h.tracked)),
	}
	for path := range h.tracked {
		backup, err := h.backupFile(path)
		if err != nil {
			return "", err
		}
		snapshot.Files[path] = backup
	}

	h.snapshots = append(h.snapshots, snapshot)

	// Evict old snapshots
	if len(h.snapshots) > maxSnapshots {
		h.snapshots = h.snapshots[len(h.snapshots)-maxSnapshots:]
	}

	return snapshot.ID, nil
}

// backupFile copies the current content of path into the content-addressed
// backup store and describes it. A missing file is recorded as not existing.
// Backups can hold secrets, so only the owner may read them.
func (h *FileHistory) backupFile(path string) (FileBackup, error) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return FileBackup{Path: path}, nil
	}
	if err != nil {
		return FileBackup{}, fmt.Errorf("open %s for backup: %w", path, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return FileBackup{}, fmt.Errorf("stat %s for backup: %w", path, err)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return FileBackup{}, fmt.Errorf("read %s for backup: %w", path, err)
	}

	hash := hashContent(data)
	backupPath := filepath.Join(h.baseDir, hash[:2], hash)
	if _, err := os.Stat(backupPath); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(backupPath), 0o700); err != nil {
			return FileBackup{}, fmt.Errorf("create backup dir: %w", err)
		}
		if err := os.WriteFile(backupPath, data, 0o600); err != nil {
			return FileBackup{}, fmt.Errorf("write backup: %w", err)
		}
	} else if err != nil {
		return FileBackup{}, fmt.Errorf("stat backup: %w", err)
	}

	return FileBackup{
		Path:       path,
		BackupPath: backupPath,
		Existed:    true,
		Hash:       hash,
		Mode:       info.Mode().Perm(),
	}, nil
}

// findSnapshot returns the snapshot with the given id, or nil. h.mu must be held.
func (h *FileHistory) findSnapshot(snapshotID string) *FileSnapshot {
	for i := range h.snapshots {
		if h.snapshots[i].ID == snapshotID {
			return &h.snapshots[i]
		}
	}
	return nil
}

// Rewind restores all files to the state captured in the given snapshot.
func (h *FileHistory) Rewind(snapshotID string) (FileRewindResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	target := h.findSnapshot(snapshotID)
	if target == nil {
		return FileRewindResult{}, fmt.Errorf("snapshot %q not found", snapshotID)
	}

	result := FileRewindResult{}
	for _, path := range sortedKeys(target.Files) {
		if err := restoreFileBackup(target.Files[path]); err != nil {
			result.Failed = append(result.Failed, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		invalidateFileReadState(path)
		result.Restored++
	}

	return result, nil
}

// restoreFileBackup puts a file back into its backed-up state: the recorded
// content, or no file at all when it did not exist.
func restoreFileBackup(backup FileBackup) error {
	if !backup.Existed {
		if err := os.Remove(backup.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove file: %w", err)
		}
		return nil
	}

	data, err := os.ReadFile(backup.BackupPath)
	if err != nil {
		return fmt.Errorf("read backup: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(backup.Path), 0o755); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}
	// The mode only applies when the file has to be recreated; an existing
	// file keeps its own.
	if err := os.WriteFile(backup.Path, data, backup.Mode); err != nil {
		return fmt.Errorf("write restored file: %w", err)
	}
	return nil
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

	target := h.findSnapshot(snapshotID)
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
