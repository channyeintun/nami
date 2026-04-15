package mcp

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	configpkg "github.com/channyeintun/chan/internal/config"
)

const (
	defaultDiscoveryTimeout    = 15 * time.Second
	defaultStdioStartupTimeout = 10 * time.Second
	defaultShutdownGrace       = 5 * time.Second
)

type TransportKind string

const (
	TransportStdio TransportKind = "stdio"
	TransportSSE   TransportKind = "sse"
	TransportHTTP  TransportKind = "http"
	TransportWS    TransportKind = "ws"
)

type ToolPermission string

const (
	ToolPermissionRead    ToolPermission = "read"
	ToolPermissionWrite   ToolPermission = "write"
	ToolPermissionExecute ToolPermission = "execute"
)

type ServerDefinition struct {
	Name              string
	Transport         TransportKind
	Enabled           bool
	Trusted           bool
	WorkingDir        string
	ConnectTimeout    time.Duration
	ShutdownGrace     time.Duration
	ExcludeTools      map[string]struct{}
	IncludeTools      map[string]struct{}
	ToolPermissions   map[string]ToolPermission
	Command           string
	Args              []string
	Env               map[string]string
	URL               string
	Headers           map[string]string
	ProjectConfigPath string
}

type ConfigProblem struct {
	ServerName string
	Transport  string
	Err        error
}

func (p ConfigProblem) Error() string {
	if strings.TrimSpace(p.Transport) == "" {
		return fmt.Sprintf("mcp server %q: %v", p.ServerName, p.Err)
	}
	return fmt.Sprintf("mcp server %q (%s): %v", p.ServerName, p.Transport, p.Err)
}

type ResolvedConfig struct {
	Servers  []ServerDefinition
	Problems []ConfigProblem
}

func ResolveConfig(cwd string, cfg configpkg.MCPConfig) ResolvedConfig {
	if len(cfg.Servers) == 0 {
		return ResolvedConfig{}
	}

	serverNames := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		serverNames = append(serverNames, name)
	}
	sort.Strings(serverNames)

	resolved := ResolvedConfig{
		Servers: make([]ServerDefinition, 0, len(serverNames)),
	}
	projectPath := configpkg.LoadProjectMCPOverridePath(cwd)

	for _, name := range serverNames {
		serverCfg := cfg.Servers[name]
		definition, err := resolveServerDefinition(cwd, projectPath, name, serverCfg)
		if err != nil {
			transport := ""
			if serverCfg.Transport != nil {
				transport = strings.TrimSpace(*serverCfg.Transport)
			}
			resolved.Problems = append(resolved.Problems, ConfigProblem{
				ServerName: name,
				Transport:  transport,
				Err:        err,
			})
			continue
		}
		resolved.Servers = append(resolved.Servers, definition)
	}

	return resolved
}

func resolveServerDefinition(cwd, projectPath, name string, cfg configpkg.MCPServerConfig) (ServerDefinition, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ServerDefinition{}, fmt.Errorf("server name cannot be empty")
	}

	transport, err := resolveTransportKind(cfg.Transport)
	if err != nil {
		return ServerDefinition{}, err
	}

	workingDir := filepath.Clean(cwd)
	if strings.TrimSpace(workingDir) == "" {
		workingDir, _ = os.Getwd()
		workingDir = filepath.Clean(workingDir)
	}

	definition := ServerDefinition{
		Name:              name,
		Transport:         transport,
		Enabled:           true,
		Trusted:           cfg.Trust != nil && *cfg.Trust,
		WorkingDir:        workingDir,
		ConnectTimeout:    defaultDiscoveryTimeout,
		ShutdownGrace:     defaultShutdownGrace,
		ExcludeTools:      normalizeToolSet(cfg.ExcludeTools),
		IncludeTools:      normalizeToolSet(cfg.IncludeTools),
		ToolPermissions:   make(map[string]ToolPermission),
		ProjectConfigPath: projectPath,
	}
	if cfg.Enabled != nil {
		definition.Enabled = *cfg.Enabled
	}
	if definition.Transport == TransportStdio {
		definition.ConnectTimeout = defaultStdioStartupTimeout
	}
	if cfg.StartupTimeoutMS != nil {
		if transport != TransportStdio {
			return ServerDefinition{}, fmt.Errorf("startup_timeout_ms is only valid for stdio transport")
		}
		if *cfg.StartupTimeoutMS <= 0 {
			return ServerDefinition{}, fmt.Errorf("startup_timeout_ms must be greater than 0")
		}
		definition.ConnectTimeout = time.Duration(*cfg.StartupTimeoutMS) * time.Millisecond
	}

	toolPermissions, err := normalizeToolPermissions(cfg.ToolPermissions)
	if err != nil {
		return ServerDefinition{}, err
	}
	definition.ToolPermissions = toolPermissions

	switch transport {
	case TransportStdio:
		return resolveStdioDefinition(definition, cfg)
	case TransportSSE:
		return resolveHTTPDefinition(definition, cfg, TransportSSE)
	case TransportHTTP:
		return resolveHTTPDefinition(definition, cfg, TransportHTTP)
	case TransportWS:
		return resolveWSDefinition(definition, cfg)
	default:
		return ServerDefinition{}, fmt.Errorf("unsupported transport %q", transport)
	}
}

func resolveStdioDefinition(definition ServerDefinition, cfg configpkg.MCPServerConfig) (ServerDefinition, error) {
	if cfg.Command == nil || strings.TrimSpace(*cfg.Command) == "" {
		return ServerDefinition{}, fmt.Errorf("stdio transport requires command")
	}
	if cfg.URL != nil {
		return ServerDefinition{}, fmt.Errorf("stdio transport does not accept url")
	}
	if cfg.Headers != nil {
		return ServerDefinition{}, fmt.Errorf("stdio transport does not accept headers")
	}

	command, err := expandEnvString(*cfg.Command)
	if err != nil {
		return ServerDefinition{}, fmt.Errorf("expand command: %w", err)
	}
	args, err := expandEnvSlice(cfg.Args)
	if err != nil {
		return ServerDefinition{}, fmt.Errorf("expand args: %w", err)
	}
	env, err := expandEnvMap(cfg.Env)
	if err != nil {
		return ServerDefinition{}, fmt.Errorf("expand env: %w", err)
	}

	definition.Command = command
	definition.Args = args
	definition.Env = env
	return definition, nil
}

func resolveHTTPDefinition(definition ServerDefinition, cfg configpkg.MCPServerConfig, transport TransportKind) (ServerDefinition, error) {
	if cfg.URL == nil || strings.TrimSpace(*cfg.URL) == "" {
		return ServerDefinition{}, fmt.Errorf("%s transport requires url", transport)
	}
	if cfg.Command != nil {
		return ServerDefinition{}, fmt.Errorf("%s transport does not accept command", transport)
	}
	if cfg.Args != nil {
		return ServerDefinition{}, fmt.Errorf("%s transport does not accept args", transport)
	}
	if cfg.Env != nil {
		return ServerDefinition{}, fmt.Errorf("%s transport does not accept env", transport)
	}

	rawURL, err := expandEnvString(*cfg.URL)
	if err != nil {
		return ServerDefinition{}, fmt.Errorf("expand url: %w", err)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ServerDefinition{}, fmt.Errorf("invalid url: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return ServerDefinition{}, fmt.Errorf("url must include scheme and host")
	}
	headers, err := expandEnvMap(cfg.Headers)
	if err != nil {
		return ServerDefinition{}, fmt.Errorf("expand headers: %w", err)
	}

	definition.URL = rawURL
	definition.Headers = headers
	return definition, nil
}

func resolveWSDefinition(definition ServerDefinition, cfg configpkg.MCPServerConfig) (ServerDefinition, error) {
	definition, err := resolveHTTPDefinition(definition, cfg, TransportWS)
	if err != nil {
		return ServerDefinition{}, err
	}
	return definition, nil
}

func resolveTransportKind(raw *string) (TransportKind, error) {
	if raw == nil {
		return "", fmt.Errorf("transport is required")
	}
	kind := TransportKind(strings.ToLower(strings.TrimSpace(*raw)))
	switch kind {
	case TransportStdio, TransportSSE, TransportHTTP, TransportWS:
		return kind, nil
	default:
		return "", fmt.Errorf("unsupported transport %q", strings.TrimSpace(*raw))
	}
}

func normalizeToolPermissions(raw map[string]configpkg.MCPPermission) (map[string]ToolPermission, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	permissions := make(map[string]ToolPermission, len(raw))
	for name, permission := range raw {
		toolName := strings.TrimSpace(name)
		if toolName == "" {
			return nil, fmt.Errorf("tool_permissions cannot contain an empty tool name")
		}
		normalized, ok := configpkg.NormalizeMCPPermission(string(permission))
		if !ok {
			return nil, fmt.Errorf("tool_permissions[%q] has unsupported permission %q", toolName, permission)
		}
		permissions[toolName] = ToolPermission(normalized)
	}
	return permissions, nil
}

func normalizeToolSet(raw []string) map[string]struct{} {
	if len(raw) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(raw))
	for _, entry := range raw {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		set[trimmed] = struct{}{}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

func expandEnvSlice(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	expanded := make([]string, 0, len(values))
	for index, value := range values {
		resolved, err := expandEnvString(value)
		if err != nil {
			return nil, fmt.Errorf("entry %d: %w", index, err)
		}
		expanded = append(expanded, resolved)
	}
	return expanded, nil
}

func expandEnvMap(values map[string]string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	expanded := make(map[string]string, len(values))
	for key, value := range values {
		resolved, err := expandEnvString(value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		expanded[key] = resolved
	}
	return expanded, nil
}

func expandEnvString(value string) (string, error) {
	if !strings.Contains(value, "$") {
		return value, nil
	}

	missing := make(map[string]struct{})
	expanded := os.Expand(value, func(name string) string {
		resolved, ok := os.LookupEnv(name)
		if !ok {
			missing[name] = struct{}{}
			return ""
		}
		return resolved
	})
	if len(missing) == 0 {
		return expanded, nil
	}

	names := make([]string, 0, len(missing))
	for name := range missing {
		names = append(names, name)
	}
	sort.Strings(names)
	return "", fmt.Errorf("missing environment variable(s): %s", strings.Join(names, ", "))
}
