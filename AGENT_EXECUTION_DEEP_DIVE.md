# Agent Execution Deep Dive

How an agent is actually executed — from HTTP request to streamed response, with exact line numbers.

---

## 1. The HTTP Request

A browser sends:
```
POST /agents/default/stream
Content-Type: application/json

{
  "messages": [{"role": "user", "content": "Hello"}],
  "thread_id": "thread_abc"
}
```

## 2. Route Resolution (`handlers/handlers.go:61-212`)

`RegisterRoutes()` registers a single catch-all handler for `/agents/`.

```
/agents/default/stream
        ^^^^^^^ ^^^^^^
        agentID  action
```

The path is split at line 151:
```go
parts := strings.SplitN(path, "/", 2)   // ["default", "stream"]
agentID := parts[0]                       // "default"
sub := parts[1]                           // "stream"
```

Then the switch at line 158 dispatches:
```go
case "stream":
    h.stream(w, r, &agentID)       // line 171
case "invoke":
    h.invoke(w, r, &agentID)       // line 169
```

---

## 3. The `stream()` Handler (`handlers/handlers.go:468-597`)

This is the full stream handler, step by step:

### 3a. Parse and validate the request (lines 474-491)
```go
var req invokeRequest
json.NewDecoder(r.Body).Decode(&req)

msgs, err := validateAndConvertMessages(req.Messages)
```
`validateAndConvertMessages()` (line 368) only allows `"user"` and `"system"` roles.
The `"assistant"` and `"tool"` roles are internal — created by the agent loop itself.

### 3b. Resolve the user and agent (lines 480-497)
```go
username := h.resolveUsername(r)           // from auth context, or "local"
resolvedID := "default"                    // fallback
if agentID != nil { resolvedID = *agentID }

inst, err := h.deps.Registry.GetOrClone(resolvedID, username)
```
`GetOrClone` either returns the cached instance for this user, or clones from the template (see registry.go).

### 3c. Create SSE writer (lines 499-504)
```go
sseWriter := sse.NewWriter(w)
```
This sets HTTP headers (`Content-Type: text/event-stream`, `Cache-Control: no-cache`) and grabs the `http.Flusher` interface so each event is pushed immediately.

### 3d. Build the agent — lazy init (line 507)
```go
a, buildErr := h.buildAgent(inst, username)
```
If `inst.Agent` is already built from a previous request, this returns immediately.
Otherwise it builds everything from scratch (see Section 4 below).

### 3e. Set up thread and trace (lines 513-520)
```go
threadID := fmt.Sprintf("%d", time.Now().UnixNano())   // auto-generate
if req.ThreadID != nil { threadID = *req.ThreadID }     // or use provided

trace := tracing.NewTrace(resolvedID, threadID, model, "stream", len(msgs))
ctx := tracing.WithTrace(r.Context(), trace)
```

### 3f. Launch agent in a goroutine (lines 525-526)
```go
eventCh := make(chan agent.StreamEvent, 64)
go a.RunStream(ctx, msgs, threadID, eventCh)
```
This is **the moment execution starts**. The agent runs in a background goroutine,
writing events into `eventCh`. The handler reads from `eventCh` in the main goroutine.

### 3g. Stream events to the browser (lines 528-596)
```go
for evt := range eventCh {
    switch evt.Event {
    case "on_chat_model_stream":
        sseWriter.SendEvent("on_chat_model_stream", {...})
    case "on_tool_start":
        sseWriter.SendEvent("on_tool_start", {...})
    case "on_tool_end":
        sseWriter.SendEvent("on_tool_end", {...})
    case "done":
        trace.Finish(nil)
        sseWriter.SendEvent("done", {trace_id, thread_id, duration})
    case "error":
        trace.Finish(err)
        sseWriter.SendEvent("error", {...})
    }
}
```
This loop runs until `eventCh` is closed (when `RunStream` finishes via `defer close(eventCh)`).

---

## 4. Building the Agent (`handlers/handlers.go:2153-2229`)

`buildAgent()` is the lazy initialization. Called on the first request per user-agent pair.

### 4a. Cache check (line 2154)
```go
if inst.Agent != nil {
    return inst.Agent, nil   // already built, reuse it
}
```

### 4b. Resolve LLM client (line 2161)
```go
llmClient, _, err := llm.Resolve(cfg.Model)
```
`cfg.Model` can be:
- `"ollama:llama3.1:8b"` → creates OpenAI-compatible client pointing at `localhost:11434`
- `map{"provider":"anthropic","model":"claude-sonnet-4-20250514"}` → creates Anthropic client

### 4c. Resolve backend (lines 2167-2182)
```go
b := h.deps.Backends.Get(inst.AgentID, username)
if b == nil && cfg.Backend != nil {
    // Check if another agent already manages this container
    if existing := h.deps.Backends.GetByContainer(containerName); existing != nil {
        b = existing                          // reuse shared container
    } else {
        b = h.createBackend(cfg.Backend, ...) // create new Docker/local backend
    }
    h.deps.Backends.Set(inst.AgentID, username, b)
}
```
Multiple agents can share the same Docker container (e.g., both `default` and `gateway-claude`
use `wick-sandbox-local`).

### 4d. Build hook chain (lines 2184-2218)
Hooks are added in this exact order — **order matters** for the onion ring:
```go
agentHooks = append(agentHooks, tracing.NewTracingHook())        // 1. outermost
agentHooks = append(agentHooks, hooks.NewTodoListHook())         // 2. todo management
if b != nil {
    agentHooks = append(agentHooks, hooks.NewFilesystemHook(b))    // 3. file tools
}
if hasSkills && b != nil {
    agentHooks = append(agentHooks, hooks.NewLazySkillsHook(...)) // 4. lazy skill catalog (default)
}
agentHooks = append(agentHooks, hooks.NewPhasedHook(...))        // 5. phased tool gating
if hasMemory && b != nil {
    agentHooks = append(agentHooks, hooks.NewMemoryHook(...))      // 6. agent memory
}
agentHooks = append(agentHooks, hooks.NewSummarizationHook(...)) // 7. innermost
```
`LazySkillsHook` replaces the eager `SkillsHook` as the default. `PhasedHook` is new — it gates which tools
the LLM can see based on the current execution phase. `createHookByName` handles both `"lazy_skills"` and
`"phased"` names for override configuration. The skills listing endpoint handles both `*SkillsHook` and
`*LazySkillsHook` via type switch.

Then hook overrides are applied if the user has customized them (line 2217).

### 4e. Collect tools and create agent (lines 2221-2228)
```go
var tools []agent.Tool
if h.deps.ExternalTools != nil {
    tools = append(tools, h.deps.ExternalTools.All()...)   // HTTP callback tools
}
a := agent.NewAgent(inst.AgentID, cfg, llmClient, tools, agentHooks)
inst.Agent = a    // cache for future requests
```
Note: `cfg.Tools` (like `["internet_search", "calculate"]`) are app-level tools registered
via `s.RegisterTool()` in `main.go`. They're matched by name later in the handler's tool
resolution, separate from the hook-registered tools.

---

## 5. `RunStream()` — The Entry Point (`agent/loop.go:58-77`)

```go
func (a *Agent) RunStream(ctx, messages, threadID, eventCh) {
    defer close(eventCh)                                    // signals handler to stop reading

    state, err := a.runLoop(ctx, messages, threadID, eventCh)  // THE MAIN LOOP
    if err != nil {
        eventCh <- StreamEvent{Event: "error", ...}
        return
    }
    eventCh <- StreamEvent{Event: "done", ThreadID: state.ThreadID, ...}
}
```

vs `Run()` (line 39) — the synchronous version:
```go
func (a *Agent) Run(ctx, messages, threadID) (*AgentState, error) {
    ch := make(chan StreamEvent, 64)       // throwaway channel
    go func() {
        state, runErr = a.runLoop(...)     // same loop
    }()
    for range ch {}                        // drain events, ignore them
    return state, runErr                   // return final result
}
```
Both call `runLoop()`. The only difference: `RunStream` passes the handler's channel
(events go to the browser), `Run` creates a dummy channel (events are discarded).

---

## 6. `runLoop()` — The Core Agent Loop (`agent/loop.go:79-363`)

This is the heart of the entire system.

### 6a. Load thread state (lines 83-85)
```go
state := a.threadStore.LoadOrCreate(threadID)
state.Messages = append(state.Messages, messages...)
ctx = WithState(ctx, state)
```
If `thread_id` was provided and exists, previous conversation messages are restored.
New user messages are appended. This is how multi-turn conversations work.

### 6b. BeforeAgent hooks (lines 91-105)
```go
for _, hook := range a.Hooks {
    hook.BeforeAgent(ctx, state)
}
```
What each hook does here:
- **FilesystemHook**: registers 7 tools on `state.toolRegistry` (ls, read_file, write_file,
  edit_file, glob, grep, execute)
- **SkillsHook**: scans skill directories for SKILL.md files
- **MemoryHook**: reads AGENTS.md memory files from the workspace
- **TodoListHook**: initializes todo state
- **TracingHook**: no-op (tracing starts later)
- **SummarizationHook**: no-op (runs during ModifyRequest)

### 6c. Main loop — up to 25 iterations (lines 135-349)

```
for iter := 0; iter < 25; iter++ {
```

Each iteration does:

#### Step 1: ModifyRequest hooks (lines 142-165)
```go
systemPrompt := a.Config.SystemPrompt
msgs := copy(state.Messages)

for _, hook := range a.Hooks {
    systemPrompt, msgs, err = hook.ModifyRequest(ctx, systemPrompt, msgs)
}
```
What each hook does:
- **LazySkillsHook**: appends active skill prompt (if any) + "Call list_skills to discover skills. Call activate_skill to load one."
- **PhasedHook**: sets `state.ToolFilter` based on current phase (plan/execute/verify). Auto-transitions phase based on todo state.
- **MemoryHook**: appends agent memory to the system prompt
- **TodoListHook**: injects current todo list into the system prompt
- **SummarizationHook**: if messages exceed 85% of context window, compresses
  older messages into a summary

#### Step 1b: Build tool map (per iteration, after ModifyRequest)
```go
// Agent-level tools (from main.go: calculate, internet_search, etc.)
toolMap := make(map[string]Tool)
for _, t := range a.Tools {
    toolMap[t.Name()] = t
}

// Hook-registered tools (from FilesystemHook: ls, read_file, write_file, etc.)
if state.toolRegistry != nil {
    for name, t := range state.toolRegistry {
        toolMap[name] = t
    }
}

// Apply ToolFilter (set by PhasedHook in ModifyRequest)
if state.ToolFilter != nil {
    for name := range toolMap {
        if !state.ToolFilter[name] {
            delete(toolMap, name)
        }
    }
}

// Convert to JSON Schema for the LLM
toolSchemas := buildToolSchemas(toolMap)
```
The tool map is rebuilt **every iteration** (not once before the loop). This is because
`ModifyRequest` hooks (like `PhasedHook`) can set `state.ToolFilter` to change which tools
are visible to the LLM on each iteration. The LLM only sees schemas for tools that pass the filter.

#### Step 2: Build model call chain — onion ring (line 212)
```go
modelCall := a.buildModelChain(systemPrompt, toolSchemas)
```
This wraps the base LLM call in hook layers (lines 402-525):
```
TracingHook.WrapModelCall(           <- outermost: records timing
  SummarizationHook.WrapModelCall(   <- can retry if context too long
    baseLLMCall()                     <- actual HTTP call to LLM provider
  )
)
```
Hooks wrap in **reverse order** (line 479: `for i := len(a.Hooks) - 1; i >= 0; i--`)
so hook[0] (TracingHook) ends up as the outermost layer.

#### Step 3: Call the LLM (line 217)
```go
eventCh <- StreamEvent{Event: "on_chat_model_start"}
response, err := modelCall(ctx, msgs, eventCh)
eventCh <- StreamEvent{Event: "on_chat_model_end"}
```

Inside `baseLLMCall` (lines 404-475):
```go
// Send request to LLM provider
chunkCh := make(chan llm.StreamChunk, 64)
go func() {
    llmErr = a.LLM.Stream(ctx, req, chunkCh)   // HTTP POST to Ollama/OpenAI/Anthropic
}()

// Read streaming response
for chunk := range chunkCh {
    if chunk.Delta != "" {
        content += chunk.Delta
        eventCh <- StreamEvent{Event: "on_chat_model_stream", Data: {chunk}}
        //         ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
        //         This is what makes tokens appear in the browser in real-time
    }
    if chunk.ToolCall != nil {
        toolCalls = append(toolCalls, chunk.ToolCall)
    }
}

return &ModelResponse{Content: content, ToolCalls: toolCalls}
```

#### Step 4: Append assistant message (line 243)
```go
state.Messages = append(state.Messages, AI(response.Content, response.ToolCalls...))
```

#### Step 5: Check if done (line 246)
```go
if len(response.ToolCalls) == 0 {
    break    // LLM didn't request any tools — conversation turn is complete
}
```
**This is the exit condition.** The loop only continues if the LLM wants to call tools.

#### Step 6: AfterModel hooks (lines 253-279)
```go
intercepted := make(map[string]ToolResult)
for _, hook := range a.Hooks {
    got, err := hook.AfterModel(ctx, state, response.ToolCalls)
    for id, r := range got {
        intercepted[id] = r   // hook pre-built a result, skip execution
    }
}
```
- **TodoListHook**: intercepts duplicate `write_todos` calls. If the LLM calls
  `write_todos` with the same content twice, the hook returns a pre-built
  "already up to date" result instead of executing the tool.

#### Step 7: Execute tools in parallel (lines 282-343)
```go
var wg sync.WaitGroup
results := make([]ToolResult, len(response.ToolCalls))

for i, tc := range response.ToolCalls {
    // Skip intercepted calls
    if ir, ok := intercepted[tc.ID]; ok {
        results[i] = ir
        continue
    }

    wg.Add(1)
    go func(idx int, tc ToolCall) {
        defer wg.Done()

        eventCh <- StreamEvent{Event: "on_tool_start", Name: tc.Name}

        // Build tool call chain (onion ring wrapping)
        toolCallFn := a.buildToolCallChain(toolMap)
        result, err := toolCallFn(ctx, tc)

        results[idx] = result

        eventCh <- StreamEvent{Event: "on_tool_end", Name: tc.Name, Data: result}
    }(i, tc)
}
wg.Wait()
```

Each tool call runs in its **own goroutine** — so if the LLM calls 3 tools at once,
all 3 execute in parallel. `wg.Wait()` blocks until all are done.

The tool call chain (lines 529-546) wraps execution in hooks:
```
TracingHook.WrapToolCall(           <- records timing
  FilesystemHook.WrapToolCall(      <- evicts oversized results (>80k chars)
    executeTool(ctx, tc, toolMap)    <- actually runs the tool
  )
)
```

`executeTool()` (lines 365-391) is simple:
```go
tool, ok := toolMap[tc.Name]         // look up by name
output, err := tool.Execute(ctx, tc.Args)   // call it
return ToolResult{Output: output}
```

#### Step 8: Append tool results (lines 346-348)
```go
for _, result := range results {
    state.Messages = append(state.Messages, ToolMsg(result.ToolCallID, result.Name, result.Output))
}
```
Tool results become messages in the conversation. Then the loop goes back to Step 1 —
the LLM sees the tool results and decides what to do next.

### 6e. Save and return (lines 352-362)
```go
a.threadStore.Save(threadID, state)
return state, nil
```
The thread state (all messages, todos, files) is saved with a 1-hour TTL.
If the user sends another request with the same `thread_id`, the conversation continues.

---

## 7. `invoke()` vs `stream()` — Side by Side

### `invoke()` (`handlers/handlers.go:382-464`)
```go
a, _ := h.buildAgent(inst, username)
state, err := a.Run(ctx, msgs, threadID)       // blocks until done

// Extract last assistant message
response := state.Messages[last].Content

writeJSON(w, 200, {response, thread_id, todos, files})
```
- Synchronous — client waits for the full response
- Returns JSON with the final answer
- No streaming, no SSE
- Uses `a.Run()` which drains events internally

### `stream()` (`handlers/handlers.go:468-597`)
```go
sseWriter := sse.NewWriter(w)
a, _ := h.buildAgent(inst, username)

eventCh := make(chan agent.StreamEvent, 64)
go a.RunStream(ctx, msgs, threadID, eventCh)   // background goroutine

for evt := range eventCh {                     // stream to browser
    sseWriter.SendEvent(evt.Event, evt.Data)
}
```
- Asynchronous — tokens appear in the browser as they're generated
- Uses SSE (Server-Sent Events)
- Handler and agent run in separate goroutines, connected by `eventCh`

---

## 8. The Complete Timeline

```
Browser                    Handler (main goroutine)          Agent (background goroutine)
  |                              |                                    |
  |--- POST /agents/default/stream --------------------------------->|
  |                              |                                    |
  |                    parse JSON body                                |
  |                    validate messages                              |
  |                    resolveUsername() -> "alice"                    |
  |                    GetOrClone("default","alice")                  |
  |                    buildAgent() [if first time]                   |
  |                      - resolve LLM client                        |
  |                      - resolve backend                           |
  |                      - build hook chain                          |
  |                      - collect tools                             |
  |                      - NewAgent()                                |
  |                    create SSE writer                              |
  |                    create trace                                   |
  |                              |                                    |
  |                    eventCh = make(chan, 64)                        |
  |                    go a.RunStream(ctx, msgs, tid, eventCh) ------>|
  |                              |                                    |
  |                              |                          LoadOrCreate(threadID)
  |                              |                          BeforeAgent hooks
  |                              |                                    |
  |                              |                          === ITERATION 0 ===
  |                              |                          ModifyRequest hooks
  |                              |                          build toolMap + apply ToolFilter + schemas
  |                              |                          buildModelChain (onion ring)
  |                              |                                    |
  |                              |                          eventCh <- on_chat_model_start
  |              <-- SSE: on_chat_model_start                         |
  |                              |                                    |
  |                              |                          LLM.Stream() --> POST to Ollama
  |                              |                                    |
  |                              |                          eventCh <- on_chat_model_stream
  |              <-- SSE: "Hel"  |                          eventCh <- on_chat_model_stream
  |              <-- SSE: "lo"   |                          eventCh <- on_chat_model_stream
  |              <-- SSE: "!"    |                                    |
  |                              |                          eventCh <- on_chat_model_end
  |              <-- SSE: on_chat_model_end                           |
  |                              |                                    |
  |                              |                          response has tool_calls?
  |                              |                            YES -> continue loop
  |                              |                            NO  -> break
  |                              |                                    |
  |                              |                    (if tool_calls: YES)
  |                              |                          AfterModel hooks
  |                              |                                    |
  |                              |                          eventCh <- on_tool_start
  |              <-- SSE: on_tool_start (read_file)                   |
  |                              |                          execute tool in goroutine
  |                              |                          eventCh <- on_tool_end
  |              <-- SSE: on_tool_end (result)                        |
  |                              |                                    |
  |                              |                          append tool results to messages
  |                              |                          === ITERATION 1 ===
  |                              |                          (LLM sees tool result, responds)
  |                              |                          ...
  |                              |                                    |
  |                              |                    (when no more tool_calls)
  |                              |                          threadStore.Save(tid, state)
  |                              |                          eventCh <- done
  |              <-- SSE: done (trace_id, thread_id)                  |
  |                              |                          close(eventCh)
  |                              |                                    |
  |                    for range eventCh exits                        |
  |                    HTTP response complete                         |
  |                              |                                    |
```

---

## 9. Key Takeaways

1. **Execution starts at line 526**: `go a.RunStream(ctx, msgs, threadID, eventCh)` — this
   goroutine launch is the exact moment the agent begins working.

2. **The loop is LLM-driven**: The agent doesn't decide what to do. The LLM decides.
   If the LLM returns tool calls, the agent executes them and loops. If not, it stops.

3. **Max 25 iterations**: Safety limit to prevent infinite loops (line 135).

4. **Tools run in parallel**: Multiple tool calls from a single LLM response execute
   concurrently via goroutines + WaitGroup (line 282).

5. **The channel is the bridge**: `eventCh` connects the agent goroutine to the HTTP
   handler. Events flow: agent -> channel -> handler -> SSE -> browser.

6. **Lazy init means first request is slow**: `buildAgent()` creates the LLM client,
   backend, hooks, and tools. Subsequent requests reuse the cached `inst.Agent`.

7. **Thread state enables multi-turn**: Same `thread_id` = same conversation.
   Messages accumulate across requests. ThreadStore has 1h TTL with 5min eviction.

8. **Hooks are pure middleware**: They don't own the loop — they wrap it.
   Remove all hooks and the agent still works (just without files, skills, memory, etc.).
