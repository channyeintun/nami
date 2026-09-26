package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrProjectScopeUnavailable = errors.New("project scope requires a git repository")

type MCPScope string

const (
	MCPScopeUser    MCPScope = "user"
	MCPScopeProject MCPScope = "project"
)

func ParseMCPScope(raw string) (MCPScope, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(MCPScopeUser):
		return MCPScopeUser, nil
	case string(MCPScopeProject):
		return MCPScopeProject, nil
	default:
		return "", fmt.Errorf("unsupported MCP scope %q (valid: project, user)", strings.TrimSpace(raw))
	}
}

func (scope MCPScope) String() string {
	return string(scope)
}

func MCPConfigPathForScope(cwd string, scope MCPScope) (string, error) {
	switch scope {
	case MCPScopeUser:
		return ConfigPath(), nil
	case MCPScopeProject:
		path := LoadProjectMCPOverridePath(cwd)
		if strings.TrimSpace(path) == "" {
			return "", ErrProjectScopeUnavailable
		}
		return path, nil
	default:
		return "", fmt.Errorf("unsupported MCP scope %q", scope)
	}
}

func LoadMCPConfigForScope(cwd string, scope MCPScope) (MCPConfig, string, error) {
	path, err := MCPConfigPathForScope(cwd, scope)
	if err != nil {
		return MCPConfig{}, "", err
	}

	switch scope {
	case MCPScopeUser:
		cfg := loadUserConfig()
		return cfg.MCP.Clone(), path, nil
	case MCPScopeProject:
		cfg, projectPath, err := loadProjectMCPOverride(cwd)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return MCPConfig{}, path, nil
			}
			return MCPConfig{}, projectPath, err
		}
		return cfg.Clone(), projectPath, nil
	default:
		return MCPConfig{}, path, fmt.Errorf("unsupported MCP scope %q", scope)
	}
}

func SaveMCPConfigForScope(cwd string, scope MCPScope, cfg MCPConfig) (string, error) {
	path, err := MCPConfigPathForScope(cwd, scope)
	if err != nil {
		return "", err
	}

	cloned := cfg.Clone()

	switch scope {
	case MCPScopeUser:
		if err := Update(func(persisted *Config) { persisted.MCP = cloned }); err != nil {
			return path, err
		}
		return path, nil
	case MCPScopeProject:
		if len(cloned.Servers) == 0 {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return path, err
			}
			return path, nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return path, err
		}
		data, err := json.MarshalIndent(cloned, "", "  ")
		if err != nil {
			return path, err
		}
		data = append(data, '\n')
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return path, err
		}
		return path, nil
	default:
		return path, fmt.Errorf("unsupported MCP scope %q", scope)
	}
}
