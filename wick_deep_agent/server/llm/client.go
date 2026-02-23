package llm

import "context"

// Client is the interface for LLM providers.
type Client interface {
	// Call makes a synchronous LLM call and returns the full response.
	Call(ctx context.Context, req Request) (*Response, error)

	// Stream makes an LLM call and sends chunks to the channel.
	// The channel is closed when streaming is complete.
	Stream(ctx context.Context, req Request, ch chan<- StreamChunk) error
}

// Message represents a chat message for the LLM.
type Message struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
	ToolCalls  []ToolCallInfo `json:"tool_calls,omitempty"`
}

// ToolCallInfo is a tool call attached to an assistant message.
type ToolCallInfo struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Args map[string]any `json:"arguments"`
}

// ToolSchema describes a tool for the LLM.
type ToolSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Request is the input to an LLM call.
type Request struct {
	Model        string       `json:"model"`
	Messages     []Message    `json:"messages"`
	Tools        []ToolSchema `json:"tools,omitempty"`
	SystemPrompt string       `json:"system_prompt,omitempty"`
	MaxTokens    int          `json:"max_tokens,omitempty"`
	Temperature  *float64     `json:"temperature,omitempty"`
}

// Response is the full result of an LLM call.
type Response struct {
	Content   string           `json:"content"`
	ToolCalls []ToolCallResult `json:"tool_calls,omitempty"`
}

// ToolCallResult is a parsed tool call from the LLM response.
type ToolCallResult struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Args map[string]any `json:"arguments"`
}

// StreamChunk is a single chunk from a streaming LLM call.
type StreamChunk struct {
	Delta    string          `json:"delta,omitempty"`
	ToolCall *ToolCallResult `json:"tool_call,omitempty"`
	Done     bool            `json:"done,omitempty"`
	Error    error           `json:"-"`
}
