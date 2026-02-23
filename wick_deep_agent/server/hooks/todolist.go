package hooks

import (
	"context"
	"encoding/json"
	"fmt"

	"wick_go/agent"
	"wick_go/llm"
)

// TodoListHook tracks task progress via a write_todos tool.
type TodoListHook struct {
	agent.BaseHook
}

// NewTodoListHook creates a todo list hook.
func NewTodoListHook() *TodoListHook {
	return &TodoListHook{}
}

func (h *TodoListHook) Name() string { return "todolist" }

// BeforeAgent initializes the todo state and registers the write_todos tool.
func (h *TodoListHook) BeforeAgent(ctx context.Context, state *agent.AgentState) error {
	if state.Todos == nil {
		state.Todos = []agent.Todo{}
	}

	agent.RegisterToolOnState(state, &agent.FuncTool{
		ToolName: "write_todos",
		ToolDesc: "Update the task tracking list. Pass the complete list of todos with their current status.",
		ToolParams: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"todos": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id":     map[string]any{"type": "string"},
							"title":  map[string]any{"type": "string"},
							"status": map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "done"}},
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

			// Convert via JSON round-trip for type safety
			data, _ := json.Marshal(todosRaw)
			var todos []agent.Todo
			if err := json.Unmarshal(data, &todos); err != nil {
				return "Error parsing todos: " + err.Error(), nil
			}

			state.Todos = todos
			return fmt.Sprintf("Updated %d todo(s)", len(todos)), nil
		},
	})

	return nil
}

func (h *TodoListHook) ModifyRequest(ctx context.Context, msgs []agent.Message) ([]agent.Message, error) {
	return msgs, nil
}

func (h *TodoListHook) WrapModelCall(ctx context.Context, msgs []agent.Message, next agent.ModelCallWrapFunc) (*llm.Response, error) {
	return next(ctx, msgs)
}

func (h *TodoListHook) WrapToolCall(ctx context.Context, call agent.ToolCall, next agent.ToolCallFunc) (*agent.ToolResult, error) {
	return next(ctx, call)
}
