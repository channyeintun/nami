package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds all CLI configuration.
type Config struct {
	// Model selection: provider/model-name format
	Model           string `json:"model,omitempty"`
	SubagentModel   string `json:"subagent_model,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`

	// Provider-level overrides
	BaseURL string `json:"base_url,omitempty"`
	APIKey  string `json:"-"` // never serialized

	// Session
	DefaultMode             string  `json:"default_mode,omitempty"` // "plan" or "fast"
	CostWarningThresholdUSD float64 `json:"cost_warning_threshold_usd,omitempty"`

	// Permissions
	PermissionMode string `json:"permission_mode,omitempty"` // "default", "bypassPermissions", "autoApprove"

	// Paths
	HooksDir string `json:"hooks_dir,omitempty"`
	SkillDir string `json:"skill_dir,omitempty"`

	// Provider auth
	GitHubCopilot GitHubCopilotAuth `json:"github_copilot,omitempty"`
}

type GitHubCopilotAuth struct {
	GitHubToken      string `json:"github_token,omitempty"`
	AccessToken      string `json:"access_token,omitempty"`
	ExpiresAtUnixMS  int64  `json:"expires_at_unix_ms,omitempty"`
	EnterpriseDomain string `json:"enterprise_domain,omitempty"`
}

// DefaultConfig returns the configuration with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Model:                   "anthropic/claude-sonnet-4-20250514",
		DefaultMode:             "plan",
		CostWarningThresholdUSD: 5,
	}
}

// ConfigDir returns ~/.config/gocode/.
func ConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "gocode")
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
		if err := json.Unmarshal(data, &cfg); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to parse %s: %v\n", ConfigPath(), err)
		}
	}

	// Environment overrides
	if v := os.Getenv("GOCODE_MODEL"); v != "" {
		cfg.Model = v
	}
	if v := os.Getenv("GOCODE_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv("GOCODE_API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := os.Getenv("GOCODE_REASONING_EFFORT"); v != "" {
		cfg.ReasoningEffort = v
	}
	if v := os.Getenv("GOCODE_PERMISSION_MODE"); v != "" {
		cfg.PermissionMode = v
	}
	if cfg.PermissionMode != "" {
		switch cfg.PermissionMode {
		case "default", "bypassPermissions", "autoApprove":
			// valid
		default:
			fmt.Fprintf(os.Stderr,
				"warning: unknown GOCODE_PERMISSION_MODE %q — falling back to \"default\"; valid values are: default, bypassPermissions, autoApprove\n",
				cfg.PermissionMode)
			cfg.PermissionMode = "default"
		}
	}
	if v := os.Getenv("GOCODE_COST_WARNING_THRESHOLD_USD"); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.CostWarningThresholdUSD = parsed
		} else {
			fmt.Fprintf(os.Stderr, "warning: invalid GOCODE_COST_WARNING_THRESHOLD_USD %q: %v\n", v, err)
		}
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
