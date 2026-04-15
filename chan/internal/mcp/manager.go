package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	configpkg "github.com/channyeintun/chan/internal/config"
	transportpkg "github.com/channyeintun/chan/internal/mcp/transports"
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
	if m == nil {
		return
	}

	m.mu.RLock()
	order := append([]string(nil), m.order...)
	definitions := make(map[string]ServerDefinition, len(m.definitions))
	for name, definition := range m.definitions {
		definitions[name] = definition
	}
	m.mu.RUnlock()

	var wg sync.WaitGroup
	for _, name := range order {
		definition, ok := definitions[name]
		if !ok || !definition.Enabled {
			continue
		}
		wg.Add(1)
		go func(def ServerDefinition) {
			defer wg.Done()
			m.startServer(ctx, def)
		}(definition)
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

func (m *Manager) startServer(ctx context.Context, definition ServerDefinition) {
	connectCtx, cancel := context.WithTimeout(ctx, definition.ConnectTimeout)
	defer cancel()

	session, err := connectSession(connectCtx, definition)
	if err != nil {
		m.updateStatus(definition.Name, func(status *ServerStatus) {
			status.Error = err.Error()
		})
		return
	}

	tools, err := session.ListTools(connectCtx)
	if err != nil {
		_ = session.Close()
		m.updateStatus(definition.Name, func(status *ServerStatus) {
			status.Error = fmt.Sprintf("list tools: %v", err)
		})
		return
	}

	filteredTools := filterTools(definition, tools)
	prompts, promptErr := session.ListPrompts(connectCtx)
	resources, resourceErr := session.ListResources(connectCtx)
	templates, templateErr := session.ListResourceTemplates(connectCtx)

	runtime := &serverRuntime{
		definition:        definition,
		session:           session,
		server:            session.ServerInfo(),
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

	m.mu.Lock()
	m.runtimes[definition.Name] = runtime
	status := m.statuses[definition.Name]
	status.Name = definition.Name
	status.Transport = string(definition.Transport)
	status.Enabled = definition.Enabled
	status.Connected = true
	status.Trusted = definition.Trusted
	status.SessionID = session.ID()
	status.Server = runtime.server
	status.ToolCount = len(runtime.toolNames)
	status.PromptCount = len(prompts)
	status.ResourceCount = len(resources)
	status.ResourceTemplateCount = len(templates)
	status.ToolNames = append([]string(nil), runtime.toolNames...)
	status.Warnings = warnings
	status.Error = ""
	m.statuses[definition.Name] = status
	m.mu.Unlock()
}

func (m *Manager) updateStatus(name string, update func(*ServerStatus)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := m.statuses[name]
	update(&status)
	m.statuses[name] = status
}

func (m *Manager) appendOrder(name string) {
	for _, existing := range m.order {
		if existing == name {
			return
		}
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
		Name:    "chan",
		Version: "dev",
	}, nil)
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
	if strings.HasPrefix(uri, "file://") {
		return "file:///" + strings.TrimPrefix(uri, "file://")
	}
	return uri
}

func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
