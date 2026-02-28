package handlers

import (
	"sync"

	"wick_server/agent"
)

// ToolStore is a thread-safe store for externally registered tools.
type ToolStore struct {
	mu          sync.RWMutex
	tools       map[string]*agent.HTTPTool
	nativeTools map[string]agent.Tool
}

// NewToolStore creates a new external tool store.
func NewToolStore() *ToolStore {
	return &ToolStore{
		tools:       make(map[string]*agent.HTTPTool),
		nativeTools: make(map[string]agent.Tool),
	}
}

// Register adds or replaces an external HTTP tool.
func (ts *ToolStore) Register(tool *agent.HTTPTool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.tools[tool.ToolName] = tool
}

// AddTool adds or replaces a native agent.Tool (e.g. FuncTool).
func (ts *ToolStore) AddTool(t agent.Tool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.nativeTools[t.Name()] = t
}

// Remove removes an external tool. Returns true if it existed.
func (ts *ToolStore) Remove(name string) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	_, ok1 := ts.tools[name]
	if ok1 {
		delete(ts.tools, name)
	}
	_, ok2 := ts.nativeTools[name]
	if ok2 {
		delete(ts.nativeTools, name)
	}
	return ok1 || ok2
}

// Get returns an HTTP tool by name or nil.
func (ts *ToolStore) Get(name string) *agent.HTTPTool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.tools[name]
}

// All returns all external tools (HTTP + native) as the agent.Tool interface.
func (ts *ToolStore) All() []agent.Tool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	out := make([]agent.Tool, 0, len(ts.tools)+len(ts.nativeTools))
	for _, t := range ts.tools {
		out = append(out, t)
	}
	for _, t := range ts.nativeTools {
		out = append(out, t)
	}
	return out
}

// Names returns all registered external tool names.
func (ts *ToolStore) Names() []string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	names := make([]string, 0, len(ts.tools)+len(ts.nativeTools))
	for name := range ts.tools {
		names = append(names, name)
	}
	for name := range ts.nativeTools {
		names = append(names, name)
	}
	return names
}
