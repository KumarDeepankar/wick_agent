package hooks

import (
	"context"
	"fmt"
	"log"
	"strings"

	"wick_server/agent"
	"wick_server/backend"
	"wick_server/llm"
)

// ToolLookup resolves tool names to agent.Tool instances.
// Used to give sub-agents access to externally registered tools (e.g. Python HTTP tools).
type ToolLookup func(agentID string) []agent.Tool

// SubAgentHook registers a delegate_to_agent tool that invokes configured
// sub-agents as tools. Each sub-agent runs its own LLM+tool loop with an
// isolated thread and returns the result to the parent agent.
type SubAgentHook struct {
	agent.BaseHook
	subagents   []agent.SubAgentCfg
	parentModel any
	backend     backend.Backend
	toolLookup  ToolLookup
}

// NewSubAgentHook creates a sub-agent orchestration hook.
// parentModel is used when a SubAgentCfg.Model is empty (inheritance).
// backend may be nil (sub-agents won't get filesystem tools).
// toolLookup resolves tools for sub-agents (may be nil).
func NewSubAgentHook(subagents []agent.SubAgentCfg, parentModel any, b backend.Backend, lookup ToolLookup) *SubAgentHook {
	return &SubAgentHook{
		subagents:   subagents,
		parentModel: parentModel,
		backend:     b,
		toolLookup:  lookup,
	}
}

func (h *SubAgentHook) Name() string { return "subagent" }

func (h *SubAgentHook) Phases() []string {
	return []string{"before_agent"}
}

// BeforeAgent registers the delegate_to_agent tool listing all configured sub-agents.
func (h *SubAgentHook) BeforeAgent(ctx context.Context, state *agent.AgentState) error {
	if len(h.subagents) == 0 {
		return nil
	}

	// Build agent lookup and enum values
	agentMap := make(map[string]agent.SubAgentCfg, len(h.subagents))
	enumValues := make([]any, 0, len(h.subagents))
	var descParts []string
	for _, sa := range h.subagents {
		agentMap[sa.Name] = sa
		enumValues = append(enumValues, sa.Name)
		descParts = append(descParts, fmt.Sprintf("%s (%s)", sa.Name, sa.Description))
	}

	description := fmt.Sprintf(
		"Delegate a task to a sub-agent. Available: %s.",
		strings.Join(descParts, ", "),
	)

	parentModel := h.parentModel
	b := h.backend
	toolLookup := h.toolLookup

	agent.RegisterToolOnState(state, &agent.FuncTool{
		ToolName: "delegate_to_agent",
		ToolDesc: description,
		ToolParams: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent": map[string]any{
					"type":        "string",
					"description": "Sub-agent name",
					"enum":        enumValues,
				},
				"task": map[string]any{
					"type":        "string",
					"description": "Task description for the sub-agent",
				},
			},
			"required": []string{"agent", "task"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			agentName, _ := args["agent"].(string)
			task, _ := args["task"].(string)
			if agentName == "" || task == "" {
				return "Error: agent and task are required", nil
			}

			sa, ok := agentMap[agentName]
			if !ok {
				return fmt.Sprintf("Error: unknown sub-agent %q", agentName), nil
			}

			return runSubAgent(ctx, sa, task, parentModel, b, state.ThreadID, toolLookup)
		},
	})

	return nil
}

// runSubAgent builds and executes a sub-agent synchronously.
func runSubAgent(
	ctx context.Context,
	sa agent.SubAgentCfg,
	task string,
	parentModel any,
	b backend.Backend,
	parentThreadID string,
	toolLookup ToolLookup,
) (string, error) {
	// Resolve model — inherit from parent if not specified
	modelSpec := any(sa.Model)
	if sa.Model == "" {
		modelSpec = parentModel
	}
	llmClient, _, err := llm.Resolve(modelSpec)
	if err != nil {
		return fmt.Sprintf("Error: failed to resolve model for sub-agent %q: %v", sa.Name, err), nil
	}

	// Build sub-agent config
	cfg := &agent.AgentConfig{
		Name:         sa.Name,
		Model:        modelSpec,
		SystemPrompt: sa.SystemPrompt,
		// No Subagents — prevent infinite recursion
	}

	// Build hooks for sub-agent
	var subHooks []agent.Hook
	subHooks = append(subHooks, NewTodoListHook())
	if b != nil {
		subHooks = append(subHooks, NewFilesystemHook(b))
	}
	subHooks = append(subHooks, NewSummarizationHook(llmClient, 128_000))

	// Resolve tools for the sub-agent
	var tools []agent.Tool
	if toolLookup != nil {
		tools = toolLookup(sa.Name)
	}

	// Create and run sub-agent
	subAgent := agent.NewAgent(sa.Name, cfg, llmClient, tools, subHooks)

	// Isolated thread ID
	subThreadID := fmt.Sprintf("%s:sub:%s", parentThreadID, sa.Name)

	log.Printf("[subagent] delegating to %q (thread: %s, tools: %d)", sa.Name, subThreadID, len(tools))

	result, err := subAgent.Run(ctx, agent.Messages{}.Human(task), subThreadID)
	if err != nil {
		return fmt.Sprintf("Error: sub-agent %q failed: %v", sa.Name, err), nil
	}

	// Extract the final assistant message
	for i := len(result.Messages) - 1; i >= 0; i-- {
		if result.Messages[i].Role == "assistant" && result.Messages[i].Content != "" {
			log.Printf("[subagent] %q completed (%d messages)", sa.Name, len(result.Messages))
			return result.Messages[i].Content, nil
		}
	}

	return fmt.Sprintf("Sub-agent %q completed but produced no response.", sa.Name), nil
}
