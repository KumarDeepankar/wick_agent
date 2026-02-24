package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"wick_go/llm"
)

// MaxIterations is the maximum number of LLM-tool loop iterations.
const MaxIterations = 25

// Agent is a configured agent instance ready to run.
type Agent struct {
	ID           string
	Config       *AgentConfig
	LLM          llm.Client
	Tools        []Tool
	Hooks        []Hook
	threadStore  *ThreadStore
}

// NewAgent creates a new Agent with the given configuration.
func NewAgent(id string, cfg *AgentConfig, llmClient llm.Client, tools []Tool, hooks []Hook) *Agent {
	return &Agent{
		ID:          id,
		Config:      cfg,
		LLM:         llmClient,
		Tools:       tools,
		Hooks:       hooks,
		threadStore: GlobalThreadStore,
	}
}

// Run executes the agent synchronously and returns the final state.
func (a *Agent) Run(ctx context.Context, messages []Message, threadID string) (*AgentState, error) {
	ch := make(chan StreamEvent, 64)
	var state *AgentState
	var runErr error

	go func() {
		defer close(ch)
		state, runErr = a.runLoop(ctx, messages, threadID, ch)
	}()

	// Drain channel
	for range ch {
	}

	return state, runErr
}

// RunStream executes the agent and streams events to the given channel.
// The caller must read from eventCh until it's closed.
func (a *Agent) RunStream(ctx context.Context, messages []Message, threadID string, eventCh chan<- StreamEvent) {
	defer close(eventCh)

	state, err := a.runLoop(ctx, messages, threadID, eventCh)
	if err != nil {
		eventCh <- StreamEvent{
			Event: "error",
			Data:  map[string]string{"error": err.Error()},
		}
		return
	}

	eventCh <- StreamEvent{
		Event:    "done",
		ThreadID: state.ThreadID,
		Data: map[string]any{
			"thread_id": state.ThreadID,
		},
	}
}

func (a *Agent) runLoop(ctx context.Context, messages []Message, threadID string, eventCh chan<- StreamEvent) (*AgentState, error) {
	startTime := time.Now()

	// Load or create thread state
	state := a.threadStore.LoadOrCreate(threadID)
	state.Messages = append(state.Messages, messages...)

	// Trace recorder (nil-safe — all checks below handle nil)
	tr := TraceFromContext(ctx)

	// 1. BeforeAgent hooks
	for _, hook := range a.Hooks {
		var s SpanHandle
		if tr != nil {
			s = tr.StartSpan("hook.before_agent/" + hook.Name())
		}
		if err := hook.BeforeAgent(ctx, state); err != nil {
			if s != nil {
				s.Set("error", err.Error()).End()
			}
			return nil, fmt.Errorf("hook %s BeforeAgent: %w", hook.Name(), err)
		}
		if s != nil {
			s.End()
		}
	}

	// Build tool map for execution
	toolMap := make(map[string]Tool)
	for _, t := range a.Tools {
		toolMap[t.Name()] = t
	}
	// Also check state-registered tools (from hooks like FilesystemHook)
	if state.toolRegistry != nil {
		for name, t := range state.toolRegistry {
			toolMap[name] = t
		}
	}

	// Build tool schemas for LLM
	toolSchemas := buildToolSchemas(toolMap)

	// Record available tools
	if tr != nil {
		names := make([]string, 0, len(toolMap))
		for name := range toolMap {
			names = append(names, name)
		}
		tr.RecordEvent("tools.available", map[string]any{
			"count": len(names),
			"tools": names,
		})
	}

	// 2. LLM-Tool loop
	for iter := 0; iter < MaxIterations; iter++ {
		select {
		case <-ctx.Done():
			return state, ctx.Err()
		default:
		}

		// Apply ModifyRequest hooks
		msgs := make([]Message, len(state.Messages))
		copy(msgs, state.Messages)
		for _, hook := range a.Hooks {
			before := len(msgs)
			var s SpanHandle
			if tr != nil {
				s = tr.StartSpan("hook.modify_request/" + hook.Name())
				s.Set("iteration", iter)
				s.Set("message_count_before", before)
			}
			var err error
			msgs, err = hook.ModifyRequest(ctx, msgs)
			if err != nil {
				if s != nil {
					s.Set("error", err.Error()).End()
				}
				return nil, fmt.Errorf("hook %s ModifyRequest: %w", hook.Name(), err)
			}
			if s != nil {
				s.Set("message_count_after", len(msgs)).End()
			}
		}

		// Record what will be sent to the LLM
		if tr != nil {
			inputEvent := map[string]any{
				"iteration":     iter,
				"message_count": len(msgs),
			}

			// System prompt (sent separately via req.SystemPrompt, not in messages)
			if a.Config.SystemPrompt != "" {
				sp := a.Config.SystemPrompt
				if len(sp) <= 1000 {
					inputEvent["system_prompt"] = sp
				} else {
					inputEvent["system_prompt"] = sp[:1000] + "...(truncated)"
				}
			}

			msgSummary := make([]map[string]any, len(msgs))
			for i, m := range msgs {
				entry := map[string]any{
					"role":           m.Role,
					"content_length": len(m.Content),
				}
				if len(m.Content) <= 500 {
					entry["content"] = m.Content
				} else {
					entry["content"] = m.Content[:500] + "...(truncated)"
				}
				if len(m.ToolCalls) > 0 {
					tcNames := make([]string, len(m.ToolCalls))
					for j, tc := range m.ToolCalls {
						tcNames[j] = tc.Name
					}
					entry["tool_calls"] = tcNames
				}
				if m.ToolCallID != "" {
					entry["tool_call_id"] = m.ToolCallID
				}
				msgSummary[i] = entry
			}
			inputEvent["messages"] = msgSummary
			tr.RecordEvent("llm.input", inputEvent)
		}

		// Build model call chain (onion ring)
		modelCall := a.buildModelChain(toolSchemas)

		eventCh <- StreamEvent{Event: "on_chat_model_start", Name: a.Config.ModelStr()}

		// Call LLM
		response, err := modelCall(ctx, msgs, eventCh)
		if err != nil {
			return nil, fmt.Errorf("LLM call: %w", err)
		}

		eventCh <- StreamEvent{Event: "on_chat_model_end", Name: a.Config.ModelStr()}

		// Add assistant message
		state.Messages = append(state.Messages, AI(response.Content, response.ToolCalls...))

		// No tool calls → done
		if len(response.ToolCalls) == 0 {
			break
		}

		// Execute tool calls
		var wg sync.WaitGroup
		results := make([]ToolResult, len(response.ToolCalls))

		for i, tc := range response.ToolCalls {
			wg.Add(1)
			go func(idx int, tc ToolCall) {
				defer wg.Done()
				eventCh <- StreamEvent{
					Event: "on_tool_start",
					Name:  tc.Name,
					RunID: tc.ID,
					Data: map[string]any{
						"input": tc.Args,
					},
				}

				// Build tool call chain (onion ring wrapping actual execution)
				toolCallFn := a.buildToolCallChain(toolMap)
				wrapped, err := toolCallFn(ctx, tc)
				var result ToolResult
				if err != nil {
					result = ToolResult{
						ToolCallID: tc.ID,
						Name:       tc.Name,
						Error:      err.Error(),
						Output:     "Error: " + err.Error(),
					}
				} else if wrapped != nil {
					result = *wrapped
				}

				results[idx] = result

				eventCh <- StreamEvent{
					Event: "on_tool_end",
					Name:  tc.Name,
					RunID: tc.ID,
					Data: map[string]any{
						"output": result.Output,
					},
				}
			}(i, tc)
		}
		wg.Wait()

		// Add tool result messages
		for _, result := range results {
			state.Messages = append(state.Messages, ToolMsg(result.ToolCallID, result.Name, result.Output))
		}
	}

	// Update done event with timing
	elapsed := time.Since(startTime).Milliseconds()
	a.threadStore.Save(threadID, state)

	// Update the done event data
	if eventCh != nil {
		// The done event is sent by RunStream after this returns
		state.Files = make(map[string]string) // placeholder
		_ = elapsed // used in done event by caller
	}

	return state, nil
}

func (a *Agent) executeTool(ctx context.Context, tc ToolCall, toolMap map[string]Tool) ToolResult {
	tool, ok := toolMap[tc.Name]
	if !ok {
		return ToolResult{
			ToolCallID: tc.ID,
			Name:       tc.Name,
			Error:      fmt.Sprintf("unknown tool: %s", tc.Name),
			Output:     fmt.Sprintf("Error: tool %q not found", tc.Name),
		}
	}

	output, err := tool.Execute(ctx, tc.Args)
	if err != nil {
		return ToolResult{
			ToolCallID: tc.ID,
			Name:       tc.Name,
			Error:      err.Error(),
			Output:     "Error: " + err.Error(),
		}
	}

	return ToolResult{
		ToolCallID: tc.ID,
		Name:       tc.Name,
		Output:     output,
	}
}

// ModelCallFunc is the type for functions in the model call chain.
type ModelCallFunc func(ctx context.Context, msgs []Message, eventCh chan<- StreamEvent) (*ModelResponse, error)

// ModelResponse holds the result of an LLM call.
type ModelResponse struct {
	Content   string
	ToolCalls []ToolCall
}

func (a *Agent) buildModelChain(toolSchemas []llm.ToolSchema) ModelCallFunc {
	// Base function: call the LLM
	base := func(ctx context.Context, msgs []Message, eventCh chan<- StreamEvent) (*ModelResponse, error) {
		llmMsgs := convertMessages(msgs)
		req := llm.Request{
			Model:       a.Config.ModelStr(),
			Messages:    llmMsgs,
			Tools:       toolSchemas,
			MaxTokens:   4096,
		}

		if a.Config.SystemPrompt != "" {
			req.SystemPrompt = a.Config.SystemPrompt
		}

		// Use streaming — capture errors from the LLM client
		chunkCh := make(chan llm.StreamChunk, 64)
		var llmErr error
		var llmDone sync.WaitGroup
		llmDone.Add(1)
		go func() {
			defer llmDone.Done()
			llmErr = a.LLM.Stream(ctx, req, chunkCh)
		}()

		var content string
		var toolCalls []ToolCall

		for chunk := range chunkCh {
			if chunk.Error != nil {
				return nil, chunk.Error
			}
			if chunk.Delta != "" {
				content += chunk.Delta
				eventCh <- StreamEvent{
					Event: "on_chat_model_stream",
					Name:  a.Config.ModelStr(),
					Data: map[string]any{
						"chunk": map[string]any{
							"content": chunk.Delta,
						},
					},
				}
			}
			if chunk.ToolCall != nil {
				tc := ToolCall{
					ID:   chunk.ToolCall.ID,
					Name: chunk.ToolCall.Name,
					Args: chunk.ToolCall.Args,
				}
				toolCalls = append(toolCalls, tc)
			}
		}

		// Check if the LLM stream returned an error
		llmDone.Wait()
		if llmErr != nil {
			return nil, llmErr
		}

		return &ModelResponse{
			Content:   content,
			ToolCalls: toolCalls,
		}, nil
	}

	// Wrap with hooks (onion ring)
	fn := base
	for i := len(a.Hooks) - 1; i >= 0; i-- {
		hook := a.Hooks[i]
		prev := fn
		fn = func(ctx context.Context, msgs []Message, eventCh chan<- StreamEvent) (*ModelResponse, error) {
			wrapped, err := hook.WrapModelCall(ctx, msgs, func(c context.Context, m []Message) (*llm.Response, error) {
				resp, err := prev(c, m, eventCh)
				if err != nil {
					return nil, err
				}
				// Convert back to llm.Response for the hook
				var llmTC []llm.ToolCallResult
				for _, tc := range resp.ToolCalls {
					llmTC = append(llmTC, llm.ToolCallResult{
						ID:   tc.ID,
						Name: tc.Name,
						Args: tc.Args,
					})
				}
				return &llm.Response{
					Content:   resp.Content,
					ToolCalls: llmTC,
				}, nil
			})
			if err != nil {
				return nil, err
			}
			if wrapped == nil {
				return prev(ctx, msgs, eventCh)
			}
			// Convert llm.Response back to ModelResponse
			var tcs []ToolCall
			for _, tc := range wrapped.ToolCalls {
				tcs = append(tcs, ToolCall{
					ID:   tc.ID,
					Name: tc.Name,
					Args: tc.Args,
				})
			}
			return &ModelResponse{
				Content:   wrapped.Content,
				ToolCalls: tcs,
			}, nil
		}
	}

	return fn
}

// buildToolCallChain builds an onion-ring chain for tool execution,
// wrapping the actual executeTool call with all WrapToolCall hooks.
func (a *Agent) buildToolCallChain(toolMap map[string]Tool) ToolCallFunc {
	// Base: actual tool execution
	base := func(ctx context.Context, tc ToolCall) (*ToolResult, error) {
		r := a.executeTool(ctx, tc, toolMap)
		return &r, nil
	}

	// Wrap with hooks (reverse order so index-0 is outermost)
	fn := base
	for i := len(a.Hooks) - 1; i >= 0; i-- {
		hook := a.Hooks[i]
		prev := fn
		fn = func(ctx context.Context, tc ToolCall) (*ToolResult, error) {
			return hook.WrapToolCall(ctx, tc, prev)
		}
	}
	return fn
}

func convertMessages(msgs []Message) []llm.Message {
	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		out[i] = llm.Message{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
		for _, tc := range m.ToolCalls {
			out[i].ToolCalls = append(out[i].ToolCalls, llm.ToolCallInfo{
				ID:   tc.ID,
				Name: tc.Name,
				Args: tc.Args,
			})
		}
	}
	return out
}

func buildToolSchemas(toolMap map[string]Tool) []llm.ToolSchema {
	schemas := make([]llm.ToolSchema, 0, len(toolMap))
	for _, t := range toolMap {
		schemas = append(schemas, llm.ToolSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		})
	}
	return schemas
}
