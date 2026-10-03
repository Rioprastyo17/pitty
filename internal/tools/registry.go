package tools

import (
	"context"
	"sort"
)

// ToolParam describes a single parameter for a tool.
type ToolParam struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Required    bool     `json:"-"`
	Enum        []string `json:"enum,omitempty"`
}

// ToolHandler is the interface all tools must implement.
type ToolHandler interface {
	Name() string
	Description() string
	Parameters() map[string]ToolParam
	RequiredParams() []string
	Execute(ctx context.Context, args map[string]interface{}) (string, error)
}

// Registry holds all registered tools.
type Registry struct {
	tools map[string]ToolHandler
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]ToolHandler)}
}

// Register adds a tool to the registry.
func (r *Registry) Register(handler ToolHandler) {
	r.tools[handler.Name()] = handler
}

// Get retrieves a tool by name.
func (r *Registry) Get(name string) (ToolHandler, bool) {
	h, ok := r.tools[name]
	return h, ok
}

// List returns all registered tools sorted by name.
func (r *Registry) List() []ToolHandler {
	handlers := make([]ToolHandler, 0, len(r.tools))
	for _, h := range r.tools {
		handlers = append(handlers, h)
	}
	sort.Slice(handlers, func(i, j int) bool {
		return handlers[i].Name() < handlers[j].Name()
	})
	return handlers
}

// Len returns the number of registered tools.
func (r *Registry) Len() int {
	return len(r.tools)
}


