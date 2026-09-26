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

// Settings that default to on must stay off once the user turns them off, even
// after something unrelated — a token refresh, a /reasoning change — saves.
func TestSavePreservesSettingsTurnedOffInTheFile(t *testing.T) {
	useTempConfigDir(t)
	for _, name := range []string{"NAMI_ENABLE_SESSION_MEMORY", "NAMI_ENABLE_MICROCOMPACT", "NAMI_COST_WARNING_THRESHOLD_USD"} {
		t.Setenv(name, "")
	}
	if err := os.MkdirAll(ConfigDir(), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	written := `{"enable_session_memory": false, "enable_microcompact": false, "cost_warning_threshold_usd": 0}`
	if err := os.WriteFile(ConfigPath(), []byte(written), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg := Load()
	cfg.ReasoningEffort = "high"
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded := Load()
	if reloaded.EnableSessionMemory || reloaded.EnableMicrocompact {
		t.Fatalf("disabled settings came back on: session memory %t, microcompact %t", reloaded.EnableSessionMemory, reloaded.EnableMicrocompact)
	}
	if reloaded.CostWarningThresholdUSD != 0 {
		t.Fatalf("CostWarningThresholdUSD = %v, want the saved 0", reloaded.CostWarningThresholdUSD)
	}
}

// Environment variables override the config for one process. Saving what
// LoadUser returns must not turn them into the saved setting.
func TestLoadUserIgnoresEnvironmentOverrides(t *testing.T) {
	useTempConfigDir(t)
	t.Setenv("NAMI_PERMISSION_MODE", "bypassPermissions")
	t.Setenv("NAMI_BASE_URL", "http://127.0.0.1:9999")

	if got := Load().PermissionMode; got != "bypassPermissions" {
		t.Fatalf("Load().PermissionMode = %q, want the environment override", got)
	}
	cfg := LoadUser()
	if cfg.PermissionMode != "" || cfg.BaseURL != "" {
		t.Fatalf("LoadUser applied environment overrides: %+v", cfg)
	}

	cfg.ReasoningEffort = "high"
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Setenv("NAMI_PERMISSION_MODE", "")
	t.Setenv("NAMI_BASE_URL", "")
	reloaded := Load()
	if reloaded.PermissionMode != "" || reloaded.BaseURL != "" {
		t.Fatalf("environment overrides were persisted: permission %q, base URL %q", reloaded.PermissionMode, reloaded.BaseURL)
	}
	if reloaded.ReasoningEffort != "high" {
		t.Fatalf("ReasoningEffort = %q, want the saved change", reloaded.ReasoningEffort)
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

// A config.json that no longer parses loads as defaults. Saving over it would
// then silently replace the user's stored tokens and MCP servers with those
// defaults, so Save refuses and leaves the file for the user to repair.
func TestSaveRefusesToOverwriteUnparseableConfig(t *testing.T) {
	useTempConfigDir(t)
	if err := os.MkdirAll(ConfigDir(), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	damaged := []byte(`{"github_copilot": {"github_token": "gho_keep_me"`)
	if err := os.WriteFile(ConfigPath(), damaged, 0o600); err != nil {
		t.Fatalf("write damaged config: %v", err)
	}

	if err := Save(DefaultConfig()); err == nil {
		t.Fatal("Save overwrote a config file that does not parse")
	}
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(data) != string(damaged) {
		t.Fatalf("damaged config was modified: %q", data)
	}

	// An empty file holds nothing to lose.
	if err := os.WriteFile(ConfigPath(), []byte("  \n"), 0o600); err != nil {
		t.Fatalf("write empty config: %v", err)
	}
	if err := Save(DefaultConfig()); err != nil {
		t.Fatalf("Save over an empty config: %v", err)
	}
}
