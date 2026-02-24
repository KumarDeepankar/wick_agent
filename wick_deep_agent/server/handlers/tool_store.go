package handlers

import (
	"sync"

	"wick_go/agent"
)

// ToolStore is a thread-safe store for externally registered tools.
type ToolStore struct {
	mu    sync.RWMutex
	tools map[string]*agent.HTTPTool
}

// NewToolStore creates a new external tool store.
func NewToolStore() *ToolStore {
	return &ToolStore{
		tools: make(map[string]*agent.HTTPTool),
	}
}

// Register adds or replaces an external tool.
func (ts *ToolStore) Register(tool *agent.HTTPTool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.tools[tool.ToolName] = tool
}

// Remove removes an external tool. Returns true if it existed.
func (ts *ToolStore) Remove(name string) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	_, ok := ts.tools[name]
	if ok {
		delete(ts.tools, name)
	}
	return ok
}

// Get returns a tool by name or nil.
func (ts *ToolStore) Get(name string) *agent.HTTPTool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.tools[name]
}

// All returns all external tools as the agent.Tool interface.
func (ts *ToolStore) All() []agent.Tool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	out := make([]agent.Tool, 0, len(ts.tools))
	for _, t := range ts.tools {
		out = append(out, t)
	}
	return out
}

// Names returns all registered external tool names.
func (ts *ToolStore) Names() []string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	names := make([]string, 0, len(ts.tools))
	for name := range ts.tools {
		names = append(names, name)
	}
	return names
}
