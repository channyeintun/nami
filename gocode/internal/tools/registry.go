package tools

import (
	"fmt"
	"sync"

	"github.com/channyeintun/gocode/internal/api"
)

// Registry holds all registered tools.
type Registry struct {
	mu          sync.RWMutex
	tools       map[string]Tool
	aliases     map[string]Tool
	FileHistory *FileHistory
}

// NewRegistry creates a registry preloaded with built-in tools.
func NewRegistry() *Registry {
	r := NewEmptyRegistry()

	r.Register(NewBashTool())
	r.Register(NewThinkTool())
	r.Register(NewListDirTool())
	r.Register(NewCreateFileTool())
	r.Register(NewFileReadTool())
	r.Register(NewFileWriteTool())
	r.Register(NewFileEditTool())
	r.Register(NewApplyPatchTool())
	r.Register(NewMultiReplaceFileContentTool())
	r.Register(NewFileDiffPreviewTool())
	r.Register(NewGlobTool())
	r.Register(NewGrepTool())
	r.Register(NewGoDefinitionTool())
	r.Register(NewGoReferencesTool())
	r.Register(NewProjectOverviewTool())
	r.Register(NewDependencyOverviewTool())
	r.Register(NewSymbolSearchTool())
	r.Register(NewWebSearchTool())
	r.Register(NewWebFetchTool())
	r.Register(NewGitTool())
	r.Register(NewListCommandsTool())
	r.Register(NewCommandStatusTool())
	r.Register(NewSendCommandInputTool())
	r.Register(NewStopCommandTool())
	r.Register(NewForgetCommandTool())
	r.Register(NewFileHistoryTool())
	r.Register(NewFileHistoryRewindTool())
	r.Register(NewSaveImplementationPlanTool())
	r.Register(NewUpsertTaskListTool())
	r.Register(NewSaveWalkthroughTool())
	r.RegisterAlias(NewFileSearchAliasTool())
	r.RegisterAlias(NewReadFileAliasTool())

	return r
}

// NewEmptyRegistry creates an empty registry without built-in tools.
func NewEmptyRegistry() *Registry {
	return &Registry{
		tools:   make(map[string]Tool),
		aliases: make(map[string]Tool),
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// RegisterAlias adds a hidden compatibility alias that is available for
// execution but not advertised in the tool definitions shown to the model.
func (r *Registry) RegisterAlias(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.aliases[t.Name()] = t
}

// Get retrieves a tool by name.
func (r *Registry) Get(name string) (Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	if !ok {
		t, ok = r.aliases[name]
		if !ok {
			return nil, fmt.Errorf("unknown tool: %s", name)
		}
	}
	return t, nil
}

// List returns all registered tool names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// Definitions returns API tool definitions for all registered tools.
func (r *Registry) Definitions() []api.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]api.ToolDefinition, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, api.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return defs
}

// CloneFiltered returns a new registry containing only the named tools.
func (r *Registry) CloneFiltered(names []string) *Registry {
	allowed := make(map[string]struct{}, len(names))
	for _, name := range names {
		allowed[name] = struct{}{}
	}

	clone := NewEmptyRegistry()
	r.mu.RLock()
	defer r.mu.RUnlock()
	for name, tool := range r.tools {
		if _, ok := allowed[name]; ok {
			clone.tools[name] = tool
		}
	}
	clone.FileHistory = r.FileHistory
	return clone
}
