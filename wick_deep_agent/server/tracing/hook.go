package tracing

import (
	"context"

	"wick_go/agent"
	"wick_go/llm"
)

// TracingHook implements agent.Hook. It wraps LLM calls and tool calls
// with timed spans. Per-hook BeforeAgent/ModifyRequest detail is recorded
// by the agent loop itself via agent.TraceRecorder.
type TracingHook struct {
	agent.BaseHook
}

// NewTracingHook creates a new tracing hook.
func NewTracingHook() *TracingHook {
	return &TracingHook{}
}

func (h *TracingHook) Name() string { return "tracing" }

func (h *TracingHook) Phases() []string {
	return []string{"wrap_model_call", "wrap_tool_call"}
}

// BeforeAgent and ModifyRequest use BaseHook defaults (no-ops).
// The loop records per-hook spans for all hooks including this one.

func (h *TracingHook) WrapModelCall(ctx context.Context, msgs []agent.Message, next agent.ModelCallWrapFunc) (*llm.Response, error) {
	tr := agent.TraceFromContext(ctx)
	if tr == nil {
		return next(ctx, msgs)
	}

	s := tr.StartSpan("llm.call")
	s.Set("message_count", len(msgs))
	resp, err := next(ctx, msgs)
	if err != nil {
		s.Set("error", err.Error())
	} else {
		s.Set("content_length", len(resp.Content))
		s.Set("tool_calls_count", len(resp.ToolCalls))
		if len(resp.Content) <= 500 {
			s.Set("content", resp.Content)
		} else {
			s.Set("content", resp.Content[:500]+"...(truncated)")
		}
		if len(resp.ToolCalls) > 0 {
			names := make([]string, len(resp.ToolCalls))
			for i, tc := range resp.ToolCalls {
				names[i] = tc.Name
			}
			s.Set("tool_calls", names)
		}
	}
	s.End()
	return resp, err
}

func (h *TracingHook) WrapToolCall(ctx context.Context, call agent.ToolCall, next agent.ToolCallFunc) (*agent.ToolResult, error) {
	tr := agent.TraceFromContext(ctx)
	if tr == nil {
		return next(ctx, call)
	}

	s := tr.StartSpan("tool.call")
	s.Set("tool_name", call.Name)
	s.Set("tool_call_id", call.ID)
	s.Set("tool_args", call.Args)
	result, err := next(ctx, call)
	if err != nil {
		s.Set("error", err.Error())
	} else if result != nil {
		s.Set("output_length", len(result.Output))
		if len(result.Output) <= 500 {
			s.Set("output", result.Output)
		} else {
			s.Set("output", result.Output[:500]+"...(truncated)")
		}
		if result.Error != "" {
			s.Set("tool_error", result.Error)
		}
	}
	s.End()
	return result, err
}
