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
var defaultTodoSystemPrompt = `## write_todos

You have access to the write_todos tool to help you manage and plan complex objectives.
Use this tool for complex objectives to ensure that you are tracking each necessary step and giving the user visibility into your progress.
This tool is very helpful for planning complex objectives, and for breaking down these larger complex objectives into smaller steps.

It is critical that you mark todos as completed as soon as you are done with a step. Do not batch up multiple steps before marking them as completed.
For simple objectives that only require a few steps, it is better to just complete the objective directly and NOT use this tool.
Writing todos takes time and tokens, use it when it is helpful for managing complex many-step problems! But not for simple few-step requests.

## Important To-Do List Usage Notes to Remember
- The write_todos tool should never be called multiple times in parallel.
- Don't be afraid to revise the To-Do list as you go. New information may reveal new tasks that need to be done, or old tasks that are irrelevant.

## update_todo — single-task status changes

You also have an update_todo tool. Use it instead of write_todos when you only need to change one task's status (e.g. marking a task done or starting the next task). This saves tokens because you don't need to re-send the entire list.

- Use update_todo when: you finished a single task and just need to flip its status.
- Use write_todos when: you need to add new tasks, remove tasks, reorder, or update multiple tasks at once.
- Never call update_todo and write_todos in the same turn.`

// Tool description for write_todos.
var defaultTodoToolDescription = `Use this tool to create and manage a structured task list for your current work session. This helps you track progress, organize complex tasks, and demonstrate thoroughness to the user.

Only use this tool if you think it will be helpful in staying organized. If the user's request is trivial and takes less than 3 steps, it is better to NOT use this tool and just do the task directly.

## When to Use This Tool
Use this tool in these scenarios:

1. Complex multi-step tasks - When a task requires 3 or more distinct steps or actions
2. Non-trivial and complex tasks - Tasks that require careful planning or multiple operations
3. User explicitly requests todo list - When the user directly asks you to use the todo list
4. User provides multiple tasks - When users provide a list of things to be done (numbered or comma-separated)
5. The plan may need future revisions or updates based on results from the first few steps

## How to Use This Tool
1. When you start working on a task - Mark it as in_progress BEFORE beginning work.
2. After completing a task - Mark it as done and add any new follow-up tasks discovered during implementation.
3. You can also update future tasks, such as deleting them if they are no longer necessary, or adding new tasks that are necessary. Don't change previously completed tasks.
4. You can make several updates to the todo list at once. For example, when you complete a task, you can mark the next task you need to start as in_progress.

## When NOT to Use This Tool
It is important to skip using this tool when:
1. There is only a single, straightforward task
2. The task is trivial and tracking it provides no benefit
3. The task can be completed in less than 3 trivial steps
4. The task is purely conversational or informational

## Task States and Management

1. Task States: Use these states to track progress:
   - pending: Task not yet started
   - in_progress: Currently working on (you can have multiple tasks in_progress at a time if they are not related to each other and can be run in parallel)
   - done: Task finished successfully

2. Task Management:
   - Update task status in real-time as you work
   - Mark tasks done IMMEDIATELY after finishing (don't batch completions)
   - Complete current tasks before starting new ones
   - Remove tasks that are no longer relevant from the list entirely
   - IMPORTANT: When you write this todo list, you should mark your first task (or tasks) as in_progress immediately!
   - IMPORTANT: Unless all tasks are done, you should always have at least one task in_progress to show the user that you are working on something.

3. Task Completion Requirements:
   - ONLY mark a task as done when you have FULLY accomplished it
   - If you encounter errors, blockers, or cannot finish, keep the task as in_progress
   - When blocked, create a new task describing what needs to be resolved
   - Never mark a task as done if:
     - There are unresolved issues or errors
     - Work is partial or incomplete
     - You encountered blockers that prevent completion

4. Task Breakdown:
   - Create specific, actionable items
   - Break complex tasks into smaller, manageable steps
   - Use clear, descriptive task names

Remember: If you only need to make a few tool calls to complete a task, and it is clear what you need to do, it is better to just do the task directly and NOT call this tool at all.`

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
