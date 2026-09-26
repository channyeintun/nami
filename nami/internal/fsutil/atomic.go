// Package fsutil holds small file helpers shared by the stores that keep
// session data on disk.
package fsutil

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic replaces path with data. It writes a uniquely named
// temporary file in the same directory and renames it into place, so a reader
// never sees a half-written file, and two writers never share a temporary
// file. The file gets perm whether or not it existed before.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Removing it after a successful rename fails harmlessly: the name is gone.
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
