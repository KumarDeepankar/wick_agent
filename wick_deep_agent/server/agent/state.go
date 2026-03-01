package agent

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
