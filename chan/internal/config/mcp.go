package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const projectMCPRelativePath = ".chan/mcp.json"

// MCPConfig describes configured external MCP servers.
type MCPConfig struct {
	Servers map[string]MCPServerConfig `json:"servers,omitempty"`
}

// MCPPermission is the config-level permission label for a trusted MCP tool.
type MCPPermission string

const (
	MCPPermissionRead    MCPPermission = "read"
	MCPPermissionWrite   MCPPermission = "write"
	MCPPermissionExecute MCPPermission = "execute"
)

// MCPServerConfig is the persisted configuration shape for one MCP server.
// Transport-specific validation happens during runtime resolution.
type MCPServerConfig struct {
	Transport *string `json:"transport,omitempty"`

	Enabled         *bool                    `json:"enabled,omitempty"`
	Trust           *bool                    `json:"trust,omitempty"`
	ExcludeTools    []string                 `json:"exclude_tools,omitempty"`
	IncludeTools    []string                 `json:"include_tools,omitempty"`
	ToolPermissions map[string]MCPPermission `json:"tool_permissions,omitempty"`

	Command          *string           `json:"command,omitempty"`
	Args             []string          `json:"args,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	StartupTimeoutMS *int              `json:"startup_timeout_ms,omitempty"`

	URL     *string           `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// LoadProjectMCPOverridePath returns the canonical repo-local MCP override path.
func LoadProjectMCPOverridePath(cwd string) string {
	projectRoot := FindProjectRoot(cwd)
	if strings.TrimSpace(projectRoot) == "" {
		return ""
	}
	return filepath.Join(projectRoot, projectMCPRelativePath)
}

// FindProjectRoot returns the nearest parent directory containing .git.
func FindProjectRoot(start string) string {
	for _, dir := range walkUpDirs(start) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
	}
	return ""
}

// MergeMCPConfig overlays project-local MCP config on top of user config.
func MergeMCPConfig(base, override MCPConfig) MCPConfig {
	merged := base.Clone()
	if len(override.Servers) == 0 {
		return merged
	}
	if merged.Servers == nil {
		merged.Servers = make(map[string]MCPServerConfig, len(override.Servers))
	}
	for name, server := range override.Servers {
		existing, ok := merged.Servers[name]
		if ok {
			merged.Servers[name] = mergeMCPServerConfig(existing, server)
			continue
		}
		merged.Servers[name] = cloneMCPServerConfig(server)
	}
	return merged
}

// Clone returns a deep copy safe for later mutation.
func (cfg MCPConfig) Clone() MCPConfig {
	if len(cfg.Servers) == 0 {
		return MCPConfig{}
	}
	servers := make(map[string]MCPServerConfig, len(cfg.Servers))
	for name, server := range cfg.Servers {
		servers[name] = cloneMCPServerConfig(server)
	}
	return MCPConfig{Servers: servers}
}

// NormalizeMCPPermission validates and canonicalizes a configured permission label.
func NormalizeMCPPermission(raw string) (MCPPermission, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(MCPPermissionRead), "readonly", "read_only":
		return MCPPermissionRead, true
	case string(MCPPermissionWrite):
		return MCPPermissionWrite, true
	case string(MCPPermissionExecute), "exec":
		return MCPPermissionExecute, true
	default:
		return "", false
	}
}

func loadProjectMCPOverride(cwd string) (MCPConfig, string, error) {
	path := LoadProjectMCPOverridePath(cwd)
	if strings.TrimSpace(path) == "" {
		return MCPConfig{}, path, os.ErrNotExist
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return MCPConfig{}, path, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return MCPConfig{}, path, os.ErrNotExist
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return MCPConfig{}, path, fmt.Errorf("unmarshal json: %w", err)
	}

	if section, ok := raw["mcp"]; ok {
		var cfg MCPConfig
		if err := json.Unmarshal(section, &cfg); err != nil {
			return MCPConfig{}, path, fmt.Errorf("unmarshal mcp section: %w", err)
		}
		return cfg, path, nil
	}

	var cfg MCPConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return MCPConfig{}, path, fmt.Errorf("unmarshal project mcp config: %w", err)
	}
	return cfg, path, nil
}

func mergeMCPServerConfig(base, override MCPServerConfig) MCPServerConfig {
	merged := cloneMCPServerConfig(base)

	if override.Transport != nil {
		merged.Transport = stringPtr(*override.Transport)
	}
	if override.Enabled != nil {
		merged.Enabled = boolPtr(*override.Enabled)
	}
	if override.Trust != nil {
		merged.Trust = boolPtr(*override.Trust)
	}
	if override.ExcludeTools != nil {
		merged.ExcludeTools = append([]string(nil), override.ExcludeTools...)
	}
	if override.IncludeTools != nil {
		merged.IncludeTools = append([]string(nil), override.IncludeTools...)
	}
	if override.ToolPermissions != nil {
		merged.ToolPermissions = clonePermissionMap(override.ToolPermissions)
	}
	if override.Command != nil {
		merged.Command = stringPtr(*override.Command)
	}
	if override.Args != nil {
		merged.Args = append([]string(nil), override.Args...)
	}
	if override.Env != nil {
		merged.Env = cloneStringMap(override.Env)
	}
	if override.StartupTimeoutMS != nil {
		merged.StartupTimeoutMS = intPtr(*override.StartupTimeoutMS)
	}
	if override.URL != nil {
		merged.URL = stringPtr(*override.URL)
	}
	if override.Headers != nil {
		merged.Headers = cloneStringMap(override.Headers)
	}

	return merged
}

func cloneMCPServerConfig(server MCPServerConfig) MCPServerConfig {
	cloned := MCPServerConfig{}
	if server.Transport != nil {
		cloned.Transport = stringPtr(*server.Transport)
	}
	if server.Enabled != nil {
		cloned.Enabled = boolPtr(*server.Enabled)
	}
	if server.Trust != nil {
		cloned.Trust = boolPtr(*server.Trust)
	}
	if server.ExcludeTools != nil {
		cloned.ExcludeTools = append([]string(nil), server.ExcludeTools...)
	}
	if server.IncludeTools != nil {
		cloned.IncludeTools = append([]string(nil), server.IncludeTools...)
	}
	if server.ToolPermissions != nil {
		cloned.ToolPermissions = clonePermissionMap(server.ToolPermissions)
	}
	if server.Command != nil {
		cloned.Command = stringPtr(*server.Command)
	}
	if server.Args != nil {
		cloned.Args = append([]string(nil), server.Args...)
	}
	if server.Env != nil {
		cloned.Env = cloneStringMap(server.Env)
	}
	if server.StartupTimeoutMS != nil {
		cloned.StartupTimeoutMS = intPtr(*server.StartupTimeoutMS)
	}
	if server.URL != nil {
		cloned.URL = stringPtr(*server.URL)
	}
	if server.Headers != nil {
		cloned.Headers = cloneStringMap(server.Headers)
	}
	return cloned
}

func clonePermissionMap(src map[string]MCPPermission) map[string]MCPPermission {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]MCPPermission, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func walkUpDirs(start string) []string {
	cleaned := filepath.Clean(start)
	if cleaned == "" {
		return nil
	}
	var dirs []string
	for {
		dirs = append(dirs, cleaned)
		parent := filepath.Dir(cleaned)
		if parent == cleaned {
			break
		}
		cleaned = parent
	}
	return dirs
}

func stringPtr(value string) *string {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

func intPtr(value int) *int {
	return &value
}
