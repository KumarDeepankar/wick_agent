package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"wick_server/agent"
	"wick_server/backend"
	"wick_server/llm"
	"wick_server/tracing"
)

// ToolLookup resolves tool names to agent.Tool instances.
// Used to give sub-agents access to externally registered tools (e.g. Python HTTP tools).
type ToolLookup func(agentID string) []agent.Tool

// SubAgentHook registers tools that invoke configured sub-agents.
//
// Sub-agents can be exposed in two modes (controlled by SubAgentCfg.Sync /
// SubAgentCfg.Async):
//
//   - Sync — via delegate_to_agent. The supervisor's tool call blocks until
//     the sub-agent completes and returns the final content.
//   - Async — via start_async_task/check_async_task/update_async_task/
//     cancel_async_task/list_async_tasks. The supervisor receives a task_id
//     immediately and can continue while the sub-agent runs in a detached
//     goroutine.
//
// Both modes share the same per-sub-agent configuration and hook chain.
type SubAgentHook struct {
	agent.BaseHook
	subagents  []agent.SubAgentCfg
	parentCfg  *agent.AgentConfig
	backend    backend.Backend
	toolLookup ToolLookup
	taskStore  *agent.AsyncTaskStore
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
		taskStore:  agent.GlobalAsyncTaskStore,
	}
}

func (h *SubAgentHook) Name() string { return "subagent" }

func (h *SubAgentHook) Phases() []string {
	return []string{"before_agent", "modify_request"}
}

// asyncSubAgentGuidance is appended to the system prompt when at least one
// sub-agent is async-capable. Keeps prompt authors mode-agnostic: the main
// system prompt describes the agent's mission; async coordination rules
// appear automatically based on which delegation tools are available.
const asyncSubAgentGuidance = `

## Async sub-agent coordination

Some sub-agents run in the background. start_async_task returns a task_id
immediately — the sub-agent is NOT yet finished when you receive it.

Passive status: a "Current background task status" section is automatically
injected into your prompt every turn with the latest status of every task
for this conversation. Read that section instead of calling list_async_tasks
just to check progress. Only call list_async_tasks when you need a
machine-readable snapshot to iterate over.

Rules:
- Before using a task's result, call check_async_task to fetch the full
  output (the status section tells you it's done, but not its content).
- Before calling a sub-agent whose inputs depend on earlier background tasks
  (for example, a consolidator that reads files written by parallel workers),
  verify every upstream task has reached status=done in the status section.
- Use cancel_async_task to abort; use update_async_task to send new
  instructions to a running task instead of launching a new one.
- Prefer start_async_task for long-running or parallelizable work; prefer
  delegate_to_agent (when available) for short, strictly sequential steps.
- Don't spin-poll. If every active task is still running and you have no
  other work (user chat, another sub-agent to launch), reply briefly to
  the user or wait — don't call list_async_tasks or check_async_task
  repeatedly in tight turns.`

// ModifyRequest appends async coordination guidance and live task status to
// the system prompt whenever this supervisor has at least one async-capable
// sub-agent. Sync-only supervisors get no injection.
//
// The live-status block means the LLM sees every running and recently-finished
// task at every turn without calling list_async_tasks — turning what used to
// be N polling tool calls into zero. The tool still exists for when a full
// JSON snapshot is wanted; it just isn't needed for routine visibility.
func (h *SubAgentHook) ModifyRequest(ctx context.Context, systemPrompt string, msgs []agent.Message) (string, []agent.Message, error) {
	if !h.hasAsyncSubAgent() {
		return systemPrompt, msgs, nil
	}
	out := systemPrompt + asyncSubAgentGuidance
	if state := agent.StateFromContext(ctx); state != nil {
		if block := h.buildTaskStatusBlock(state.ThreadID); block != "" {
			out += block
		}
	}
	return out, msgs, nil
}

// maxStatusBlockEntries caps how many tasks appear in the auto-injected
// status block (per bucket — running and terminal). Keeps the prompt
// bounded even when a thread has accumulated many tasks.
const maxStatusBlockEntries = 10

// buildTaskStatusBlock renders a compact status summary of async tasks for
// the given thread. Returns "" if there are no tasks. Format is tuned for
// LLM consumption: one line per task, agent name and elapsed time for
// running tasks, status for terminal ones.
func (h *SubAgentHook) buildTaskStatusBlock(threadID string) string {
	tasks := h.taskStore.ListByThread(threadID)
	if len(tasks) == 0 {
		return ""
	}

	var running, terminal []*agent.AsyncTask
	for _, t := range tasks {
		if t.IsTerminal() {
			terminal = append(terminal, t)
		} else {
			running = append(running, t)
		}
	}

	var b strings.Builder
	b.WriteString("\n\n## Current background task status\n")
	b.WriteString("Auto-injected every turn. You do NOT need to call list_async_tasks to see status.\n")

	if len(running) > 0 {
		b.WriteString("\nRunning:\n")
		for i, t := range running {
			if i >= maxStatusBlockEntries {
				b.WriteString(fmt.Sprintf("- ... and %d more running\n", len(running)-maxStatusBlockEntries))
				break
			}
			elapsed := time.Since(t.CreatedAt).Round(time.Second)
			fmt.Fprintf(&b, "- %s (%s, running, %s elapsed)\n", t.ID, t.AgentName, elapsed)
		}
	}

	if len(terminal) > 0 {
		b.WriteString("\nRecently finished:\n")
		for i, t := range terminal {
			if i >= maxStatusBlockEntries {
				b.WriteString(fmt.Sprintf("- ... and %d more finished\n", len(terminal)-maxStatusBlockEntries))
				break
			}
			fmt.Fprintf(&b, "- %s (%s, %s)\n", t.ID, t.AgentName, t.Status())
		}
	}

	b.WriteString("\nCall check_async_task for the full output of a finished task. Call update_async_task to steer a running task.")
	return b.String()
}

func (h *SubAgentHook) hasAsyncSubAgent() bool {
	for _, sa := range h.subagents {
		if sa.AsyncEnabled() {
			return true
		}
	}
	return false
}

// BeforeAgent registers the sync and/or async tools based on configured sub-agents.
func (h *SubAgentHook) BeforeAgent(ctx context.Context, state *agent.AgentState) error {
	if len(h.subagents) == 0 {
		return nil
	}

	// Partition sub-agents by mode. One sub-agent can be in both sets.
	syncAgents := make(map[string]agent.SubAgentCfg)
	asyncAgents := make(map[string]agent.SubAgentCfg)
	for _, sa := range h.subagents {
		if sa.SyncEnabled() {
			syncAgents[sa.Name] = sa
		}
		if sa.AsyncEnabled() {
			asyncAgents[sa.Name] = sa
		}
	}

	if len(syncAgents) > 0 {
		h.registerDelegateTool(state, syncAgents)
	}
	if len(asyncAgents) > 0 {
		h.registerAsyncTools(state, asyncAgents)
	}
	return nil
}

// registerDelegateTool installs the synchronous delegate_to_agent tool.
func (h *SubAgentHook) registerDelegateTool(state *agent.AgentState, agents map[string]agent.SubAgentCfg) {
	enumValues := make([]any, 0, len(agents))
	var descParts []string
	for _, sa := range agents {
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

			sa, ok := agents[agentName]
			if !ok {
				return fmt.Sprintf("Error: unknown sub-agent %q", agentName), nil
			}

			parentToolID := agent.ToolCallIDFromContext(ctx)
			parentEventCh := agent.EventChFromContext(ctx)
			return runSubAgent(ctx, sa, task, parentCfg, b, state.ThreadID, toolLookup, parentEventCh, parentToolID)
		},
	})
}

// registerAsyncTools installs the five async lifecycle tools.
// Each tool is scoped to the current thread (state.ThreadID) so that
// list_async_tasks / check_async_task from different conversations don't leak.
func (h *SubAgentHook) registerAsyncTools(state *agent.AgentState, agents map[string]agent.SubAgentCfg) {
	enumValues := make([]any, 0, len(agents))
	var descParts []string
	for _, sa := range agents {
		enumValues = append(enumValues, sa.Name)
		descParts = append(descParts, fmt.Sprintf("%s (%s)", sa.Name, sa.Description))
	}

	parentCfg := h.parentCfg
	b := h.backend
	toolLookup := h.toolLookup
	taskStore := h.taskStore
	threadID := state.ThreadID

	// start_async_task
	agent.RegisterToolOnState(state, &agent.FuncTool{
		ToolName: "start_async_task",
		ToolDesc: fmt.Sprintf(
			"Start a background sub-agent task. Returns a task_id immediately; "+
				"the supervisor continues while the sub-agent runs. Use "+
				"check_async_task/update_async_task/cancel_async_task/list_async_tasks "+
				"to manage it. Available sub-agents: %s.",
			strings.Join(descParts, ", "),
		),
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
			taskDesc, _ := args["task"].(string)
			if agentName == "" || taskDesc == "" {
				return "Error: agent and task are required", nil
			}
			sa, ok := agents[agentName]
			if !ok {
				return fmt.Sprintf("Error: unknown sub-agent %q (or not async-enabled)", agentName), nil
			}

			// Capture the parent event channel while it's still alive, for
			// best-effort forwarding. If the supervisor's turn ends, sends
			// become no-ops (see safeSendEvent).
			parentEventCh := agent.EventChFromContext(ctx)

			task, err := startAsyncSubAgent(sa, taskDesc, parentCfg, b, threadID, toolLookup, taskStore, parentEventCh)
			if err != nil {
				return fmt.Sprintf("Error: %v", err), nil
			}
			return jsonResult(map[string]any{
				"task_id": task.ID,
				"agent":   task.AgentName,
				"status":  string(task.Status()),
			}), nil
		},
	})

	// check_async_task
	agent.RegisterToolOnState(state, &agent.FuncTool{
		ToolName: "check_async_task",
		ToolDesc: "Return the current status and accumulated output of an async sub-agent task.",
		ToolParams: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string", "description": "Task ID returned by start_async_task"},
			},
			"required": []string{"task_id"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			id, _ := args["task_id"].(string)
			t := taskStore.Get(id)
			if t == nil || t.ThreadID != threadID {
				return fmt.Sprintf("Error: unknown task_id %q", id), nil
			}
			return jsonResult(map[string]any{
				"task_id":    t.ID,
				"agent":      t.AgentName,
				"task":       t.Task,
				"status":     string(t.Status()),
				"output":     t.Output(),
				"error":      t.Error(),
				"created_at": t.CreatedAt.Format(time.RFC3339),
				"updated_at": t.UpdatedAt().Format(time.RFC3339),
			}), nil
		},
	})

	// update_async_task
	agent.RegisterToolOnState(state, &agent.FuncTool{
		ToolName: "update_async_task",
		ToolDesc: "Send new instructions to a running async sub-agent task. The update is applied between the sub-agent's LLM turns.",
		ToolParams: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id":      map[string]any{"type": "string", "description": "Task ID"},
				"instructions": map[string]any{"type": "string", "description": "New instructions for the sub-agent"},
			},
			"required": []string{"task_id", "instructions"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			id, _ := args["task_id"].(string)
			instr, _ := args["instructions"].(string)
			t := taskStore.Get(id)
			if t == nil || t.ThreadID != threadID {
				return fmt.Sprintf("Error: unknown task_id %q", id), nil
			}
			if t.IsTerminal() {
				return fmt.Sprintf("Error: task %q is already %s; start a new one", id, t.Status()), nil
			}
			if instr == "" {
				return "Error: instructions are required", nil
			}
			select {
			case t.Updates <- instr:
				return jsonResult(map[string]any{"task_id": id, "accepted": true}), nil
			default:
				return fmt.Sprintf("Error: task %q update mailbox is full; try again shortly", id), nil
			}
		},
	})

	// cancel_async_task
	agent.RegisterToolOnState(state, &agent.FuncTool{
		ToolName: "cancel_async_task",
		ToolDesc: "Cancel a running async sub-agent task. Returns once the sub-agent has observed cancellation (or a short timeout elapses).",
		ToolParams: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{"type": "string", "description": "Task ID"},
			},
			"required": []string{"task_id"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			id, _ := args["task_id"].(string)
			t := taskStore.Get(id)
			if t == nil || t.ThreadID != threadID {
				return fmt.Sprintf("Error: unknown task_id %q", id), nil
			}
			if t.IsTerminal() {
				return jsonResult(map[string]any{"task_id": id, "status": string(t.Status()), "already_terminal": true}), nil
			}
			t.Cancel()
			select {
			case <-t.Done:
			case <-time.After(2 * time.Second):
				// Best-effort — the sub-agent may take longer to unwind.
			}
			return jsonResult(map[string]any{"task_id": id, "status": string(t.Status())}), nil
		},
	})

	// list_async_tasks — minimal fields only. Status is already auto-injected
	// into your context every turn via the "Current background task status"
	// section, so you rarely need this tool. Use it when you want a fresh
	// JSON snapshot to iterate over programmatically.
	agent.RegisterToolOnState(state, &agent.FuncTool{
		ToolName: "list_async_tasks",
		ToolDesc: "Return a JSON snapshot of async tasks for the current conversation (task_id, agent, status, updated_at). Status is already injected into your prompt every turn as 'Current background task status' — only call this when you need a fresh machine-readable list.",
		ToolParams: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			tasks := taskStore.ListByThread(threadID)
			out := make([]map[string]any, 0, len(tasks))
			for _, t := range tasks {
				out = append(out, map[string]any{
					"task_id":    t.ID,
					"agent":      t.AgentName,
					"status":     string(t.Status()),
					"updated_at": t.UpdatedAt().Format(time.RFC3339),
				})
			}
			return jsonResult(map[string]any{"tasks": out, "count": len(out)}), nil
		},
	})
}

// buildSubAgent constructs the *agent.Agent for the given sub-agent config.
// Same hook chain as parent agents — the only thing sub-agents lack is
// sub-agents of their own.
func buildSubAgent(sa agent.SubAgentCfg, parentCfg *agent.AgentConfig, b backend.Backend, toolLookup ToolLookup) (*agent.Agent, error) {
	modelSpec := any(sa.Model)
	if sa.Model == "" {
		modelSpec = parentCfg.Model
	}
	llmClient, _, err := llm.Resolve(modelSpec)
	if err != nil {
		return nil, fmt.Errorf("resolve model for sub-agent %q: %w", sa.Name, err)
	}

	cfg := &agent.AgentConfig{
		Name:         sa.Name,
		Model:        modelSpec,
		SystemPrompt: sa.SystemPrompt,
	}

	var subHooks []agent.Hook

	var maxToolOutputChars int
	if parentCfg.Backend != nil {
		maxToolOutputChars = parentCfg.Backend.MaxToolOutputChars
	}
	subHooks = append(subHooks, NewTruncationHook(maxToolOutputChars))
	subHooks = append(subHooks, tracing.NewTracingHook())
	subHooks = append(subHooks, NewTodoListHook())

	if b != nil {
		subHooks = append(subHooks, NewFilesystemHook(b))
	}
	if parentCfg.Skills != nil && len(parentCfg.Skills.Paths) > 0 && b != nil {
		subSkillsCfg := &agent.SkillsCfg{Paths: parentCfg.Skills.Paths}
		subHooks = append(subHooks, NewLazySkillsHook(b, subSkillsCfg, nil).WithAutoActivate(sa.Name))
	}
	if parentCfg.Memory != nil && len(parentCfg.Memory.Paths) > 0 && b != nil {
		subHooks = append(subHooks, NewMemoryHook(b, parentCfg.Memory.Paths))
	}

	contextWindow := parentCfg.ContextWindow
	if contextWindow <= 0 {
		contextWindow = 128_000
	}
	subHooks = append(subHooks, NewSummarizationHook(llmClient, contextWindow))

	var tools []agent.Tool
	if toolLookup != nil {
		tools = toolLookup(sa.Name)
	}

	return agent.NewAgent(sa.Name, cfg, llmClient, tools, subHooks), nil
}

// runSubAgent builds and executes a sub-agent synchronously (sync / blocking path).
// The parent tool call waits on the sub-agent's final response.
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
	subAgent, err := buildSubAgent(sa, parentCfg, b, toolLookup)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}

	// Include parent tool ID so parallel invocations don't stomp on each other.
	subThreadID := fmt.Sprintf("%s:sub:%s", parentThreadID, sa.Name)
	if parentToolID != "" {
		subThreadID = fmt.Sprintf("%s:sub:%s:%s", parentThreadID, sa.Name, parentToolID)
	}
	log.Printf("[subagent] delegating to %q (thread: %s)", sa.Name, subThreadID)

	if parentEventCh != nil {
		return runSubAgentStreaming(ctx, subAgent, sa.Name, task, subThreadID, parentEventCh, parentToolID)
	}

	result, err := subAgent.Run(ctx, agent.Messages{}.Human(task), subThreadID)
	if err != nil {
		return fmt.Sprintf("Error: sub-agent %q failed: %v", sa.Name, err), nil
	}
	return extractFinalResponse(result, sa.Name)
}

// startAsyncSubAgent creates a detached sub-agent run and returns the task
// handle. The sub-agent's goroutine runs with context.Background() so it
// survives the parent tool call that spawned it.
func startAsyncSubAgent(
	sa agent.SubAgentCfg,
	task string,
	parentCfg *agent.AgentConfig,
	b backend.Backend,
	parentThreadID string,
	toolLookup ToolLookup,
	store *agent.AsyncTaskStore,
	parentEventCh chan<- agent.StreamEvent,
) (*agent.AsyncTask, error) {
	subAgent, err := buildSubAgent(sa, parentCfg, b, toolLookup)
	if err != nil {
		return nil, err
	}

	at := store.Create(parentThreadID, sa.Name, task)
	// Isolated thread — include the task ID so parallel tasks for the same
	// sub-agent get independent message histories.
	subThreadID := fmt.Sprintf("%s:async:%s:%s", parentThreadID, sa.Name, at.ID)

	// Detached context: decoupled from the supervisor tool-call ctx, which
	// gets cancelled the moment the supervisor's tool returns.
	detachedCtx, cancel := context.WithCancel(context.Background())
	store.SetCancel(at.ID, cancel)

	// Emit an immediate "started" event while the parent channel is alive.
	safeSendEvent(parentEventCh, agent.StreamEvent{
		Event:  "on_async_task_started",
		Name:   sa.Name,
		TaskID: at.ID,
		Data:   map[string]any{"task": task, "agent": sa.Name},
	})

	log.Printf("[subagent/async] starting task %s for %q (thread: %s)", at.ID, sa.Name, subThreadID)
	go runAsyncSubAgentDriver(detachedCtx, at, subAgent, sa.Name, task, subThreadID, parentEventCh)
	return at, nil
}

// runAsyncSubAgentDriver is the detached loop that drives a single async task.
// It runs turns until either:
//   - the sub-agent produces a final (tool-call-free) response and the
//     update mailbox is empty → task done
//   - the context is cancelled → task cancelled
//   - the sub-agent returns an error → task error
//
// Updates queued via update_async_task are applied as new human messages
// between turns.
func runAsyncSubAgentDriver(
	ctx context.Context,
	at *agent.AsyncTask,
	subAgent *agent.Agent,
	agentName string,
	initialTask string,
	subThreadID string,
	parentEventCh chan<- agent.StreamEvent,
) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[subagent/async] task %s panic: %v", at.ID, r)
			at.Finish(agent.AsyncTaskError, "", fmt.Sprintf("panic: %v", r))
			safeSendEvent(parentEventCh, agent.StreamEvent{
				Event: "on_async_task_error", Name: agentName, TaskID: at.ID,
				Data: map[string]any{"error": fmt.Sprintf("panic: %v", r)},
			})
		}
	}()

	nextMsgs := agent.Messages{}.Human(initialTask)

	for {
		if ctx.Err() != nil {
			at.Finish(agent.AsyncTaskCancelled, at.Output(), "cancelled")
			safeSendEvent(parentEventCh, agent.StreamEvent{
				Event: "on_async_task_cancelled", Name: agentName, TaskID: at.ID,
			})
			return
		}

		turnOutput, err := runAsyncTurn(ctx, at, subAgent, agentName, nextMsgs, subThreadID, parentEventCh)
		if err != nil {
			if ctx.Err() != nil {
				at.Finish(agent.AsyncTaskCancelled, at.Output(), err.Error())
				safeSendEvent(parentEventCh, agent.StreamEvent{
					Event: "on_async_task_cancelled", Name: agentName, TaskID: at.ID,
				})
				return
			}
			at.Finish(agent.AsyncTaskError, at.Output(), err.Error())
			safeSendEvent(parentEventCh, agent.StreamEvent{
				Event: "on_async_task_error", Name: agentName, TaskID: at.ID,
				Data: map[string]any{"error": err.Error()},
			})
			return
		}

		// Drain any pending updates that arrived during the turn.
		updates := drainUpdates(at)
		if len(updates) == 0 {
			final := at.Output()
			if final == "" {
				final = turnOutput
			}
			at.Finish(agent.AsyncTaskDone, final, "")
			safeSendEvent(parentEventCh, agent.StreamEvent{
				Event: "on_async_task_done", Name: agentName, TaskID: at.ID,
				Data: map[string]any{"output": final},
			})
			return
		}

		// Feed updates as new human messages for the next turn.
		nextMsgs = agent.Messages{}
		for _, u := range updates {
			nextMsgs = nextMsgs.Human(u)
			safeSendEvent(parentEventCh, agent.StreamEvent{
				Event: "on_async_task_updated", Name: agentName, TaskID: at.ID,
				Data: map[string]any{"instructions": u},
			})
		}
	}
}

// runAsyncTurn runs one RunStream cycle of the sub-agent, forwarding streamed
// events to the task's output buffer and (best-effort) to the parent event
// channel. Returns the accumulated assistant content for this turn.
func runAsyncTurn(
	ctx context.Context,
	at *agent.AsyncTask,
	subAgent *agent.Agent,
	agentName string,
	newMsgs []agent.Message,
	subThreadID string,
	parentEventCh chan<- agent.StreamEvent,
) (string, error) {
	subCh := make(chan agent.StreamEvent, 64)
	go subAgent.RunStream(ctx, newMsgs, subThreadID, subCh)

	var turnContent string
	var streamErr error

	for evt := range subCh {
		switch evt.Event {
		case "on_chat_model_stream":
			if data, ok := evt.Data.(map[string]any); ok {
				if chunk, ok := data["chunk"].(map[string]any); ok {
					if s, ok := chunk["content"].(string); ok {
						turnContent += s
						at.AppendOutput(s)
					}
				}
			}
			safeSendEvent(parentEventCh, agent.StreamEvent{
				Event:  "on_async_task_stream",
				Name:   agentName,
				TaskID: at.ID,
				Data:   evt.Data,
			})
		case "on_tool_start", "on_tool_end":
			safeSendEvent(parentEventCh, agent.StreamEvent{
				Event:  "on_async_task_" + strings.TrimPrefix(evt.Event, "on_"),
				Name:   evt.Name,
				TaskID: at.ID,
				Data:   evt.Data,
			})
		case "error":
			if data, ok := evt.Data.(map[string]string); ok {
				streamErr = fmt.Errorf("%s", data["error"])
			} else {
				streamErr = fmt.Errorf("sub-agent error")
			}
		}
	}
	if streamErr != nil {
		return turnContent, streamErr
	}
	if ctx.Err() != nil {
		return turnContent, ctx.Err()
	}
	return turnContent, nil
}

// drainUpdates non-blockingly pulls every queued update off the mailbox.
func drainUpdates(at *agent.AsyncTask) []string {
	var out []string
	for {
		select {
		case u := <-at.Updates:
			out = append(out, u)
		default:
			return out
		}
	}
}

// safeSendEvent sends to a parent event channel that may have been closed by
// the supervisor completing its turn. A panic from sending on a closed channel
// is recovered; a full channel causes the event to be dropped.
func safeSendEvent(ch chan<- agent.StreamEvent, evt agent.StreamEvent) {
	if ch == nil {
		return
	}
	defer func() { _ = recover() }()
	select {
	case ch <- evt:
	default:
		// channel full — drop; task.Output retains the content for polling
	}
}

// jsonResult marshals a result map to a compact JSON string for tool output.
func jsonResult(v map[string]any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("Error: failed to encode result: %v", err)
	}
	return string(b)
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
					"agent":      agentName,
					"sub_run_id": evt.RunID,
					"input":      extractInput(evt.Data),
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

		case "on_llm_input":
			parentEventCh <- agent.StreamEvent{
				Event: "on_subagent_llm_input",
				Name:  agentName,
				RunID: parentToolID,
				Data:  evt.Data,
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
