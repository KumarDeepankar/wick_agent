package agent

import "context"

// AgentState holds the full conversation state for a thread.
type AgentState struct {
	ThreadID string    `json:"thread_id"`
	Messages []Message `json:"messages"`
	Todos    []Todo    `json:"todos,omitempty"`
	Files    map[string]string `json:"files,omitempty"` // path → content (tracked writes)

	// toolRegistry holds tools registered at runtime by hooks (e.g. FilesystemHook).
	// Not serialized — rebuilt on each agent run.
	toolRegistry map[string]Tool `json:"-"`
}

// Todo represents a task tracked by the TodoList hook.
type Todo struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"` // "pending", "in_progress", "done"
}

// --- State context helpers (follows trace_iface.go pattern) ---

type stateKey struct{}

// WithState stores an AgentState in the context.
func WithState(ctx context.Context, state *AgentState) context.Context {
	return context.WithValue(ctx, stateKey{}, state)
}

// StateFromContext extracts the AgentState, or nil.
func StateFromContext(ctx context.Context) *AgentState {
	s, _ := ctx.Value(stateKey{}).(*AgentState)
	return s
}
