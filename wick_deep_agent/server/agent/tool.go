package agent

import "context"

// Tool defines the interface for agent tools.
type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any // JSON Schema
	Execute(ctx context.Context, args map[string]any) (string, error)
}

// FuncTool wraps a plain function as a Tool.
type FuncTool struct {
	ToolName   string
	ToolDesc   string
	ToolParams map[string]any
	Fn         func(ctx context.Context, args map[string]any) (string, error)
}

func (f *FuncTool) Name() string              { return f.ToolName }
func (f *FuncTool) Description() string       { return f.ToolDesc }
func (f *FuncTool) Parameters() map[string]any { return f.ToolParams }
func (f *FuncTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	return f.Fn(ctx, args)
}

// ToolRegistry is a thread-safe registry of named tools.
type ToolRegistry struct {
	tools map[string]Tool
}

// NewToolRegistry creates a new tool registry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: make(map[string]Tool)}
}

// Register adds a tool to the registry.
func (r *ToolRegistry) Register(tool Tool) {
	r.tools[tool.Name()] = tool
}

// Get returns a tool by name or nil.
func (r *ToolRegistry) Get(name string) Tool {
	return r.tools[name]
}

// List returns all tool names.
func (r *ToolRegistry) List() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// All returns a copy of all tools.
func (r *ToolRegistry) All() map[string]Tool {
	out := make(map[string]Tool, len(r.tools))
	for k, v := range r.tools {
		out[k] = v
	}
	return out
}

// RegisterToolOnState adds a tool to an AgentState's per-session tool registry.
// Used by hooks like FilesystemHook to register tools at runtime.
func RegisterToolOnState(state *AgentState, tool Tool) {
	if state.toolRegistry == nil {
		state.toolRegistry = make(map[string]Tool)
	}
	state.toolRegistry[tool.Name()] = tool
}

// RemoveToolFromState removes a tool from an AgentState's per-session tool registry.
func RemoveToolFromState(state *AgentState, name string) {
	if state.toolRegistry != nil {
		delete(state.toolRegistry, name)
	}
}

// ClearToolsFromState removes all tools from an AgentState's per-session tool registry.
func ClearToolsFromState(state *AgentState) {
	state.toolRegistry = make(map[string]Tool)
}

// StateTools returns a copy of all tools in the state's tool registry.
func StateTools(state *AgentState) map[string]Tool {
	if state.toolRegistry == nil {
		return nil
	}
	out := make(map[string]Tool, len(state.toolRegistry))
	for k, v := range state.toolRegistry {
		out[k] = v
	}
	return out
}

// StateToolNames returns the names of all tools in the state's tool registry.
func StateToolNames(state *AgentState) []string {
	if state.toolRegistry == nil {
		return nil
	}
	names := make([]string, 0, len(state.toolRegistry))
	for name := range state.toolRegistry {
		names = append(names, name)
	}
	return names
}
