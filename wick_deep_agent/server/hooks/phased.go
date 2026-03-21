package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"wick_server/agent"
	"wick_server/llm"
)

// ToolCategory groups tools by function for phased gating.
type ToolCategory struct {
	Name  string   // e.g. "file_ops", "search", "test", "docker"
	Tools []string // tool names in this category
}

// PhasedOption configures a PhasedHook.
type PhasedOption func(*PhasedHook)

// WithToolCategories sets the tool categories for the phased hook.
func WithToolCategories(cats []ToolCategory) PhasedOption {
	return func(h *PhasedHook) { h.categories = cats }
}

// WithVerifyTools sets the tools available during the verify phase.
func WithVerifyTools(tools []string) PhasedOption {
	return func(h *PhasedHook) { h.verifyTools = tools }
}

// WithToolCatalog sets the short tool catalog injected during planning.
// Each entry is "tool_name: one-line description".
func WithToolCatalog(catalog []string) PhasedOption {
	return func(h *PhasedHook) { h.toolCatalog = catalog }
}

// PhasedHook implements plan → execute → verify phased tool gating.
//
// Plan phase:   Only write_todos + update_todo available. LLM plans tasks
//
//	with optional tool_hint fields. A short tool catalog (names only)
//	is injected so the LLM knows what's available.
//
// Execute phase: For each in_progress todo, only the relevant tools are
//
//	surfaced based on tool_hint → category mapping.
//
// Verify phase:  Only verification tools (run_command, update_todo) available.
//
//	LLM checks results before marking done.
type PhasedHook struct {
	agent.BaseHook
	categories  []ToolCategory
	verifyTools []string
	toolCatalog []string // "tool_name: description" lines for planning

}

// NewPhasedHook creates a phased execution hook.
func NewPhasedHook(opts ...PhasedOption) *PhasedHook {
	h := &PhasedHook{
		verifyTools: []string{"run_command", "update_todo"},
	}
	for _, o := range opts {
		o(h)
	}
	return h
}

func (h *PhasedHook) Name() string { return "phased" }

func (h *PhasedHook) Phases() []string {
	return []string{"before_agent", "modify_request", "after_model"}
}

// BeforeAgent captures the full tool set and sets the initial phase.
func (h *PhasedHook) BeforeAgent(ctx context.Context, state *agent.AgentState) error {
	// Start in plan phase if no todos exist yet
	if len(state.Todos) == 0 {
		state.Phase = agent.PhasePlan
	} else {
		// Resuming with existing todos — go to execute
		state.Phase = agent.PhaseExecute
	}

	log.Printf("[phased] initial phase: %s (todos=%d)", state.Phase, len(state.Todos))
	return nil
}

// ModifyRequest gates which tools the LLM sees based on the current phase,
// and injects phase-specific system prompt sections.
func (h *PhasedHook) ModifyRequest(ctx context.Context, systemPrompt string, msgs []agent.Message) (string, []agent.Message, error) {
	state := agent.StateFromContext(ctx)
	if state == nil {
		return systemPrompt, msgs, nil
	}

	// Detect phase transitions
	h.detectPhaseTransition(state)

	switch state.Phase {
	case agent.PhasePlan:
		systemPrompt += h.planPrompt()
		h.gatePlanTools(state)

	case agent.PhaseExecute:
		systemPrompt += h.executePrompt(state)
		h.gateExecuteTools(state)

	case agent.PhaseVerify:
		systemPrompt += h.verifyPrompt(state)
		h.gateVerifyTools(state)
	}

	return systemPrompt, msgs, nil
}

// AfterModel rejects tool calls that don't belong in the current phase.
func (h *PhasedHook) AfterModel(ctx context.Context, state *agent.AgentState, toolCalls []agent.ToolCall) (map[string]agent.ToolResult, error) {
	if state == nil || state.ToolFilter == nil {
		return nil, nil // no gating active
	}

	intercepted := make(map[string]agent.ToolResult)
	for _, tc := range toolCalls {
		if !state.ToolFilter[tc.Name] {
			intercepted[tc.ID] = agent.ToolResult{
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Error:      "tool not available in current phase",
				Output:     fmt.Sprintf("Error: tool %q is not available in the %s phase. Available tools: %s", tc.Name, state.Phase, h.allowedToolNames(state)),
			}
			log.Printf("[phased] rejected %s in %s phase", tc.Name, state.Phase)
		}
	}

	if len(intercepted) == 0 {
		return nil, nil
	}
	return intercepted, nil
}

func (h *PhasedHook) WrapModelCall(ctx context.Context, msgs []agent.Message, next agent.ModelCallWrapFunc) (*llm.Response, error) {
	return next(ctx, msgs)
}

// ── Phase detection ──

// detectPhaseTransition checks todo state and transitions phases automatically.
func (h *PhasedHook) detectPhaseTransition(state *agent.AgentState) {
	prev := state.Phase

	switch state.Phase {
	case agent.PhasePlan:
		// Transition to execute once todos exist and at least one is in_progress
		if len(state.Todos) > 0 && h.hasStatus(state, "in_progress") {
			state.Phase = agent.PhaseExecute
		}

	case agent.PhaseExecute:
		current := h.currentTodo(state)
		if current == nil {
			// No in_progress todo — check if all done
			if h.allDone(state) {
				state.Phase = agent.PhaseVerify
			} else {
				// Todos exist but none in_progress — stay in execute,
				// the LLM should pick the next one
			}
		}

	case agent.PhaseVerify:
		// If new pending todos appear (LLM revised the plan), go back to execute
		if h.hasStatus(state, "pending") || h.hasStatus(state, "in_progress") {
			state.Phase = agent.PhaseExecute
		}
	}

	if state.Phase != prev {
		log.Printf("[phased] transition: %s → %s", prev, state.Phase)
	}
}

// ── Tool gating via ToolFilter ──

// setFilter sets the tool visibility filter on state. Only tools in the
// filter will be included in toolMap/toolSchemas by the agent loop.
func (h *PhasedHook) setFilter(state *agent.AgentState, allowed map[string]bool) {
	state.ToolFilter = allowed
}

// clearFilter removes the tool filter, making all tools visible.
func (h *PhasedHook) clearFilter(state *agent.AgentState) {
	state.ToolFilter = nil
}

// gatePlanTools restricts tools to only write_todos, update_todo, and skill meta-tools.
func (h *PhasedHook) gatePlanTools(state *agent.AgentState) {
	h.setFilter(state, map[string]bool{
		"write_todos":      true,
		"update_todo":      true,
		"list_skills":      true,
		"activate_skill":   true,
		"deactivate_skill": true,
	})
}

// gateExecuteTools surfaces only tools relevant to the current in_progress todo.
func (h *PhasedHook) gateExecuteTools(state *agent.AgentState) {
	current := h.currentTodo(state)
	if current == nil {
		// No current todo — allow all tools so LLM can pick next task
		h.clearFilter(state)
		return
	}

	// Always allow todo management + skill meta-tools
	allowed := map[string]bool{
		"write_todos":      true,
		"update_todo":      true,
		"list_skills":      true,
		"activate_skill":   true,
		"deactivate_skill": true,
	}

	if current.ToolHint != "" {
		// Direct tool match
		allowed[current.ToolHint] = true

		// Also allow all tools in the same category
		for _, cat := range h.categories {
			for _, toolName := range cat.Tools {
				if toolName == current.ToolHint {
					for _, t := range cat.Tools {
						if t != "" {
							allowed[t] = true
						}
					}
					break
				}
			}
		}

		h.setFilter(state, allowed)
	} else {
		// No tool hint — allow all tools (creative task)
		h.clearFilter(state)
	}
}

// gateVerifyTools surfaces only verification tools.
func (h *PhasedHook) gateVerifyTools(state *agent.AgentState) {
	allowed := make(map[string]bool)
	for _, name := range h.verifyTools {
		allowed[name] = true
	}
	allowed["write_todos"] = true
	allowed["update_todo"] = true

	h.setFilter(state, allowed)
}

// allowedToolNames returns a comma-separated list of allowed tool names.
func (h *PhasedHook) allowedToolNames(state *agent.AgentState) string {
	if state.ToolFilter == nil {
		return "(all)"
	}
	names := make([]string, 0, len(state.ToolFilter))
	for name := range state.ToolFilter {
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// ── System prompts ──

func (h *PhasedHook) planPrompt() string {
	var sb strings.Builder
	sb.WriteString("\n\n[PLAN phase] Use write_todos to plan. Include tool_hint per task. Mark first task in_progress to start.")

	if len(h.toolCatalog) > 0 {
		sb.WriteString("\nTools: ")
		for i, entry := range h.toolCatalog {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(entry)
		}
	}

	return sb.String()
}

func (h *PhasedHook) executePrompt(state *agent.AgentState) string {
	current := h.currentTodo(state)
	if current != nil {
		hint := ""
		if current.ToolHint != "" {
			hint = fmt.Sprintf(" (tool: %s)", current.ToolHint)
		}
		return fmt.Sprintf("\n\n[EXECUTE phase] Task: %s — %s%s", current.ID, current.Title, hint)
	}
	return "\n\n[EXECUTE phase] No task in_progress. Use update_todo to start next."
}

func (h *PhasedHook) verifyPrompt(state *agent.AgentState) string {
	return "\n\n[VERIFY phase] All tasks done. Verify results, then respond to the user."
}

// ── Todo helpers ──

func (h *PhasedHook) currentTodo(state *agent.AgentState) *agent.Todo {
	for i := range state.Todos {
		if state.Todos[i].Status == "in_progress" {
			return &state.Todos[i]
		}
	}
	return nil
}

func (h *PhasedHook) hasStatus(state *agent.AgentState, status string) bool {
	for _, t := range state.Todos {
		if t.Status == status {
			return true
		}
	}
	return false
}

func (h *PhasedHook) allDone(state *agent.AgentState) bool {
	if len(state.Todos) == 0 {
		return false
	}
	for _, t := range state.Todos {
		if t.Status != "done" {
			return false
		}
	}
	return true
}

// ── Extended write_todos schema ──

// PhasedTodoToolDescription is a compact write_todos description with tool_hint support.
var PhasedTodoToolDescription = `Replace the full todo list. Each item: id, title, status (pending|in_progress|done), optional tool_hint.`

// PhasedTodoParams returns the JSON Schema for write_todos with tool_hint support.
func PhasedTodoParams() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"todos": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":        map[string]any{"type": "string"},
						"title":     map[string]any{"type": "string"},
						"status":    map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "done"}},
						"tool_hint": map[string]any{"type": "string", "description": "Name of the primary tool needed for this task (optional)"},
					},
				},
			},
		},
		"required": []string{"todos"},
	}
}

// PhasedWriteTodosFn returns a write_todos handler that captures tool_hint.
func PhasedWriteTodosFn(state *agent.AgentState) func(ctx context.Context, args map[string]any) (string, error) {
	return func(ctx context.Context, args map[string]any) (string, error) {
		todosRaw, ok := args["todos"]
		if !ok {
			return "Error: 'todos' field is required", nil
		}

		data, _ := json.Marshal(todosRaw)
		var todos []agent.Todo
		if err := json.Unmarshal(data, &todos); err != nil {
			return "Error parsing todos: " + err.Error(), nil
		}

		state.Todos = todos

		out, _ := json.Marshal(todos)
		return fmt.Sprintf("Updated todo list to %s", string(out)), nil
	}
}
