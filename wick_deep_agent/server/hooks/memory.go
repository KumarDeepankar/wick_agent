package hooks

import (
	"context"
	"fmt"
	"strings"

	"wick_server/agent"
	"wick_server/backend"
	"wick_server/llm"
)

// MemoryHook loads AGENTS.md files from configured paths and injects
// their content wrapped in <agent_memory> tags into the system prompt.
type MemoryHook struct {
	agent.BaseHook
	backend      backend.Backend
	paths        []string
	memoryContent string
}

// NewMemoryHook creates a memory hook that loads from the given paths.
func NewMemoryHook(b backend.Backend, paths []string) *MemoryHook {
	return &MemoryHook{
		backend: b,
		paths:   paths,
	}
}

func (h *MemoryHook) Name() string { return "memory" }

func (h *MemoryHook) Phases() []string {
	return []string{"before_agent", "modify_request"}
}

// BeforeAgent loads AGENTS.md content from configured paths.
func (h *MemoryHook) BeforeAgent(ctx context.Context, state *agent.AgentState) error {
	var parts []string

	for _, path := range h.paths {
		result := h.backend.Execute(fmt.Sprintf("cat %s 2>/dev/null", shellQuote(path)))
		if result.ExitCode == 0 && strings.TrimSpace(result.Output) != "" {
			parts = append(parts, result.Output)
		}
	}

	if len(parts) > 0 {
		h.memoryContent = strings.Join(parts, "\n\n---\n\n")
	}

	return nil
}

// ModifyRequest injects memory content wrapped in <agent_memory> tags.
func (h *MemoryHook) ModifyRequest(ctx context.Context, msgs []agent.Message) ([]agent.Message, error) {
	if h.memoryContent == "" {
		return msgs, nil
	}

	injection := fmt.Sprintf(`

<agent_memory>
%s
</agent_memory>

Guidelines for agent memory:
- This memory persists across conversations
- You can update it by using edit_file on the AGENTS.md file
- Use it to track important context, decisions, and patterns
- Keep entries concise and organized`, h.memoryContent)

	// Find or create system message
	if len(msgs) > 0 && msgs[0].Role == "system" {
		msgs[0].Content += injection
	} else {
		msgs = append([]agent.Message{{Role: "system", Content: injection}}, msgs...)
	}

	return msgs, nil
}

func (h *MemoryHook) WrapModelCall(ctx context.Context, msgs []agent.Message, next agent.ModelCallWrapFunc) (*llm.Response, error) {
	return next(ctx, msgs)
}

func (h *MemoryHook) WrapToolCall(ctx context.Context, call agent.ToolCall, next agent.ToolCallFunc) (*agent.ToolResult, error) {
	return next(ctx, call)
}
