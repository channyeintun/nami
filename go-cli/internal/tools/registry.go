package tools

import (
	"fmt"
	"sync"

	"github.com/channyeintun/go-cli/internal/api"
)

// Registry holds all registered tools.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry creates a registry preloaded with built-in tools.
func NewRegistry() *Registry {
	r := &Registry{
		tools: make(map[string]Tool),
	}

	r.Register(NewBashTool())
	r.Register(NewFileReadTool())
	r.Register(NewFileWriteTool())
	r.Register(NewFileEditTool())
	r.Register(NewGlobTool())
	r.Register(NewGrepTool())
	r.Register(NewWebSearchTool())
	r.Register(NewWebFetchTool())

	return r
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// Get retrieves a tool by name.
func (r *Registry) Get(name string) (Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
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
