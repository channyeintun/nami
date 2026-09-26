package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func resolveToolPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	baseDir, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	resolved := filepath.Join(baseDir, path)
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}

	escapes, err := pathEscapes(baseDir, resolved)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}
	if escapes {
		return "", fmt.Errorf("relative path %q escapes working directory %q", path, baseDir)
	}

	// A symlink inside the working directory must not lead outside it. Both
	// sides are compared fully resolved, because the working directory itself
	// may be reached through a link. A path that does not exist yet
	// (create_file) is checked through its nearest existing ancestor, which
	// can be a link to a directory elsewhere.
	realBase, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		return "", fmt.Errorf("resolve working directory %q: %w", baseDir, err)
	}
	real, err := resolveExistingPrefix(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve symlink for path %q: %w", path, err)
	}
	escapes, err = pathEscapes(realBase, real)
	if err != nil {
		return "", fmt.Errorf("resolve symlink for path %q: %w", path, err)
	}
	if escapes {
		return "", fmt.Errorf("path %q resolves via symlink to %q which escapes working directory %q", path, real, baseDir)
	}

	return resolved, nil
}

// pathEscapes reports whether target lies outside base. Both paths must be
// absolute.
func pathEscapes(base, target string) (bool, error) {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false, err
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}

// resolveExistingPrefix evaluates the symlinks in the longest prefix of path
// that exists and appends the remaining, not yet existing, components as they
// are.
func resolveExistingPrefix(path string) (string, error) {
	existing := path
	for {
		real, err := filepath.EvalSymlinks(existing)
		if err == nil {
			rest, err := filepath.Rel(existing, path)
			if err != nil {
				return "", err
			}
			return filepath.Join(real, rest), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", err
		}
		existing = parent
	}
}

// writeNewFile creates path with content and fails with fs.ErrExist if
// anything is already there. O_EXCL makes the check and the creation one
// step: a file that appeared after validation is never overwritten, and a
// dangling symlink is refused instead of followed to wherever it points.
func writeNewFile(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		// Leave no half-written file behind.
		return errors.Join(err, file.Close(), os.Remove(path))
	}
	return file.Close()
}
