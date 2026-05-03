package hooks

import (
	"context"
	"encoding/json"
	"time"

	"wick_server/agent"
)

// hitlGuidance is appended to the supervisor's system prompt. It tells the
// LLM when to use HITL tools versus a plain text question — the latter
// exits the loop and forces the user to nudge the agent back to resume.
const hitlGuidance = `

## Human-in-the-loop

You have two tools for asking the user something MID-TURN without ending the
loop:

- request_user_input(prompt) — block until the user types free-form text. Use
  this whenever you need a value, choice, or clarification you cannot
  reasonably infer or default. The tool returns the user's text.
- request_user_approval(prompt, options=["Approve","Deny"]) — block until the
  user picks one option. Use for sensitive or destructive actions that need
  explicit human authorization (deletes, payments, irreversible operations).

Critical: NEVER ask a question via plain assistant text. A tool-less reply
exits the turn, so the answer cannot reach you until the user manually nudges
the agent to resume. Always prefer the HITL tools for in-flight questions.

Use sparingly: don't gate read-only or already-authorized work on approval, and
don't ask for input you can default sensibly.`

// HITLHook exposes request_user_input and request_user_approval as blocking
// tools. Each tool creates a HITLRequest, emits an SSE event so the UI can
// render the prompt, then blocks until the UI POSTs a response (resolves the
// request) or the deadline / request context fires.
type HITLHook struct {
	agent.BaseHook
	store *agent.HITLStore
}

// NewHITLHook returns a hook backed by GlobalHITLStore. The store is shared
// with the HTTP handler so POST /agents/hitl/<id>/respond can resolve the
// pending request the tool is blocked on.
func NewHITLHook() *HITLHook {
	return &HITLHook{store: agent.GlobalHITLStore}
}

func (h *HITLHook) Name() string { return "hitl" }

func (h *HITLHook) Phases() []string {
	return []string{"before_agent", "modify_request"}
}

// ModifyRequest appends the HITL guidance to the supervisor's system prompt
// every turn so the LLM sees the rule "never ask via assistant text".
func (h *HITLHook) ModifyRequest(ctx context.Context, systemPrompt string, msgs []agent.Message) (string, []agent.Message, error) {
	return systemPrompt + hitlGuidance, msgs, nil
}

// BeforeAgent registers request_user_input and request_user_approval on the
// supervisor's tool registry.
func (h *HITLHook) BeforeAgent(ctx context.Context, state *agent.AgentState) error {
	threadID := state.ThreadID
	store := h.store

	agent.RegisterToolOnState(state, &agent.FuncTool{
		ToolName: "request_user_input",
		ToolDesc: "Ask the user a question and BLOCK until they reply via the UI. Returns the user's free-form text answer. Use this whenever you would otherwise ask a question in plain assistant text and end the turn — this keeps the loop alive so your next step runs immediately on the answer.",
		ToolParams: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":        "string",
					"description": "The question or instruction to show the user.",
				},
				"max_wait_seconds": map[string]any{
					"type":        "integer",
					"description": "Maximum seconds to block before timing out. Default 1800 (30 min), capped at 86400 (24 h).",
				},
			},
			"required": []string{"prompt"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			prompt, _ := args["prompt"].(string)
			if prompt == "" {
				return "Error: 'prompt' is required", nil
			}
			return runHITLWait(ctx, store, threadID, agent.HITLInput, prompt, nil, args)
		},
	})

	agent.RegisterToolOnState(state, &agent.FuncTool{
		ToolName: "request_user_approval",
		ToolDesc: "Ask the user to choose one option (typically Approve / Deny) and BLOCK until they reply via the UI. Returns the chosen option string. Use this for sensitive or destructive actions that need explicit human authorization.",
		ToolParams: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":        "string",
					"description": "What the user should approve or choose between.",
				},
				"options": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Options the user can pick from. Defaults to ['Approve','Deny']. Use custom labels for multi-way choices.",
				},
				"max_wait_seconds": map[string]any{
					"type":        "integer",
					"description": "Maximum seconds to block before timing out. Default 1800 (30 min), capped at 86400 (24 h).",
				},
			},
			"required": []string{"prompt"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			prompt, _ := args["prompt"].(string)
			if prompt == "" {
				return "Error: 'prompt' is required", nil
			}

			options := []string{"Approve", "Deny"}
			if rawOpts, ok := args["options"]; ok {
				data, _ := json.Marshal(rawOpts)
				var parsed []string
				if err := json.Unmarshal(data, &parsed); err == nil && len(parsed) > 0 {
					options = parsed
				}
			}

			return runHITLWait(ctx, store, threadID, agent.HITLApproval, prompt, options, args)
		},
	})

	return nil
}

// runHITLWait creates a pending request, emits the SSE prompt event, then
// blocks on (response | deadline | ctx-cancel | heartbeat). The heartbeat
// emits a keep-alive event every 60s so the SSE connection survives the
// 120s IdleTimeout in app.go even when the user takes minutes to respond.
func runHITLWait(
	ctx context.Context,
	store *agent.HITLStore,
	threadID string,
	kind agent.HITLKind,
	prompt string,
	options []string,
	args map[string]any,
) (string, error) {
	maxWait := 1800
	if v, ok := args["max_wait_seconds"]; ok {
		switch n := v.(type) {
		case float64:
			maxWait = int(n)
		case int:
			maxWait = n
		}
	}
	if maxWait <= 0 {
		maxWait = 1800
	}
	if maxWait > 86400 {
		maxWait = 86400
	}

	req := store.Create(threadID, kind, prompt, options)

	eventCh := agent.EventChFromContext(ctx)
	if eventCh != nil {
		safeSendEvent(eventCh, agent.StreamEvent{
			Event: "on_hitl_request",
			Name:  string(kind),
			RunID: req.ID,
			Data: map[string]any{
				"id":      req.ID,
				"kind":    string(kind),
				"prompt":  prompt,
				"options": options,
			},
		})
	}

	deadline := time.NewTimer(time.Duration(maxWait) * time.Second)
	defer deadline.Stop()

	heartbeat := time.NewTicker(60 * time.Second)
	defer heartbeat.Stop()

	var status agent.HITLStatus
waitLoop:
	for {
		select {
		case <-req.Done:
			status = req.Status()
			break waitLoop
		case <-deadline.C:
			req.Resolve(agent.HITLTimedOut, "", nil)
			status = agent.HITLTimedOut
			break waitLoop
		case <-ctx.Done():
			req.Resolve(agent.HITLCancelled, "", nil)
			status = agent.HITLCancelled
			break waitLoop
		case <-heartbeat.C:
			if eventCh != nil {
				safeSendEvent(eventCh, agent.StreamEvent{
					Event: "on_hitl_heartbeat",
					RunID: req.ID,
					Data: map[string]any{
						"id":              req.ID,
						"elapsed_seconds": int(time.Since(req.CreatedAt).Seconds()),
					},
				})
			}
		}
	}

	if eventCh != nil {
		safeSendEvent(eventCh, agent.StreamEvent{
			Event: "on_hitl_response",
			RunID: req.ID,
			Data: map[string]any{
				"id":       req.ID,
				"status":   string(status),
				"response": req.Response(),
			},
		})
	}

	result := map[string]any{
		"id":       req.ID,
		"kind":     string(kind),
		"status":   string(status),
		"response": req.Response(),
	}
	switch status {
	case agent.HITLAnswered, agent.HITLDenied:
		result["answer"] = req.Response()
	case agent.HITLTimedOut:
		result["hint"] = "User did not respond before max_wait_seconds elapsed. Decide whether to proceed with a safe default, ask again, or stop."
	case agent.HITLCancelled:
		result["hint"] = "Request was cancelled (likely the user closed the conversation)."
	}
	return jsonResult(result), nil
}
