package handlers

import (
	"sync"

	"wick_server/agent"
)

// ToolStore is a thread-safe store for externally registered tools.
// Tools are scoped per-agent. An empty agent ID ("") means global —
// global tools are included for all agents.
type ToolStore struct {
	mu          sync.RWMutex
	tools       map[string]map[string]*agent.HTTPTool // [agentID][toolName]
	nativeTools map[string]map[string]agent.Tool      // [agentID][toolName]
}

// NewToolStore creates a new external tool store.
func NewToolStore() *ToolStore {
	return &ToolStore{
		tools:       make(map[string]map[string]*agent.HTTPTool),
		nativeTools: make(map[string]map[string]agent.Tool),
	}
}

// Register adds or replaces a global external HTTP tool (available to all agents).
func (ts *ToolStore) Register(tool *agent.HTTPTool) {
	ts.RegisterForAgent("", tool)
}

// RegisterForAgent adds or replaces an HTTP tool scoped to a specific agent.
// Use agentID="" for global tools visible to all agents.
func (ts *ToolStore) RegisterForAgent(agentID string, tool *agent.HTTPTool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.tools[agentID] == nil {
		ts.tools[agentID] = make(map[string]*agent.HTTPTool)
	}
	ts.tools[agentID][tool.ToolName] = tool
}

// AddTool adds or replaces a global native agent.Tool.
func (ts *ToolStore) AddTool(t agent.Tool) {
	ts.AddToolForAgent("", t)
}

// AddToolForAgent adds or replaces a native tool scoped to a specific agent.
func (ts *ToolStore) AddToolForAgent(agentID string, t agent.Tool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.nativeTools[agentID] == nil {
		ts.nativeTools[agentID] = make(map[string]agent.Tool)
	}
	ts.nativeTools[agentID][t.Name()] = t
}

// Remove removes a tool by name from a specific agent scope. Returns true if it existed.
func (ts *ToolStore) Remove(name string) bool {
	return ts.RemoveForAgent("", name)
}

// RemoveForAgent removes a tool by name from a specific agent scope.
func (ts *ToolStore) RemoveForAgent(agentID, name string) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	found := false
	if m := ts.tools[agentID]; m != nil {
		if _, ok := m[name]; ok {
			delete(m, name)
			found = true
		}
	}
	if m := ts.nativeTools[agentID]; m != nil {
		if _, ok := m[name]; ok {
			delete(m, name)
			found = true
		}
	}
	return found
}

// Get returns a global HTTP tool by name or nil.
func (ts *ToolStore) Get(name string) *agent.HTTPTool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	if m := ts.tools[""]; m != nil {
		return m[name]
	}
	return nil
}

// ForAgent returns tools scoped to the given agent plus global tools.
// Agent-specific tools override global tools with the same name.
func (ts *ToolStore) ForAgent(agentID string) []agent.Tool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	merged := make(map[string]agent.Tool)

	// Global tools first
	for _, t := range ts.tools[""] {
		merged[t.ToolName] = t
	}
	for _, t := range ts.nativeTools[""] {
		merged[t.Name()] = t
	}

	// Agent-specific tools override globals
	if agentID != "" {
		for _, t := range ts.tools[agentID] {
			merged[t.ToolName] = t
		}
		for _, t := range ts.nativeTools[agentID] {
			merged[t.Name()] = t
		}
	}

	out := make([]agent.Tool, 0, len(merged))
	for _, t := range merged {
		out = append(out, t)
	}
	return out
}

// All returns all external tools across all agents (for backward compat).
func (ts *ToolStore) All() []agent.Tool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	seen := make(map[string]agent.Tool)
	for _, m := range ts.tools {
		for _, t := range m {
			seen[t.ToolName] = t
		}
	}
	for _, m := range ts.nativeTools {
		for _, t := range m {
			seen[t.Name()] = t
		}
	}
	out := make([]agent.Tool, 0, len(seen))
	for _, t := range seen {
		out = append(out, t)
	}
	return out
}

// Names returns all registered external tool names (across all agents).
func (ts *ToolStore) Names() []string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	seen := make(map[string]bool)
	for _, m := range ts.tools {
		for name := range m {
			seen[name] = true
		}
	}
	for _, m := range ts.nativeTools {
		for name := range m {
			seen[name] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	return names
}
