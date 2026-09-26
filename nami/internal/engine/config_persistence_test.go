package engine

import (
	"io"
	"strings"
	"testing"

	"github.com/channyeintun/nami/internal/config"
	"github.com/channyeintun/nami/internal/ipc"
)

// A NAMI_* variable overrides the config for one process. A command that saves
// the config — here /logout — must not write the override back, or a single
// NAMI_PERMISSION_MODE=bypassPermissions run would disable approvals for every
// later session.
func TestSlashCommandSaveDoesNotPersistEnvironmentOverrides(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("HOME", root)
	t.Setenv("AppData", root)

	saved := config.LoadUser()
	saved.GitHubCopilot.GitHubToken = "gho_stored"
	if err := config.Save(saved); err != nil {
		t.Fatalf("Save: %v", err)
	}

	t.Setenv("NAMI_PERMISSION_MODE", "bypassPermissions")
	t.Setenv("NAMI_BASE_URL", "http://127.0.0.1:9999")
	cmd := &slashCommandContext{
		args:   "copilot",
		bridge: ipc.NewBridge(strings.NewReader(""), io.Discard),
	}
	if err := handleLogoutSlashCommand(cmd); err != nil {
		t.Fatalf("logout: %v", err)
	}

	t.Setenv("NAMI_PERMISSION_MODE", "")
	t.Setenv("NAMI_BASE_URL", "")
	reloaded := config.Load()
	if reloaded.GitHubCopilot.GitHubToken != "" {
		t.Fatal("logout did not clear the stored token")
	}
	if reloaded.PermissionMode != "" || reloaded.BaseURL != "" {
		t.Fatalf("environment overrides were saved: permission %q, base URL %q", reloaded.PermissionMode, reloaded.BaseURL)
	}
}
