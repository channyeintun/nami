package tools

import (
	"fmt"
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

	rel, err := filepath.Rel(baseDir, resolved)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("relative path %q escapes working directory %q", path, baseDir)
	}

	// Resolve symlinks on the resolved path to prevent a symlink inside the
	// working directory from pointing outside it and bypassing the check above.
	// EvalSymlinks fails if the path does not exist yet (e.g. create_file), so
	// we only apply the extra check when the path already exists on disk.
	if real, err := filepath.EvalSymlinks(resolved); err == nil {
		realRel, err := filepath.Rel(baseDir, real)
		if err != nil {
			return "", fmt.Errorf("resolve symlink for path %q: %w", path, err)
		}
		if realRel == ".." || strings.HasPrefix(realRel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("path %q resolves via symlink to %q which escapes working directory %q", path, real, baseDir)
		}
	}

	return resolved, nil
}
