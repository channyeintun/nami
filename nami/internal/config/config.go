package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds all CLI configuration.
type Config struct {
	// Model selection: decoupled provider and model format
	Provider         string    `json:"provider,omitempty"`
	Model            string    `json:"model,omitempty"`
	SubagentProvider string    `json:"subagent_provider,omitempty"`
	SubagentModel    string    `json:"subagent_model,omitempty"`
	ReasoningEffort  string    `json:"reasoning_effort,omitempty"`
	MCP              MCPConfig `json:"mcp"`
	ModelSource      string    `json:"-"`

	// Provider-level overrides
	BaseURL   string                      `json:"base_url,omitempty"`
	APIKey    string                      `json:"-"` // never serialized
	Providers map[string]ProviderOverride `json:"providers,omitempty"`

	// Session
	DefaultMode             string  `json:"default_mode,omitempty"` // "plan" or "fast"
	CostWarningThresholdUSD float64 `json:"cost_warning_threshold_usd,omitempty"`
	EnableSessionMemory     bool    `json:"enable_session_memory,omitempty"`
	EnableMicrocompact      bool    `json:"enable_microcompact,omitempty"`

	// Permissions
	PermissionMode string `json:"permission_mode,omitempty"` // "default", "bypassPermissions", "autoApprove"
	AutoMode       bool   `json:"auto_mode,omitempty"`

	// Paths
	HooksDir string `json:"hooks_dir,omitempty"`
	SkillDir string `json:"skill_dir,omitempty"`

	// Provider auth
	GitHubCopilot GitHubCopilotAuth `json:"github_copilot"`
	Codex         CodexAuth         `json:"codex"`
}

type GitHubCopilotAuth struct {
	GitHubToken      string `json:"github_token,omitempty"`
	AccessToken      string `json:"access_token,omitempty"`
	ExpiresAtUnixMS  int64  `json:"expires_at_unix_ms,omitempty"`
	EnterpriseDomain string `json:"enterprise_domain,omitempty"`
}

type CodexAuth struct {
	AccessToken     string `json:"access_token,omitempty"`
	RefreshToken    string `json:"refresh_token,omitempty"`
	ExpiresAtUnixMS int64  `json:"expires_at_unix_ms,omitempty"`
	AccountID       string `json:"account_id,omitempty"`
}

type ProviderOverride struct {
	BaseURL      string `json:"base_url,omitempty"`
	APIKeyEnv    string `json:"api_key_env,omitempty"`
	DefaultModel string `json:"default_model,omitempty"`
}

// DefaultConfig returns the configuration with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Provider:                "anthropic",
		Model:                   "claude-sonnet-5",
		ModelSource:             "default",
		DefaultMode:             "fast",
		CostWarningThresholdUSD: 5,
		EnableSessionMemory:     true,
		EnableMicrocompact:      true,
	}
}

// ConfigPath returns the path to the config file.
func ConfigPath() string {
	return filepath.Join(ConfigDir(), "config.json")
}

// Load reads configuration from file and environment.
func Load() Config {
	cfg := loadUserConfig()
	applyEnvOverrides(&cfg)
	return cfg
}

// LoadForWorkingDir reads user configuration, merges the repo-local MCP
// override for the current workspace, and then applies environment overrides.
func LoadForWorkingDir(cwd string) Config {
	cfg := loadUserConfig()

	override, path, err := loadProjectMCPOverride(cwd)
	if err == nil {
		cfg.MCP = MergeMCPConfig(cfg.MCP, override)
	} else if !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "warning: failed to parse %s: %v\n", path, err)
	}

	applyEnvOverrides(&cfg)
	return cfg
}

func loadUserConfig() Config {
	cfg := DefaultConfig()

	// File config
	data, err := os.ReadFile(ConfigPath())
	if err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to parse %s: %v\n", ConfigPath(), err)
		} else {
			var probe struct {
				Model *string `json:"model"`
			}
			if err := json.Unmarshal(data, &probe); err == nil && probe.Model != nil {
				cfg.ModelSource = "config"
			}
		}
	}

	// Migrate backward-compatible format: provider/model strings
	if cfg.Provider == "" && cfg.Model != "" {
		if prov, mod := ParseModel(cfg.Model); prov != "" {
			cfg.Provider = prov
			cfg.Model = mod
		}
	}
	if cfg.SubagentProvider == "" && cfg.SubagentModel != "" {
		if prov, mod := ParseModel(cfg.SubagentModel); prov != "" {
			cfg.SubagentProvider = prov
			cfg.SubagentModel = mod
		}
	}
	migrateProviderOverrides(&cfg)

	return cfg
}

func migrateProviderOverrides(cfg *Config) {
	if cfg == nil || strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.Provider) == "" {
		return
	}
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if cfg.Providers == nil {
		cfg.Providers = make(map[string]ProviderOverride)
	}
	override := cfg.Providers[provider]
	if strings.TrimSpace(override.BaseURL) == "" {
		override.BaseURL = strings.TrimSpace(cfg.BaseURL)
		cfg.Providers[provider] = override
	}
}

func applyEnvOverrides(cfg *Config) {
	if cfg == nil {
		return
	}

	// Environment overrides
	if v := os.Getenv("NAMI_PROVIDER"); v != "" {
		cfg.Provider = v
	}
	if v := os.Getenv("NAMI_MODEL"); v != "" {
		if prov, mod := ParseModel(v); prov != "" {
			cfg.Provider = prov
			cfg.Model = mod
		} else {
			cfg.Model = v
		}
		cfg.ModelSource = "env"
	}
	if v := os.Getenv("NAMI_SUBAGENT_PROVIDER"); v != "" {
		cfg.SubagentProvider = v
	}
	if v := os.Getenv("NAMI_SUBAGENT_MODEL"); v != "" {
		if prov, mod := ParseModel(v); prov != "" {
			cfg.SubagentProvider = prov
			cfg.SubagentModel = mod
		} else {
			cfg.SubagentModel = v
		}
	}
	if v := os.Getenv("NAMI_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv("NAMI_API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := os.Getenv("NAMI_REASONING_EFFORT"); v != "" {
		cfg.ReasoningEffort = v
	}
	if v := os.Getenv("NAMI_PERMISSION_MODE"); v != "" {
		cfg.PermissionMode = v
	}
	if v := os.Getenv("NAMI_AUTO_MODE"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.AutoMode = parsed
		}
	}
	if cfg.PermissionMode != "" {
		switch cfg.PermissionMode {
		case "default", "bypassPermissions", "autoApprove":
			// valid
		default:
			fmt.Fprintf(os.Stderr,
				"warning: unknown NAMI_PERMISSION_MODE %q — falling back to \"default\"; valid values are: default, bypassPermissions, autoApprove\n",
				cfg.PermissionMode)
			cfg.PermissionMode = "default"
		}
	}
	if v := os.Getenv("NAMI_COST_WARNING_THRESHOLD_USD"); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.CostWarningThresholdUSD = parsed
		} else {
			fmt.Fprintf(os.Stderr, "warning: invalid NAMI_COST_WARNING_THRESHOLD_USD %q: %v\n", v, err)
		}
	}
	if v := os.Getenv("NAMI_ENABLE_SESSION_MEMORY"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.EnableSessionMemory = parsed
		} else {
			fmt.Fprintf(os.Stderr, "warning: invalid NAMI_ENABLE_SESSION_MEMORY %q: %v\n", v, err)
		}
	}
	if v := os.Getenv("NAMI_ENABLE_MICROCOMPACT"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.EnableMicrocompact = parsed
		} else {
			fmt.Fprintf(os.Stderr, "warning: invalid NAMI_ENABLE_MICROCOMPACT %q: %v\n", v, err)
		}
	}
}

func (cfg Config) ApplyProviderOverride(providerID string) Config {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	if providerID == "" || cfg.Providers == nil {
		return cfg
	}
	override := cfg.Providers[providerID]
	if strings.TrimSpace(override.BaseURL) != "" {
		cfg.BaseURL = strings.TrimSpace(override.BaseURL)
	}
	if strings.TrimSpace(override.APIKeyEnv) != "" && strings.TrimSpace(cfg.APIKey) == "" {
		cfg.APIKey = strings.TrimSpace(os.Getenv(strings.TrimSpace(override.APIKeyEnv)))
	}
	if strings.TrimSpace(override.DefaultModel) != "" && strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = strings.TrimSpace(override.DefaultModel)
	}
	return cfg
}

func (cfg Config) ProviderAPIKeyEnv(providerID string, fallback string) string {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	if providerID != "" && cfg.Providers != nil {
		if override := strings.TrimSpace(cfg.Providers[providerID].APIKeyEnv); override != "" {
			return override
		}
	}
	return fallback
}

func (cfg Config) ProviderDefaultModel(providerID string, fallback string) string {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	if providerID != "" && cfg.Providers != nil {
		if override := strings.TrimSpace(cfg.Providers[providerID].DefaultModel); override != "" {
			return override
		}
	}
	return fallback
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
