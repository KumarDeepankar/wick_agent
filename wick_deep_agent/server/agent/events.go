package agent

// StreamEvent is sent from the agent loop to the SSE handler.
type StreamEvent struct {
	Event    string `json:"event"`              // on_chat_model_stream, on_tool_start, on_tool_end, done, error
	Name     string `json:"name,omitempty"`     // tool name or model name
	RunID    string `json:"run_id,omitempty"`
	Data     any    `json:"data,omitempty"`
	ThreadID string `json:"thread_id,omitempty"` // set on "done" event
	TaskID   string `json:"task_id,omitempty"`   // set on on_async_task_* events
}
