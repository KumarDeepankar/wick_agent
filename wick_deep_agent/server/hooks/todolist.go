package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"wick_server/agent"
	"wick_server/llm"
)

// System prompt injected on every LLM call to guide todo usage.
var defaultTodoSystemPrompt = `For multi-step tasks, use write_todos to plan and track progress. Use update_todo to change one task's status. Mark done immediately. For simple questions, respond directly.`

// Tool description for write_todos.
var defaultTodoToolDescription = `Replace the full todo list. Each item: id, title, status (pending|in_progress|done).`

// TodoListOption configures a TodoListHook.
type TodoListOption func(*TodoListHook)

// WithTodoSystemPrompt sets a custom system prompt for todo guidance.
func WithTodoSystemPrompt(prompt string) TodoListOption {
	return func(h *TodoListHook) { h.systemPrompt = prompt }
}

// WithTodoToolDescription sets a custom description for the write_todos tool.
func WithTodoToolDescription(desc string) TodoListOption {
	return func(h *TodoListHook) { h.toolDescription = desc }
}

// todoTools is the set of tool names managed by this hook.
var todoTools = map[string]bool{"write_todos": true, "update_todo": true}

// TodoListHook tracks task progress via write_todos and update_todo tools.
// Uses AfterModel to reject conflicting parallel calls before dispatch.
// Injects current task progress into the system prompt on every LLM call.
type TodoListHook struct {
	agent.BaseHook
	systemPrompt    string
	toolDescription string
}

// NewTodoListHook creates a todo list hook with optional configuration.
func NewTodoListHook(opts ...TodoListOption) *TodoListHook {
	h := &TodoListHook{
		systemPrompt:    defaultTodoSystemPrompt,
		toolDescription: defaultTodoToolDescription,
	}
	for _, o := range opts {
		o(h)
	}
	return h
}

func (h *TodoListHook) Name() string { return "todolist" }

func (h *TodoListHook) Phases() []string {
	return []string{"before_agent", "modify_request", "after_model"}
}

// BeforeAgent initializes the todo state and registers tools.
func (h *TodoListHook) BeforeAgent(ctx context.Context, state *agent.AgentState) error {
	if state.Todos == nil {
		state.Todos = []agent.Todo{}
	}

	// write_todos — replaces the entire todo list
	agent.RegisterToolOnState(state, &agent.FuncTool{
		ToolName:   "write_todos",
		ToolDesc:   h.toolDescription,
		ToolParams: map[string]any{
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
						},
					},
				},
			},
			"required": []string{"todos"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
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
		},
	})

	// update_todo — single-task status change (saves tokens)
	agent.RegisterToolOnState(state, &agent.FuncTool{
		ToolName: "update_todo",
		ToolDesc: "Update a single todo item's status by ID. Use this instead of write_todos when you only need to change one task's status.",
		ToolParams: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":     map[string]any{"type": "string", "description": "ID of the todo to update"},
				"status": map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "done"}, "description": "New status"},
			},
			"required": []string{"id", "status"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			id, _ := args["id"].(string)
			newStatus, _ := args["status"].(string)
			if id == "" || newStatus == "" {
				return "Error: 'id' and 'status' are required", nil
			}

			for i := range state.Todos {
				if state.Todos[i].ID == id {
					state.Todos[i].Status = newStatus
					out, _ := json.Marshal(state.Todos)
					return fmt.Sprintf("Updated todo list to %s", string(out)), nil
				}
			}

			return fmt.Sprintf("Error: todo with id %q not found", id), nil
		},
	})

	return nil
}

// ModifyRequest injects the todo system prompt and current task progress.
func (h *TodoListHook) ModifyRequest(ctx context.Context, systemPrompt string, msgs []agent.Message) (string, []agent.Message, error) {
	systemPrompt += "\n\n" + h.systemPrompt

	// Inject current task progress so the LLM always sees its plan
	state := agent.StateFromContext(ctx)
	if state != nil && len(state.Todos) > 0 {
		var lines []string
		for _, t := range state.Todos {
			lines = append(lines, fmt.Sprintf("- [%s] %s: %s", t.Status, t.ID, t.Title))
		}
		systemPrompt += "\n\n## Current Task Progress\n" + strings.Join(lines, "\n")
	}

	return systemPrompt, msgs, nil
}

func (h *TodoListHook) WrapModelCall(ctx context.Context, msgs []agent.Message, next agent.ModelCallWrapFunc) (*llm.Response, error) {
	return next(ctx, msgs)
}

// AfterModel rejects conflicting todo tool calls before dispatch.
//
// Rules:
//   - 2+ write_todos → reject all (ambiguous which list wins)
//   - 2+ update_todo → reject all (would race on state.Todos)
//   - write_todos + update_todo → reject update_todo (redundant)
func (h *TodoListHook) AfterModel(ctx context.Context, state *agent.AgentState, toolCalls []agent.ToolCall) (map[string]agent.ToolResult, error) {
	var writeCalls, updateCalls []agent.ToolCall
	for _, tc := range toolCalls {
		switch tc.Name {
		case "write_todos":
			writeCalls = append(writeCalls, tc)
		case "update_todo":
			updateCalls = append(updateCalls, tc)
		}
	}

	intercepted := make(map[string]agent.ToolResult)

	if len(writeCalls) > 1 {
		for _, tc := range writeCalls {
			intercepted[tc.ID] = agent.ToolResult{
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Error:      "parallel write_todos rejected",
				Output:     "Error: The write_todos tool should never be called multiple times in parallel. Please call it only once per model invocation.",
			}
		}
	}

	if len(updateCalls) > 1 {
		for _, tc := range updateCalls {
			intercepted[tc.ID] = agent.ToolResult{
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Error:      "parallel update_todo rejected",
				Output:     "Error: The update_todo tool should never be called multiple times in parallel. Use write_todos to update multiple tasks at once.",
			}
		}
	}

	if len(writeCalls) == 1 && len(updateCalls) > 0 {
		for _, tc := range updateCalls {
			intercepted[tc.ID] = agent.ToolResult{
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Error:      "update_todo rejected (write_todos in same turn)",
				Output:     "Error: write_todos and update_todo should not be called in the same turn. write_todos replaces the entire list.",
			}
		}
	}

	if len(intercepted) == 0 {
		return nil, nil
	}
	return intercepted, nil
}
