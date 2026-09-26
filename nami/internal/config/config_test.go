package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// useTempConfigDir points ConfigDir at a fresh directory for the test.
func useTempConfigDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("HOME", root)
	t.Setenv("AppData", root)
	return ConfigDir()
}

func requirePerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s has mode %o, want %o", path, got, want)
	}
}

// The config file holds OAuth tokens and MCP credentials, so no other local
// user may read it — including when it replaces a file an older version wrote
// world-readable.
func TestSaveKeepsCredentialsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply on Windows")
	}
	dir := useTempConfigDir(t)

	cfg := DefaultConfig()
	cfg.GitHubCopilot.GitHubToken = "gho_secret"
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	requirePerm(t, dir, 0o700)
	requirePerm(t, ConfigPath(), 0o600)

	if err := os.Chmod(ConfigPath(), 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save over an existing file: %v", err)
	}
	requirePerm(t, ConfigPath(), 0o600)

	leftovers, err := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("Save left temporary files behind: %v", leftovers)
	}
}

func TestSaveRoundTripsThroughLoad(t *testing.T) {
	useTempConfigDir(t)
	for _, name := range []string{"NAMI_PROVIDER", "NAMI_MODEL", "NAMI_API_KEY", "NAMI_BASE_URL"} {
		t.Setenv(name, "")
	}

	cfg := DefaultConfig()
	cfg.Model = "claude-opus-5"
	cfg.Codex.RefreshToken = "refresh-secret"
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded := Load()
	if loaded.Codex.RefreshToken != "refresh-secret" {
		t.Fatalf("RefreshToken = %q after reload", loaded.Codex.RefreshToken)
	}
	if loaded.Provider != "anthropic" || loaded.Model != "claude-opus-5" {
		t.Fatalf("model = %q/%q after reload", loaded.Provider, loaded.Model)
	}
}
