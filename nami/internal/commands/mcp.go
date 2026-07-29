package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	configpkg "github.com/channyeintun/nami/internal/config"
	mcppkg "github.com/channyeintun/nami/internal/mcp"
)

var mcpServerNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type MCPAddOptions struct {
	Scope     string
	Transport string
	Env       []string
	Headers   []string
	Trust     bool
	Disabled  bool
	StartupMS int
}

type MCPCommandResult struct {
	OutputLines  []string
	WarningLines []string
}

func RunMCPAdd(cwd string, args []string, options MCPAddOptions) (MCPCommandResult, error) {
	name := strings.TrimSpace(args[0])
	if err := validateMCPServerName(name); err != nil {
		return MCPCommandResult{}, err
	}
	if options.StartupMS < 0 {
		return MCPCommandResult{}, fmt.Errorf("startup-timeout-ms must be greater than or equal to 0")
	}
	scope, err := configpkg.ParseMCPScope(options.Scope)
	if err != nil {
		return MCPCommandResult{}, err
	}
	transport, err := parseMCPTransport(options.Transport)
	if err != nil {
		return MCPCommandResult{}, err
	}
	server, warning, summary, err := buildMCPServerConfig(transport, strings.TrimSpace(args[1]), args[2:], options)
	if err != nil {
		return MCPCommandResult{}, err
	}
	return addMCPServer(cwd, scope, name, server, summary, warning)
}

func RunMCPAddJSON(cwd, scopeRaw, name, rawJSON string) (MCPCommandResult, error) {
	scope, err := configpkg.ParseMCPScope(scopeRaw)
	if err != nil {
		return MCPCommandResult{}, err
	}
	trimmedName := strings.TrimSpace(name)
	if err := validateMCPServerName(trimmedName); err != nil {
		return MCPCommandResult{}, err
	}
	var server configpkg.MCPServerConfig
	if err := json.Unmarshal([]byte(rawJSON), &server); err != nil {
		return MCPCommandResult{}, fmt.Errorf("parse server JSON: %w", err)
	}
	return addMCPServer(cwd, scope, trimmedName, server, "", "")
}

func RunMCPList(cwd, scopeRaw string) (MCPCommandResult, error) {
	configToList, sources, err := loadMCPConfigForListing(cwd, scopeRaw)
	if err != nil {
		return MCPCommandResult{}, err
	}
	if len(configToList.Servers) == 0 {
		return MCPCommandResult{OutputLines: []string{"No MCP servers configured."}}, nil
	}

	statuses, closeErr := loadMCPStatuses(cwd, configToList)
	result := MCPCommandResult{}
	if closeErr != nil {
		result.WarningLines = append(result.WarningLines, fmt.Sprintf("warning: close MCP manager: %v", closeErr))
	}

	for _, status := range statuses {
		summary := renderStatusSummary(status)
		source := sources[status.Name]
		server := configToList.Servers[status.Name]
		location := renderServerSummary(server)
		if source != "" {
			result.OutputLines = append(result.OutputLines, fmt.Sprintf("%s: %s [%s] - %s", status.Name, location, source, summary))
			continue
		}
		result.OutputLines = append(result.OutputLines, fmt.Sprintf("%s: %s - %s", status.Name, location, summary))
	}
	return result, nil
}

func RunMCPGet(cwd, rawName, scopeRaw string) (MCPCommandResult, error) {
	name := strings.TrimSpace(rawName)
	if err := validateMCPServerName(name); err != nil {
		return MCPCommandResult{}, err
	}

	targetScope, server, configuredScopes, err := resolveMCPServerForGet(cwd, name, scopeRaw)
	if err != nil {
		return MCPCommandResult{}, err
	}
	result := MCPCommandResult{}
	status, closeErr, err := loadSingleMCPStatus(cwd, name, server)
	if closeErr != nil {
		result.WarningLines = append(result.WarningLines, fmt.Sprintf("warning: close MCP manager: %v", closeErr))
	}
	if err != nil {
		return result, err
	}

	result.OutputLines = append(result.OutputLines,
		name,
		fmt.Sprintf("  Scope: %s", targetScope),
	)
	if len(configuredScopes) > 1 {
		result.OutputLines = append(result.OutputLines, fmt.Sprintf("  Configured in: %s", strings.Join(configuredScopes, ", ")))
	}
	result.OutputLines = append(result.OutputLines,
		fmt.Sprintf("  Transport: %s", effectiveTransportLabel(server)),
		fmt.Sprintf("  Status: %s", renderStatusSummary(status)),
	)

	if server.Enabled != nil {
		result.OutputLines = append(result.OutputLines, fmt.Sprintf("  Enabled: %t", *server.Enabled))
	}
	if server.Trust != nil {
		result.OutputLines = append(result.OutputLines, fmt.Sprintf("  Trusted: %t", *server.Trust))
	}
	if server.StartupTimeoutMS != nil {
		result.OutputLines = append(result.OutputLines, fmt.Sprintf("  Startup timeout: %dms", *server.StartupTimeoutMS))
	}
	if server.Command != nil {
		result.OutputLines = append(result.OutputLines, fmt.Sprintf("  Command: %s", *server.Command))
	}
	if len(server.Args) > 0 {
		result.OutputLines = append(result.OutputLines, fmt.Sprintf("  Args: %s", strings.Join(server.Args, " ")))
	}
	if len(server.Env) > 0 {
		result.OutputLines = append(result.OutputLines, "  Environment:")
		result.OutputLines = append(result.OutputLines, formatSortedKeyValue(server.Env, "    %s=%s")...)
	}
	if server.URL != nil {
		result.OutputLines = append(result.OutputLines, fmt.Sprintf("  URL: %s", *server.URL))
	}
	if len(server.Headers) > 0 {
		result.OutputLines = append(result.OutputLines, "  Headers:")
		result.OutputLines = append(result.OutputLines, formatSortedKeyValue(server.Headers, "    %s: %s")...)
	}
	if len(server.IncludeTools) > 0 {
		result.OutputLines = append(result.OutputLines, fmt.Sprintf("  Include tools: %s", strings.Join(server.IncludeTools, ", ")))
	}
	if len(server.ExcludeTools) > 0 {
		result.OutputLines = append(result.OutputLines, fmt.Sprintf("  Exclude tools: %s", strings.Join(server.ExcludeTools, ", ")))
	}
	if len(server.ToolPermissions) > 0 {
		result.OutputLines = append(result.OutputLines, "  Tool permissions:")
		toolNames := make([]string, 0, len(server.ToolPermissions))
		for toolName := range server.ToolPermissions {
			toolNames = append(toolNames, toolName)
		}
		sort.Strings(toolNames)
		for _, toolName := range toolNames {
			result.OutputLines = append(result.OutputLines, fmt.Sprintf("    %s: %s", toolName, server.ToolPermissions[toolName]))
		}
	}
	return result, nil
}

func RunMCPRemove(cwd, rawName, scopeRaw string) (MCPCommandResult, error) {
	name := strings.TrimSpace(rawName)
	if err := validateMCPServerName(name); err != nil {
		return MCPCommandResult{}, err
	}

	scope, err := resolveMCPRemoveScope(cwd, name, scopeRaw)
	if err != nil {
		return MCPCommandResult{}, err
	}
	cfg, _, err := configpkg.LoadMCPConfigForScope(cwd, scope)
	if err != nil {
		return MCPCommandResult{}, err
	}
	if _, ok := cfg.Servers[name]; !ok {
		return MCPCommandResult{}, fmt.Errorf("No MCP server found with name %q in %s config", name, scope)
	}
	delete(cfg.Servers, name)
	path, err := configpkg.SaveMCPConfigForScope(cwd, scope, cfg)
	if err != nil {
		return MCPCommandResult{}, err
	}
	return MCPCommandResult{OutputLines: []string{
		fmt.Sprintf("Removed MCP server %s from %s config", name, scope),
		fmt.Sprintf("Config path: %s", path),
	}}, nil
}

func addMCPServer(cwd string, scope configpkg.MCPScope, name string, server configpkg.MCPServerConfig, summary string, warning string) (MCPCommandResult, error) {
	if err := validateMCPServerConfig(cwd, name, server); err != nil {
		return MCPCommandResult{}, err
	}
	existing, _, err := configpkg.LoadMCPConfigForScope(cwd, scope)
	if err != nil {
		return MCPCommandResult{}, err
	}
	if existing.Servers == nil {
		existing.Servers = make(map[string]configpkg.MCPServerConfig)
	}
	if _, ok := existing.Servers[name]; ok {
		return MCPCommandResult{}, fmt.Errorf("MCP server %q already exists in %s config", name, scope)
	}
	existing.Servers[name] = server
	path, err := configpkg.SaveMCPConfigForScope(cwd, scope, existing)
	if err != nil {
		return MCPCommandResult{}, err
	}
	transport := effectiveTransportLabel(server)
	if summary == "" {
		summary = renderServerSummary(server)
	}
	output := []string{}
	if strings.TrimSpace(summary) != "" {
		output = append(output, fmt.Sprintf("Added %s MCP server %s to %s config: %s", transport, name, scope, summary))
	} else {
		output = append(output, fmt.Sprintf("Added %s MCP server %s to %s config", transport, name, scope))
	}
	output = append(output, fmt.Sprintf("Config path: %s", path))
	result := MCPCommandResult{OutputLines: output}
	if warning != "" {
		result.WarningLines = append(result.WarningLines, warning)
	}
	return result, nil
}

func validateMCPServerName(name string) error {
	if name == "" {
		return fmt.Errorf("server name cannot be empty")
	}
	if !mcpServerNamePattern.MatchString(name) {
		return fmt.Errorf("invalid server name %q: names can only contain letters, numbers, hyphens, and underscores", name)
	}
	return nil
}

func parseMCPTransport(raw string) (mcppkg.TransportKind, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return mcppkg.TransportStdio, nil
	}
	transport := mcppkg.TransportKind(trimmed)
	switch transport {
	case mcppkg.TransportStdio, mcppkg.TransportHTTP, mcppkg.TransportSSE, mcppkg.TransportWS:
		return transport, nil
	default:
		return "", fmt.Errorf("unsupported MCP transport %q (valid: stdio, http, sse, ws)", strings.TrimSpace(raw))
	}
}

func buildMCPServerConfig(transport mcppkg.TransportKind, commandOrURL string, extraArgs []string, options MCPAddOptions) (configpkg.MCPServerConfig, string, string, error) {
	transportValue := string(transport)
	server := configpkg.MCPServerConfig{Transport: &transportValue}
	if options.Disabled {
		enabled := false
		server.Enabled = &enabled
	}
	if options.Trust {
		trusted := true
		server.Trust = &trusted
	}
	if options.StartupMS > 0 {
		server.StartupTimeoutMS = new(options.StartupMS)
	}

	switch transport {
	case mcppkg.TransportStdio:
		if len(options.Headers) > 0 {
			return configpkg.MCPServerConfig{}, "", "", fmt.Errorf("stdio transport does not accept headers")
		}
		env, err := parseEnvAssignments(options.Env)
		if err != nil {
			return configpkg.MCPServerConfig{}, "", "", err
		}
		server.Command = new(commandOrURL)
		if len(extraArgs) > 0 {
			server.Args = append([]string(nil), extraArgs...)
		}
		server.Env = env
		warning := ""
		if looksLikeURL(commandOrURL) {
			warning = fmt.Sprintf("warning: %q looks like a URL; if this is an HTTP server, use --transport http, or use --transport sse for SSE", commandOrURL)
		}
		return server, warning, strings.TrimSpace(strings.Join(append([]string{commandOrURL}, extraArgs...), " ")), nil
	case mcppkg.TransportHTTP, mcppkg.TransportSSE, mcppkg.TransportWS:
		if len(extraArgs) > 0 {
			return configpkg.MCPServerConfig{}, "", "", fmt.Errorf("%s transport accepts only a single URL argument", transport)
		}
		if len(options.Env) > 0 {
			return configpkg.MCPServerConfig{}, "", "", fmt.Errorf("%s transport does not accept env vars", transport)
		}
		headers, err := parseHeaderAssignments(options.Headers)
		if err != nil {
			return configpkg.MCPServerConfig{}, "", "", err
		}
		server.URL = new(commandOrURL)
		server.Headers = headers
		return server, "", commandOrURL, nil
	default:
		return configpkg.MCPServerConfig{}, "", "", fmt.Errorf("unsupported transport %q", transport)
	}
}

func parseEnvAssignments(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	parsed := make(map[string]string, len(values))
	for _, value := range values {
		parts := strings.SplitN(value, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid env assignment %q: expected KEY=value", value)
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			return nil, fmt.Errorf("invalid env assignment %q: key cannot be empty", value)
		}
		parsed[key] = parts[1]
	}
	return parsed, nil
}

func parseHeaderAssignments(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	parsed := make(map[string]string, len(values))
	for _, value := range values {
		parts := strings.SplitN(value, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid header %q: expected Key: Value", value)
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			return nil, fmt.Errorf("invalid header %q: key cannot be empty", value)
		}
		parsed[key] = strings.TrimSpace(parts[1])
	}
	return parsed, nil
}

func looksLikeURL(value string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") || strings.HasPrefix(trimmed, "ws://") || strings.HasPrefix(trimmed, "wss://")
}

func validateMCPServerConfig(cwd, name string, server configpkg.MCPServerConfig) error {
	resolved := mcppkg.ResolveConfig(cwd, configpkg.MCPConfig{Servers: map[string]configpkg.MCPServerConfig{name: server}})
	if len(resolved.Problems) > 0 {
		return resolved.Problems[0].Err
	}
	if len(resolved.Servers) != 1 {
		return fmt.Errorf("failed to validate MCP server %q", name)
	}
	return nil
}

func loadMCPConfigForListing(cwd, scopeRaw string) (configpkg.MCPConfig, map[string]string, error) {
	if strings.TrimSpace(scopeRaw) != "" {
		scope, err := configpkg.ParseMCPScope(scopeRaw)
		if err != nil {
			return configpkg.MCPConfig{}, nil, err
		}
		cfg, _, err := configpkg.LoadMCPConfigForScope(cwd, scope)
		if err != nil {
			return configpkg.MCPConfig{}, nil, err
		}
		sources := make(map[string]string, len(cfg.Servers))
		for name := range cfg.Servers {
			sources[name] = scope.String()
		}
		return cfg, sources, nil
	}

	userCfg, _, err := configpkg.LoadMCPConfigForScope(cwd, configpkg.MCPScopeUser)
	if err != nil {
		return configpkg.MCPConfig{}, nil, err
	}
	projectCfg, _, err := configpkg.LoadMCPConfigForScope(cwd, configpkg.MCPScopeProject)
	if err != nil && !errors.Is(err, configpkg.ErrProjectScopeUnavailable) {
		return configpkg.MCPConfig{}, nil, err
	}
	merged := configpkg.MergeMCPConfig(userCfg, projectCfg)
	sources := make(map[string]string, len(merged.Servers))
	for name := range userCfg.Servers {
		sources[name] = configpkg.MCPScopeUser.String()
	}
	for name := range projectCfg.Servers {
		if _, ok := userCfg.Servers[name]; ok {
			sources[name] = fmt.Sprintf("%s (overrides user)", configpkg.MCPScopeProject)
			continue
		}
		sources[name] = configpkg.MCPScopeProject.String()
	}
	return merged, sources, nil
}

func loadMCPStatuses(cwd string, cfg configpkg.MCPConfig) ([]mcppkg.ServerStatus, error) {
	manager := mcppkg.NewManager(cwd, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	manager.Start(ctx)
	statuses := manager.Statuses()
	return statuses, manager.Close()
}

func resolveMCPServerForGet(cwd, name, scopeRaw string) (string, configpkg.MCPServerConfig, []string, error) {
	entries, err := collectScopedServers(cwd, name)
	if err != nil {
		return "", configpkg.MCPServerConfig{}, nil, err
	}
	if len(entries) == 0 {
		return "", configpkg.MCPServerConfig{}, nil, fmt.Errorf("No MCP server found with name %q", name)
	}

	if strings.TrimSpace(scopeRaw) != "" {
		scope, err := configpkg.ParseMCPScope(scopeRaw)
		if err != nil {
			return "", configpkg.MCPServerConfig{}, nil, err
		}
		server, ok := entries[scope]
		if !ok {
			return "", configpkg.MCPServerConfig{}, nil, fmt.Errorf("No MCP server found with name %q in %s config", name, scope)
		}
		return scope.String(), server, []string{scope.String()}, nil
	}

	configuredScopes := make([]string, 0, len(entries))
	if _, ok := entries[configpkg.MCPScopeUser]; ok {
		configuredScopes = append(configuredScopes, configpkg.MCPScopeUser.String())
	}
	if _, ok := entries[configpkg.MCPScopeProject]; ok {
		configuredScopes = append(configuredScopes, configpkg.MCPScopeProject.String())
	}
	sort.Strings(configuredScopes)
	if server, ok := entries[configpkg.MCPScopeProject]; ok {
		return configpkg.MCPScopeProject.String(), server, configuredScopes, nil
	}
	return configpkg.MCPScopeUser.String(), entries[configpkg.MCPScopeUser], configuredScopes, nil
}

func loadSingleMCPStatus(cwd, name string, server configpkg.MCPServerConfig) (mcppkg.ServerStatus, error, error) {
	manager := mcppkg.NewManager(cwd, configpkg.MCPConfig{Servers: map[string]configpkg.MCPServerConfig{name: server}})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	manager.Start(ctx)
	statuses := manager.Statuses()
	closeErr := manager.Close()
	if len(statuses) == 0 {
		return mcppkg.ServerStatus{}, closeErr, fmt.Errorf("MCP server %q did not produce a status entry", name)
	}
	return statuses[0], closeErr, nil
}

func resolveMCPRemoveScope(cwd, name, scopeRaw string) (configpkg.MCPScope, error) {
	if strings.TrimSpace(scopeRaw) != "" {
		return configpkg.ParseMCPScope(scopeRaw)
	}
	entries, err := collectScopedServers(cwd, name)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("No MCP server found with name %q", name)
	}
	if len(entries) == 1 {
		for scope := range entries {
			return scope, nil
		}
	}
	return "", fmt.Errorf("MCP server %q exists in multiple scopes; rerun with --scope project or --scope user", name)
}

func collectScopedServers(cwd, name string) (map[configpkg.MCPScope]configpkg.MCPServerConfig, error) {
	entries := make(map[configpkg.MCPScope]configpkg.MCPServerConfig, 2)
	userCfg, _, err := configpkg.LoadMCPConfigForScope(cwd, configpkg.MCPScopeUser)
	if err != nil {
		return nil, err
	}
	if server, ok := userCfg.Servers[name]; ok {
		entries[configpkg.MCPScopeUser] = server
	}
	projectCfg, _, err := configpkg.LoadMCPConfigForScope(cwd, configpkg.MCPScopeProject)
	if err != nil && !errors.Is(err, configpkg.ErrProjectScopeUnavailable) {
		return nil, err
	}
	if server, ok := projectCfg.Servers[name]; ok {
		entries[configpkg.MCPScopeProject] = server
	}
	return entries, nil
}

func renderStatusSummary(status mcppkg.ServerStatus) string {
	if !status.Enabled {
		return "disabled"
	}
	if strings.TrimSpace(status.Error) != "" {
		return "error: " + status.Error
	}
	if !status.Connected {
		return "not connected"
	}
	summary := fmt.Sprintf("connected, %d tools, %d prompts, %d resources, %d templates", status.ToolCount, status.PromptCount, status.ResourceCount, status.ResourceTemplateCount)
	if len(status.Warnings) > 0 {
		summary += " [warnings: " + strings.Join(status.Warnings, "; ") + "]"
	}
	return summary
}

func renderServerSummary(server configpkg.MCPServerConfig) string {
	switch effectiveTransportLabel(server) {
	case string(mcppkg.TransportStdio):
		parts := []string{}
		if server.Command != nil {
			parts = append(parts, *server.Command)
		}
		parts = append(parts, server.Args...)
		return strings.TrimSpace(strings.Join(parts, " "))
	default:
		if server.URL != nil {
			return *server.URL
		}
		return "(" + effectiveTransportLabel(server) + ")"
	}
}

func effectiveTransportLabel(server configpkg.MCPServerConfig) string {
	if server.Transport != nil && strings.TrimSpace(*server.Transport) != "" {
		return strings.TrimSpace(*server.Transport)
	}
	if server.URL != nil {
		return string(mcppkg.TransportHTTP)
	}
	if server.Command != nil {
		return string(mcppkg.TransportStdio)
	}
	return "unknown"
}

func formatSortedKeyValue(values map[string]string, format string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf(format, key, values[key]))
	}
	return lines
}
