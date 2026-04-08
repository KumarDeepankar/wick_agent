package hooks

import (
	"context"
	"fmt"
	"log"
	"strings"

	"wick_server/agent"
	"wick_server/backend"
	"wick_server/llm"
	"wick_server/tracing"
)

// ToolLookup resolves tool names to agent.Tool instances.
// Used to give sub-agents access to externally registered tools (e.g. Python HTTP tools).
type ToolLookup func(agentID string) []agent.Tool

// SubAgentHook registers a delegate_to_agent tool that invokes configured
// sub-agents as tools. Each sub-agent runs its own LLM+tool loop with an
// isolated thread and returns the result to the parent agent.
// When the parent event channel is available in context, sub-agent streaming
// events are forwarded to the parent SSE connection for real-time UI rendering.
type SubAgentHook struct {
	agent.BaseHook
	subagents  []agent.SubAgentCfg
	parentCfg  *agent.AgentConfig
	backend    backend.Backend
	toolLookup ToolLookup
}

// NewSubAgentHook creates a sub-agent orchestration hook.
// parentCfg is the parent agent's config — sub-agents inherit model, skills, and memory from it.
// backend may be nil (sub-agents won't get filesystem tools).
// toolLookup resolves tools for sub-agents (may be nil).
func NewSubAgentHook(subagents []agent.SubAgentCfg, parentCfg *agent.AgentConfig, b backend.Backend, lookup ToolLookup) *SubAgentHook {
	return &SubAgentHook{
		subagents:  subagents,
		parentCfg:  parentCfg,
		backend:    b,
		toolLookup: lookup,
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

	parentCfg := h.parentCfg
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

			// Resolve the parent tool call ID and event channel from context
			// so the UI can associate sub-agent events with the delegate_to_agent card.
			parentToolID := agent.ToolCallIDFromContext(ctx)
			parentEventCh := agent.EventChFromContext(ctx)

			return runSubAgent(ctx, sa, task, parentCfg, b, state.ThreadID, toolLookup, parentEventCh, parentToolID)
		},
	})

	return nil
}

// runSubAgent builds and executes a sub-agent, streaming events when parentEventCh is set.
// Sub-agents are full agents — they get the same hook chain as parent agents.
// The only difference is they run on an isolated thread and have no sub-agents of their own.
func runSubAgent(
	ctx context.Context,
	sa agent.SubAgentCfg,
	task string,
	parentCfg *agent.AgentConfig,
	b backend.Backend,
	parentThreadID string,
	toolLookup ToolLookup,
	parentEventCh chan<- agent.StreamEvent,
	parentToolID string,
) (string, error) {
	// Resolve model — inherit from parent if not specified
	modelSpec := any(sa.Model)
	if sa.Model == "" {
		modelSpec = parentCfg.Model
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
	}

	// Build hooks — same chain as parent agents (mirrors handlers.go buildAgent).
	// Sub-agents are full agents; the only thing they lack is sub-agents of their own.
	var subHooks []agent.Hook

	// Truncation hook (outermost)
	var maxToolOutputChars int
	if parentCfg.Backend != nil {
		maxToolOutputChars = parentCfg.Backend.MaxToolOutputChars
	}
	subHooks = append(subHooks, NewTruncationHook(maxToolOutputChars))

	// Tracing hook
	subHooks = append(subHooks, tracing.NewTracingHook())

	// TodoList hook
	subHooks = append(subHooks, NewTodoListHook())

	// Filesystem hook
	if b != nil {
		subHooks = append(subHooks, NewFilesystemHook(b))
	}

	// Skills hook — inherit skill paths from parent so sub-agents can discover and activate skills.
	// Auto-activate the skill matching the sub-agent's name (no-op if no match).
	if parentCfg.Skills != nil && len(parentCfg.Skills.Paths) > 0 && b != nil {
		subHooks = append(subHooks, NewLazySkillsHook(b, parentCfg.Skills.Paths, nil).WithAutoActivate(sa.Name))
	}

	// Memory hook — inherit memory paths from parent
	if parentCfg.Memory != nil && len(parentCfg.Memory.Paths) > 0 && b != nil {
		subHooks = append(subHooks, NewMemoryHook(b, parentCfg.Memory.Paths))
	}

	// No SubAgentHook — sub-agents have no sub-agents of their own

	// Summarization hook
	contextWindow := parentCfg.ContextWindow
	if contextWindow <= 0 {
		contextWindow = 128_000
	}
	subHooks = append(subHooks, NewSummarizationHook(llmClient, contextWindow))

	// Resolve tools for the sub-agent
	var tools []agent.Tool
	if toolLookup != nil {
		tools = toolLookup(sa.Name)
	}

	// Create and run sub-agent
	subAgent := agent.NewAgent(sa.Name, cfg, llmClient, tools, subHooks)

	// Isolated thread ID — include parentToolID so parallel invocations of the
	// same sub-agent each get their own thread and don't stomp on each other's state.
	subThreadID := fmt.Sprintf("%s:sub:%s", parentThreadID, sa.Name)
	if parentToolID != "" {
		subThreadID = fmt.Sprintf("%s:sub:%s:%s", parentThreadID, sa.Name, parentToolID)
	}

	log.Printf("[subagent] delegating to %q (thread: %s, tools: %d)", sa.Name, subThreadID, len(tools))

	// Use streaming path when parent event channel is available
	if parentEventCh != nil {
		return runSubAgentStreaming(ctx, subAgent, sa.Name, task, subThreadID, parentEventCh, parentToolID)
	}

	// Fallback: synchronous execution (no streaming)
	result, err := subAgent.Run(ctx, agent.Messages{}.Human(task), subThreadID)
	if err != nil {
		return fmt.Sprintf("Error: sub-agent %q failed: %v", sa.Name, err), nil
	}

	return extractFinalResponse(result, sa.Name)
}

// runSubAgentStreaming runs the sub-agent with RunStream and forwards events to the parent.
func runSubAgentStreaming(
	ctx context.Context,
	subAgent *agent.Agent,
	agentName string,
	task string,
	subThreadID string,
	parentEventCh chan<- agent.StreamEvent,
	parentToolID string,
) (string, error) {
	subCh := make(chan agent.StreamEvent, 64)
	go subAgent.RunStream(ctx, agent.Messages{}.Human(task), subThreadID, subCh)

	var finalContent string

	for evt := range subCh {
		// Map sub-agent events to on_subagent_* events with parent context
		switch evt.Event {
		case "on_chat_model_stream":
			parentEventCh <- agent.StreamEvent{
				Event: "on_subagent_stream",
				Name:  agentName,
				RunID: parentToolID,
				Data:  evt.Data,
			}
			// Accumulate content for the final return value
			if data, ok := evt.Data.(map[string]any); ok {
				if chunk, ok := data["chunk"].(map[string]any); ok {
					if content, ok := chunk["content"].(string); ok {
						finalContent += content
					}
				}
			}

		case "on_tool_start":
			parentEventCh <- agent.StreamEvent{
				Event: "on_subagent_tool_start",
				Name:  evt.Name,
				RunID: parentToolID,
				Data: map[string]any{
					"agent":        agentName,
					"sub_run_id":   evt.RunID,
					"input":        extractInput(evt.Data),
				},
			}

		case "on_tool_end":
			parentEventCh <- agent.StreamEvent{
				Event: "on_subagent_tool_end",
				Name:  evt.Name,
				RunID: parentToolID,
				Data: map[string]any{
					"agent":      agentName,
					"sub_run_id": evt.RunID,
					"output":     extractOutput(evt.Data),
				},
			}

		case "on_chat_model_start":
			parentEventCh <- agent.StreamEvent{
				Event: "on_subagent_model_start",
				Name:  agentName,
				RunID: parentToolID,
			}

		case "on_chat_model_end":
			parentEventCh <- agent.StreamEvent{
				Event: "on_subagent_model_end",
				Name:  agentName,
				RunID: parentToolID,
			}

		case "done":
			parentEventCh <- agent.StreamEvent{
				Event: "on_subagent_done",
				Name:  agentName,
				RunID: parentToolID,
			}

		case "error":
			parentEventCh <- agent.StreamEvent{
				Event: "on_subagent_error",
				Name:  agentName,
				RunID: parentToolID,
				Data:  evt.Data,
			}
		}
	}

	log.Printf("[subagent] %q completed (streaming)", agentName)

	if finalContent != "" {
		return finalContent, nil
	}
	return fmt.Sprintf("Sub-agent %q completed but produced no response.", agentName), nil
}

// extractFinalResponse extracts the last assistant message from an agent result.
func extractFinalResponse(result *agent.AgentState, agentName string) (string, error) {
	for i := len(result.Messages) - 1; i >= 0; i-- {
		if result.Messages[i].Role == "assistant" && result.Messages[i].Content != "" {
			log.Printf("[subagent] %q completed (%d messages)", agentName, len(result.Messages))
			return result.Messages[i].Content, nil
		}
	}
	return fmt.Sprintf("Sub-agent %q completed but produced no response.", agentName), nil
}

// extractInput extracts the "input" field from event data.
func extractInput(data any) any {
	if m, ok := data.(map[string]any); ok {
		return m["input"]
	}
	return nil
}

// extractOutput extracts the "output" field from event data.
func extractOutput(data any) any {
	if m, ok := data.(map[string]any); ok {
		return m["output"]
	}
	return nil
}
