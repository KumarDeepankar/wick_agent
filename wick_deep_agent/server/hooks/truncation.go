package hooks

import (
	"context"
	"fmt"

	"wick_server/agent"
)

// DefaultMaxToolOutputChars is the default threshold for truncating tool output.
const DefaultMaxToolOutputChars = 80_000

// TruncationHook truncates large tool results with head+tail formatting.
// Filesystem tools (ls, glob, grep, read_file, edit_file, write_file) are excluded.
//
// Configure via BackendCfg.MaxToolOutputChars:
//
//	0  → use default (80,000 chars)
//	-1 → disable truncation entirely
//	>0 → custom threshold
type TruncationHook struct {
	agent.BaseHook
	maxChars int
}

// NewTruncationHook creates a TruncationHook with the given character limit.
// Pass 0 for default (80,000), -1 to disable truncation.
func NewTruncationHook(maxToolOutputChars int) *TruncationHook {
	limit := maxToolOutputChars
	if limit == 0 {
		limit = DefaultMaxToolOutputChars
	}
	return &TruncationHook{maxChars: limit}
}

func (h *TruncationHook) Name() string { return "truncation" }

// WrapToolCall truncates tool results that exceed the configured threshold.
func (h *TruncationHook) WrapToolCall(ctx context.Context, call agent.ToolCall, next agent.ToolCallFunc) (*agent.ToolResult, error) {
	result, err := next(ctx, call)
	if err != nil || result == nil {
		return result, err
	}

	// Disabled via -1
	if h.maxChars < 0 {
		return result, nil
	}

	// Filesystem tools are excluded — their output is already bounded.
	excluded := map[string]bool{
		"ls": true, "glob": true, "grep": true,
		"read_file": true, "edit_file": true, "write_file": true,
	}

	if len(result.Output) > h.maxChars && !excluded[call.Name] {
		// Reserve ~100 chars for the truncation notice, split the rest 50/50 between head and tail.
		// Minimum 200 chars per side to keep output useful.
		half := (h.maxChars - 100) / 2
		if half < 200 {
			half = 200
		}
		head := result.Output[:half]
		tail := result.Output[len(result.Output)-half:]
		result.Output = fmt.Sprintf(
			"%s\n\n... [Output truncated: %d chars total. Showing first and last %d chars] ...\n\n%s",
			head, len(result.Output), half, tail,
		)
	}

	return result, nil
}
