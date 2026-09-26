package mcp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	configpkg "github.com/channyeintun/nami/internal/config"
	transportpkg "github.com/channyeintun/nami/internal/mcp/transports"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type ServerStatus struct {
	Name                  string
	Transport             string
	Enabled               bool
	Connected             bool
	Trusted               bool
	SessionID             string
	Server                ServerInfo
	ToolCount             int
	PromptCount           int
	ResourceCount         int
	ResourceTemplateCount int
	ToolNames             []string
	Warnings              []string
	Error                 string
}

type DiscoveredTool struct {
	ServerName  string
	Transport   string
	Trusted     bool
	Permission  ToolPermission
	Tool        ToolDescriptor
	Server      ServerInfo
	SessionID   string
	WorkingDir  string
	ProjectPath string
}

type ResourceInventory struct {
	ServerName        string
	Connected         bool
	ResourcesCapable  bool
	Resources         []ResourceDescriptor
	ResourceTemplates []ResourceTemplateDescriptor
	Warnings          []string
	Error             string
}

type ResourceReadResult struct {
	ServerName string
	URI        string
	Contents   []ResourceContent
}

type StatusCallback func(ServerStatus)

type Manager struct {
	mu          sync.RWMutex
	definitions map[string]ServerDefinition
	order       []string
	runtimes    map[string]*serverRuntime
	statuses    map[string]ServerStatus
	closed      bool
}

type serverRuntime struct {
	definition        ServerDefinition
	session           Session
	server            ServerInfo
	resourcesCapable  bool
	toolByName        map[string]ToolDescriptor
	toolNames         []string
	prompts           []PromptDescriptor
	resources         []ResourceDescriptor
	resourceTemplates []ResourceTemplateDescriptor
}

func NewManager(cwd string, cfg configpkg.MCPConfig) *Manager {
	resolved := ResolveConfig(cwd, cfg)
	manager := &Manager{
		definitions: make(map[string]ServerDefinition, len(resolved.Servers)),
		runtimes:    make(map[string]*serverRuntime, len(resolved.Servers)),
		statuses:    make(map[string]ServerStatus, len(resolved.Servers)+len(resolved.Problems)),
	}

	for _, definition := range resolved.Servers {
		manager.definitions[definition.Name] = definition
		manager.appendOrder(definition.Name)
		manager.statuses[definition.Name] = ServerStatus{
			Name:      definition.Name,
			Transport: string(definition.Transport),
			Enabled:   definition.Enabled,
			Trusted:   definition.Trusted,
		}
	}
	for _, problem := range resolved.Problems {
		manager.appendOrder(problem.ServerName)
		manager.statuses[problem.ServerName] = ServerStatus{
			Name:      problem.ServerName,
			Transport: problem.Transport,
			Error:     problem.Err.Error(),
		}
	}

	sort.Strings(manager.order)
	return manager
}

func (m *Manager) Start(ctx context.Context) {
	m.StartWithCallback(ctx, nil)
}

func (m *Manager) StartWithCallback(ctx context.Context, callback StatusCallback) {
	if m == nil {
		return
	}

	m.mu.RLock()
	order := append([]string(nil), m.order...)
	definitions := make(map[string]ServerDefinition, len(m.definitions))
	maps.Copy(definitions, m.definitions)
	m.mu.RUnlock()

	var wg sync.WaitGroup
	for _, name := range order {
		definition, ok := definitions[name]
		if !ok || !definition.Enabled {
			continue
		}
		def := definition
		wg.Go(func() {
			status := m.startServer(ctx, def)
			if callback != nil {
				callback(status)
			}
		})
	}
	wg.Wait()
}

func (m *Manager) Close() error {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	runtimes := make([]*serverRuntime, 0, len(m.runtimes))
	for _, runtime := range m.runtimes {
		runtimes = append(runtimes, runtime)
	}
	m.mu.Unlock()

	var closeErrs []error
	for _, runtime := range runtimes {
		if runtime == nil || runtime.session == nil {
			continue
		}
		if err := runtime.session.Close(); err != nil {
			closeErrs = append(closeErrs, fmt.Errorf("close %s: %w", runtime.definition.Name, err))
		}
	}
	return errors.Join(closeErrs...)
}

func (m *Manager) Tools() []DiscoveredTool {
	if m == nil {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var tools []DiscoveredTool
	for _, name := range m.order {
		runtime, ok := m.runtimes[name]
		if !ok || runtime == nil {
			continue
		}
		status := m.statuses[name]
		for _, toolName := range runtime.toolNames {
			descriptor := runtime.toolByName[toolName]
			tools = append(tools, DiscoveredTool{
				ServerName:  runtime.definition.Name,
				Transport:   string(runtime.definition.Transport),
				Trusted:     runtime.definition.Trusted,
				Permission:  runtime.toolPermission(toolName),
				Tool:        descriptor,
				Server:      runtime.server,
				SessionID:   status.SessionID,
				WorkingDir:  runtime.definition.WorkingDir,
				ProjectPath: runtime.definition.ProjectConfigPath,
			})
		}
	}
	return tools
}

func (m *Manager) CallTool(ctx context.Context, serverName, toolName string, arguments any) (CallResult, error) {
	if m == nil {
		return CallResult{}, fmt.Errorf("mcp manager is unavailable")
	}

	m.mu.RLock()
	runtime, ok := m.runtimes[serverName]
	m.mu.RUnlock()
	if !ok || runtime == nil {
		return CallResult{}, fmt.Errorf("mcp server %q is not connected", serverName)
	}
	if _, ok := runtime.toolByName[toolName]; !ok {
		return CallResult{}, fmt.Errorf("mcp server %q does not expose tool %q", serverName, toolName)
	}
	return runtime.session.CallTool(ctx, toolName, arguments)
}

func (m *Manager) Statuses() []ServerStatus {
	if m == nil {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := make([]ServerStatus, 0, len(m.order))
	for _, name := range m.order {
		status, ok := m.statuses[name]
		if !ok {
			continue
		}
		status.ToolNames = append([]string(nil), status.ToolNames...)
		status.Warnings = append([]string(nil), status.Warnings...)
		statuses = append(statuses, status)
	}
	return statuses
}

func (m *Manager) ResourceInventories(serverName string, includeTemplates bool) ([]ResourceInventory, error) {
	if m == nil {
		return nil, fmt.Errorf("mcp manager is unavailable")
	}

	filter := strings.TrimSpace(serverName)
	m.mu.RLock()
	defer m.mu.RUnlock()

	if filter != "" {
		if _, ok := m.statuses[filter]; !ok {
			return nil, fmt.Errorf("unknown mcp server %q", filter)
		}
	}

	inventories := make([]ResourceInventory, 0, len(m.order))
	for _, name := range m.order {
		if filter != "" && name != filter {
			continue
		}
		status, ok := m.statuses[name]
		if !ok {
			continue
		}
		inventory := ResourceInventory{
			ServerName: name,
			Connected:  status.Connected,
			Warnings:   append([]string(nil), status.Warnings...),
			Error:      status.Error,
		}
		if runtime := m.runtimes[name]; runtime != nil {
			inventory.ResourcesCapable = runtime.resourcesCapable
			inventory.Resources = append([]ResourceDescriptor(nil), runtime.resources...)
			if includeTemplates {
				inventory.ResourceTemplates = append([]ResourceTemplateDescriptor(nil), runtime.resourceTemplates...)
			}
		}
		inventories = append(inventories, inventory)
	}

	if len(inventories) == 0 {
		return nil, fmt.Errorf("no MCP servers matched")
	}
	return inventories, nil
}

func (m *Manager) ReadResource(ctx context.Context, serverName, uri string) (ResourceReadResult, error) {
	if m == nil {
		return ResourceReadResult{}, fmt.Errorf("mcp manager is unavailable")
	}
	serverName = strings.TrimSpace(serverName)
	uri = strings.TrimSpace(uri)
	if serverName == "" {
		return ResourceReadResult{}, fmt.Errorf("mcp server name is required")
	}
	if uri == "" {
		return ResourceReadResult{}, fmt.Errorf("resource uri is required")
	}

	m.mu.RLock()
	runtime, ok := m.runtimes[serverName]
	m.mu.RUnlock()
	if !ok || runtime == nil {
		return ResourceReadResult{}, fmt.Errorf("mcp server %q is not connected", serverName)
	}
	if !runtime.resourcesCapable {
		return ResourceReadResult{}, fmt.Errorf("mcp server %q does not expose resources", serverName)
	}
	contents, err := runtime.session.ReadResource(ctx, uri)
	if err != nil {
		return ResourceReadResult{}, err
	}
	return ResourceReadResult{ServerName: serverName, URI: uri, Contents: contents}, nil
}
func (m *Manager) startServer(ctx context.Context, definition ServerDefinition) ServerStatus {
	connectCtx, cancel := context.WithTimeout(ctx, definition.ConnectTimeout)
	defer cancel()

	session, err := connectSession(connectCtx, definition)
	if err != nil {
		m.updateStatus(definition.Name, func(status *ServerStatus) {
			status.Error = err.Error()
		})
		return m.status(definition.Name)
	}

	tools, err := session.ListTools(connectCtx)
	if err != nil {
		_ = session.Close()
		m.updateStatus(definition.Name, func(status *ServerStatus) {
			status.Error = fmt.Sprintf("list tools: %v", err)
		})
		return m.status(definition.Name)
	}

	filteredTools := filterTools(definition, tools)
	prompts, promptErr := session.ListPrompts(connectCtx)
	resources, resourceErr := session.ListResources(connectCtx)
	templates, templateErr := session.ListResourceTemplates(connectCtx)

	runtime := &serverRuntime{
		definition:        definition,
		session:           session,
		server:            session.ServerInfo(),
		resourcesCapable:  session.HasResourcesCapability(),
		toolByName:        make(map[string]ToolDescriptor, len(filteredTools)),
		toolNames:         make([]string, 0, len(filteredTools)),
		prompts:           prompts,
		resources:         resources,
		resourceTemplates: templates,
	}
	for _, tool := range filteredTools {
		runtime.toolByName[tool.Name] = tool
		runtime.toolNames = append(runtime.toolNames, tool.Name)
	}
	sort.Strings(runtime.toolNames)

	warnings := make([]string, 0, 3)
	if promptErr != nil {
		warnings = append(warnings, fmt.Sprintf("list prompts: %v", promptErr))
	}
	if resourceErr != nil {
		warnings = append(warnings, fmt.Sprintf("list resources: %v", resourceErr))
	}
	if templateErr != nil {
		warnings = append(warnings, fmt.Sprintf("list resource templates: %v", templateErr))
	}

	status, published := m.publishRuntime(runtime, session.ID(), warnings)
	if !published {
		// The manager was closed while this server was still connecting. Close
		// snapshotted the runtimes before this one was published, so nothing
		// else will ever close it — release the session here (and, for stdio,
		// terminate its child process) instead of leaking it.
		if err := session.Close(); err != nil {
			status.Warnings = append(status.Warnings, fmt.Sprintf("close session after shutdown: %v", err))
		}
	}
	return status
}

// publishRuntime records a freshly connected server, unless the manager has
// already been closed. It reports whether the runtime was published; when it
// was not, the caller owns the session and must close it, because Close has
// already taken its snapshot and will never see this runtime.
func (m *Manager) publishRuntime(runtime *serverRuntime, sessionID string, warnings []string) (ServerStatus, bool) {
	definition := runtime.definition

	m.mu.Lock()
	defer m.mu.Unlock()

	status := m.statuses[definition.Name]
	if m.closed {
		status.ToolNames = append([]string(nil), status.ToolNames...)
		status.Warnings = append([]string(nil), status.Warnings...)
		return status, false
	}

	m.runtimes[definition.Name] = runtime
	status.Name = definition.Name
	status.Transport = string(definition.Transport)
	status.Enabled = definition.Enabled
	status.Connected = true
	status.Trusted = definition.Trusted
	status.SessionID = sessionID
	status.Server = runtime.server
	status.ToolCount = len(runtime.toolNames)
	status.PromptCount = len(runtime.prompts)
	status.ResourceCount = len(runtime.resources)
	status.ResourceTemplateCount = len(runtime.resourceTemplates)
	status.ToolNames = append([]string(nil), runtime.toolNames...)
	status.Warnings = warnings
	status.Error = ""
	m.statuses[definition.Name] = status
	return status, true
}

func (m *Manager) status(name string) ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status := m.statuses[name]
	status.ToolNames = append([]string(nil), status.ToolNames...)
	status.Warnings = append([]string(nil), status.Warnings...)
	return status
}

func (m *Manager) updateStatus(name string, update func(*ServerStatus)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := m.statuses[name]
	update(&status)
	m.statuses[name] = status
}

func (m *Manager) appendOrder(name string) {
	if slices.Contains(m.order, name) {
		return
	}
	m.order = append(m.order, name)
}

func (r *serverRuntime) toolPermission(toolName string) ToolPermission {
	if r == nil {
		return ToolPermissionExecute
	}
	if !r.definition.Trusted {
		return ToolPermissionExecute
	}
	if permission, ok := r.definition.ToolPermissions[toolName]; ok {
		return permission
	}
	return ToolPermissionExecute
}

func filterTools(definition ServerDefinition, tools []ToolDescriptor) []ToolDescriptor {
	if len(tools) == 0 {
		return nil
	}
	filtered := make([]ToolDescriptor, 0, len(tools))
	for _, tool := range tools {
		if tool.Name == "" {
			continue
		}
		if len(definition.IncludeTools) > 0 {
			if _, ok := definition.IncludeTools[tool.Name]; !ok {
				continue
			}
		}
		if _, excluded := definition.ExcludeTools[tool.Name]; excluded {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

func connectSession(ctx context.Context, definition ServerDefinition) (Session, error) {
	transport, err := transportpkg.Build(transportpkg.Config{
		Kind:           string(definition.Transport),
		Command:        definition.Command,
		Args:           append([]string(nil), definition.Args...),
		Env:            cloneStringMap(definition.Env),
		WorkingDir:     definition.WorkingDir,
		URL:            definition.URL,
		Headers:        cloneStringMap(definition.Headers),
		ConnectTimeout: definition.ConnectTimeout,
		ShutdownGrace:  definition.ShutdownGrace,
	})
	if err != nil {
		return nil, err
	}

	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "nami",
		Version: "dev",
	}, nil)
	// Roots are deprecated as of protocol 2026-07-28 (SEP-2577) but remain
	// functional through the deprecation window, and servers such as the
	// filesystem server still scope their access by them.
	if root := rootURI(definition.WorkingDir); root != "" {
		client.AddRoots(&sdkmcp.Root{
			Name: filepath.Base(definition.WorkingDir),
			URI:  root,
		})
	}

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, err
	}
	return NewClientSession(session), nil
}

func rootURI(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Clean(trimmed))}).String()
	if strings.HasPrefix(uri, "file:///") {
		return uri
	}
	if after, ok := strings.CutPrefix(uri, "file://"); ok {
		return "file:///" + after
	}
	return uri
}

func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(source))
	maps.Copy(cloned, source)
	return cloned
}
