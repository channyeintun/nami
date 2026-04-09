package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Config holds all CLI configuration.
type Config struct {
	// Model selection: provider/model-name format
	Model string `json:"model,omitempty"`

	// Provider-level overrides
	BaseURL string `json:"base_url,omitempty"`
	APIKey  string `json:"-"` // never serialized

	// Session
	DefaultMode string `json:"default_mode,omitempty"` // "plan" or "fast"

	// Permissions
	PermissionMode string `json:"permission_mode,omitempty"` // "default", "bypassPermissions", "autoApprove"

	// Paths
	HooksDir string `json:"hooks_dir,omitempty"`
	SkillDir string `json:"skill_dir,omitempty"`
}

// DefaultConfig returns the configuration with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Model:       "anthropic/claude-sonnet-4-20250514",
		DefaultMode: "plan",
	}
}

// ConfigDir returns ~/.config/go-cli/.
func ConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "go-cli")
}

// ConfigPath returns the path to the config file.
func ConfigPath() string {
	return filepath.Join(ConfigDir(), "config.json")
}

// Load reads configuration from file and environment.
func Load() Config {
	cfg := DefaultConfig()

	// File config
	data, err := os.ReadFile(ConfigPath())
	if err == nil {
		json.Unmarshal(data, &cfg)
	}

	// Environment overrides
	if v := os.Getenv("GOCLI_MODEL"); v != "" {
		cfg.Model = v
	}
	if v := os.Getenv("GOCLI_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv("GOCLI_API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := os.Getenv("GOCLI_PERMISSION_MODE"); v != "" {
		cfg.PermissionMode = v
	}

	return cfg
}

// ParseModel splits "provider/model" into (provider, model).
// If no provider prefix, returns ("", modelStr).
func ParseModel(modelStr string) (provider, model string) {
	parts := strings.SplitN(modelStr, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", modelStr
}

// Save writes the config to disk.
func Save(cfg Config) error {
	if err := os.MkdirAll(ConfigDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ConfigPath(), data, 0o644)
}
