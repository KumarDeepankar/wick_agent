package agent

import (
	"context"

	"wick_go/llm"
)

// ModelCallWrapFunc is the signature for the "next" function in the model call chain.
type ModelCallWrapFunc func(ctx context.Context, msgs []Message) (*llm.Response, error)

// ToolCallFunc is the signature for the "next" function in the tool call chain.
type ToolCallFunc func(ctx context.Context, call ToolCall) (*ToolResult, error)

// Hook defines the interface for agent middleware (onion ring pattern).
// Matches the 4 hook points from the Python deepagents middleware.
type Hook interface {
	// Name returns the hook identifier.
	Name() string

	// Phases returns which phases this hook is active in.
	// Valid values: "before_agent", "modify_request", "wrap_model_call", "wrap_tool_call"
	Phases() []string

	// BeforeAgent is called once before the agent loop starts.
	// Use for one-time setup: load skills catalog, memory files, register tools.
	BeforeAgent(ctx context.Context, state *AgentState) error

	// WrapModelCall wraps each LLM call (summarization, prompt caching).
	// Return nil to pass through to the next hook.
	WrapModelCall(ctx context.Context, msgs []Message, next ModelCallWrapFunc) (*llm.Response, error)

	// WrapToolCall wraps each tool execution (logging, large result eviction).
	WrapToolCall(ctx context.Context, call ToolCall, next ToolCallFunc) (*ToolResult, error)

	// ModifyRequest is called before each LLM call to modify the message list.
	// Use for injecting system prompt sections.
	ModifyRequest(ctx context.Context, msgs []Message) ([]Message, error)
}

// BaseHook provides no-op defaults for all hook methods.
// Embed this to only override the methods you need.
type BaseHook struct{}

func (BaseHook) Name() string { return "base" }

func (BaseHook) Phases() []string {
	return []string{"before_agent", "modify_request", "wrap_model_call", "wrap_tool_call"}
}

func (BaseHook) BeforeAgent(ctx context.Context, state *AgentState) error {
	return nil
}

func (BaseHook) WrapModelCall(ctx context.Context, msgs []Message, next ModelCallWrapFunc) (*llm.Response, error) {
	return next(ctx, msgs)
}

func (BaseHook) WrapToolCall(ctx context.Context, call ToolCall, next ToolCallFunc) (*ToolResult, error) {
	return next(ctx, call)
}

func (BaseHook) ModifyRequest(ctx context.Context, msgs []Message) ([]Message, error) {
	return msgs, nil
}
