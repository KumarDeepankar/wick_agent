# Wick Agent — Code Execution Trace

A complete walkthrough of how a new agent is created and executed, file by file.

---

## Project Layout

```
wick_go/                          <- Your application (imports the library)
  main.go                         <- Entry point
  tools.go                        <- Custom tools (calculate, weather, etc.)
  ui/                             <- React frontend (Vite + TypeScript)

wick_deep_agent/server/           <- The core library (package wickserver)
  server.go                       <- Server struct, Start(), routes
  auth.go                         <- Auth middleware
  config.go, config_loader.go     <- Config types, YAML loader

  agent/                          <- Core agent runtime
    config.go                     <- AgentConfig struct
    loop.go                       <- The main agent loop (LLM <-> tool cycle)
    registry.go                   <- Template + per-user instance management
    tool.go                       <- Tool interface, FuncTool, ToolRegistry
    hook.go                       <- Hook interface (5 phases), BaseHook
    state.go                      <- AgentState (messages, todos, files)
    messages.go                   <- Message types, role validation, builders
    events.go                     <- StreamEvent (SSE events to frontend)
    thread.go                     <- ThreadStore (in-memory, 1h TTL)
    http_tool.go                  <- HTTPTool (remote tool calls)
    trace_iface.go                <- Tracing interfaces

  handlers/                       <- HTTP layer
    handlers.go                   <- Routes, invoke/stream endpoints, buildAgent()
    events.go                     <- EventBus (pub/sub for config changes)
    backends.go                   <- BackendStore (per-user container lifecycle)
    tool_store.go                 <- ToolStore (HTTP + native tools)
    builtin_tools.go              <- NewBuiltinTools()
    pptx.go                       <- PPTX export

  llm/                            <- LLM providers
    client.go                     <- Client interface, Request/Response types
    resolver.go                   <- Resolve model string -> Client
    openai.go                     <- OpenAI-compatible (also Ollama, vLLM)
    anthropic.go                  <- Anthropic API
    http_proxy.go                 <- HTTP proxy for external LLMs

  hooks/                          <- Middleware implementations
    filesystem.go                 <- File tools (ls, read, write, edit, glob, grep, execute)
    todolist.go                   <- Todo management tools
    skills.go                     <- SKILL.md discovery, eager catalog injection
    lazy_skills.go                <- LazySkillsHook: 3 meta-tools (list/activate/deactivate), default
    memory.go                     <- AGENTS.md memory loading
    summarization.go              <- Context compression at 85% capacity

  backend/                        <- Command execution environments
    backend.go                    <- Backend interface
    docker.go                     <- Docker container management
    local.go                      <- Local shell execution
    daemon.go                     <- DaemonClient (fast TCP to in-container daemon)

  wickfs/                         <- Filesystem abstraction
    wickfs.go                     <- FileSystem interface
    local.go                      <- LocalFS (direct stdlib)
    remote.go                     <- RemoteFS (via docker exec or daemon)

  tracing/                        <- Request tracing
    trace.go                      <- Trace, Span, Store
    hook.go                       <- TracingHook (wraps LLM/tool calls)

  sse/writer.go                   <- SSE writer (http.Flusher)
  cmd/wick_server/main.go         <- Standalone binary for agents.yaml
  cmd/wickdaemon/main.go          <- In-container daemon (TCP :9090)
  cmd/wickfs/                     <- Filesystem CLI tool
```

---

## Phase 1: Server Startup

**Files:** `wick_go/main.go` -> `wick_deep_agent/server/server.go`

```
main()
  -> wickserver.New(WithPort(8000), WithHost("0.0.0.0"))
  -> s.RegisterAgent("default", AgentConfig{
        Name: "default",
        Model: "ollama:llama3.1:8b",
        SystemPrompt: "You are a helpful assistant...",
        Backend: &BackendCfg{Type: "local"},
    })
  -> registerTools(s)          // from tools.go: adds FuncTools
  -> s.Start()
```

**What `Start()` does** (`server.go`):
1. Creates a `Registry` — stores agent templates and per-user instances
2. Creates a `Deps` struct — shared dependencies (registry, backends, event bus, tool store, trace store)
3. Builds the HTTP router with routes like:
   - `POST /agents/{id}/invoke` — synchronous call
   - `POST /agents/{id}/stream` — SSE streaming
   - `GET /agents/{id}/threads/{tid}` — thread history
   - `POST /agents/` — create agent via API
   - `POST /agents/{id}/container` — manage Docker containers
4. Starts `http.ListenAndServe()`

---

## Phase 2: HTTP Request Arrives

**File:** `handlers/handlers.go`

A user sends:
```
POST /agents/default/stream
{
  "messages": [{"role": "user", "content": "Hello"}],
  "thread_id": "thread_abc"
}
```

**Route resolution in `handlers.go`:**
1. `RegisterRoutes()` registers a catch-all `/agents/` handler
2. Path is parsed: agent ID = `"default"`, action = `"stream"`
3. `h.stream(w, r, &agentID)` is called

**Inside `stream()`:**
1. Parse and validate the request body (only `"user"` and `"system"` roles allowed)
2. Resolve the username via `h.resolveUsername(r)` (auth context)
3. Get or create the agent instance: `registry.GetOrClone("default", username)`
4. Call `h.buildAgent()` if not yet initialized (Phase 3)
5. Create an SSE writer
6. Create a trace: `tracing.NewTrace(agentID, threadID, model, "stream", ...)`
7. Launch `a.RunStream()` in a goroutine (Phase 5)
8. Loop reading events from `eventCh` and writing SSE to the response

---

## Phase 3: Agent Building (Lazy Initialization)

**File:** `handlers/handlers.go` -> `buildAgent()`

This happens on the first request per user-agent pair:

```
buildAgent(inst, username)
  |
  +- 1. Resolve LLM Client
  |     llm.Resolve("ollama:llama3.1:8b")
  |     -> parser sees "ollama:" prefix
  |     -> NewOpenAIClient("http://localhost:11434/v1", "llama3.1:8b")
  |
  +- 2. Resolve Backend
  |     If backend.type == "docker":
  |       -> NewDockerBackend(containerName, "/workspace")
  |       -> LaunchContainerAsync() (non-blocking)
  |     If backend.type == "local":
  |       -> NewLocalBackend(workdir)
  |
  +- 3. Build Hook Chain (middleware pipeline)
  |     hooks := []Hook{
  |       TracingHook,        <- outermost wrapper (timing)
  |       TodoListHook,       <- todo management
  |       FilesystemHook,     <- registers file tools
  |       LazySkillsHook,    <- SKILL.md discovery + 3 meta-tools (default, replaces SkillsHook)
  |       MemoryHook,         <- AGENTS.md loading
  |       SummarizationHook,  <- context compression
  |     }
  |
  +- 4. Collect Tools
  |     Merge: agent-level tools + ToolStore external tools
  |
  +- 5. Create Agent
        agent.NewAgent(id, cfg, llmClient, tools, hooks)
        -> Stored in inst.Agent (cached for reuse)
```

**`llm/resolver.go`** parses the model string:
- `"ollama:model"` -> OpenAI client pointing at `localhost:11434`
- `"openai:gpt-4"` -> OpenAI client with API key
- `map{"provider":"anthropic","model":"claude-sonnet-4-20250514"}` -> Anthropic client

---

## Phase 4: Registry & Instances

**File:** `agent/registry.go`

```
Registry
  +- templates map[string]*Template     <- one per registered agent
  +- instances map[string]*Instance     <- one per "agentID:username"

Template
  +- Config AgentConfig                 <- the prototype

Instance
  +- Config AgentConfig                 <- cloned from template
  +- Agent  *Agent                      <- lazily built (Phase 3)
  +- Overrides HookOverrides           <- per-user customizations
```

- `RegisterTemplate("default", config)` — stores the agent blueprint
- `GetOrClone("default", "alice")` — returns existing instance or clones template for this user
- Key = `"default:alice"` — ensures agents are scoped per-user

---

## Phase 5: The Agent Loop (Heart of the System)

**File:** `agent/loop.go`

Core state machine that loops until no more tool calls (max 25 iterations):

```
RunStream(ctx, messages, threadID, eventCh)
  |
  +- Load or create AgentState from ThreadStore
  |   state.Messages = previous messages + new messages
  |
  +- -- BEFORE_AGENT Phase --
  |   for each hook:
  |     hook.BeforeAgent(ctx, state)
  |     * FilesystemHook: registers file tools onto state.toolRegistry
  |     * LazySkillsHook: scans for SKILL.md files, registers 3 meta-tools (list_skills, activate_skill, deactivate_skill)
  |     * MemoryHook: reads AGENTS.md memory files
  |     * TodoListHook: initializes todo state
  |
  +- == MAIN LOOP (max 25 iterations) ==
     |
     +- -- MODIFY_REQUEST Phase --
     |   for each hook:
     |     systemPrompt, msgs = hook.ModifyRequest(ctx, sysPrompt, msgs)
     |     * LazySkillsHook: appends active skill prompt + "Call list_skills to discover skills."
     |     * MemoryHook: appends memory to system prompt
     |     * SummarizationHook: compresses old messages if >85% context
     |     * TodoListHook: injects current todo state into prompt
     |
     +- -- BUILD TOOL MAP (per iteration, after ModifyRequest) --
     |   toolMap = agent.Tools UNION state.toolRegistry
     |   Build toolSchemas (all tools always available -> sent to LLM)
     |
     +- -- WRAP_MODEL_CALL (onion ring) --
     |   fn = baseLLMCall
     |   for hook in reverse:
     |     fn = hook.WrapModelCall(fn)
     |   * TracingHook wraps outermost -> records timing
     |   * SummarizationHook wraps -> can retry if too long
     |
     +- -- CALL LLM --
     |   response = fn(ctx, messages, eventCh)
     |   -> Streams "on_chat_model_stream" events (token by token)
     |   -> Returns: { Content: "...", ToolCalls: [...] }
     |
     +- Append assistant message to state.Messages
     |
     +- If NO tool calls -> BREAK (done!)
     |
     +- -- AFTER_MODEL Phase --
     |   for each hook:
     |     hook.AfterModel(ctx, state, toolCalls)
     |     * TodoListHook: can intercept/deduplicate todo updates
     |
     +- -- EXECUTE TOOLS (parallel via WaitGroup) --
     |   for each toolCall:
     |     |
     |     +- WRAP_TOOL_CALL (onion ring)
     |     |   fn = executeTool
     |     |   for hook in reverse:
     |     |     fn = hook.WrapToolCall(fn)
     |     |   * TracingHook: records tool timing
     |     |   * FilesystemHook: evicts oversized results (>80k chars)
     |     |
     |     +- Emit "on_tool_start" event
     |     +- result = fn(ctx, toolCall)
     |     +- Emit "on_tool_end" event
     |     |
     |     +- tool.Execute(ctx, args) dispatches to the right tool
     |
     +- Append tool result messages to state.Messages
     |
     +- Loop back to MODIFY_REQUEST (next iteration)

  -- After loop --
  threadStore.Save(threadID, state)   <- persist for future requests
  Emit "done" event with trace_id
```

---

## Phase 6: LLM Call Details

**Files:** `llm/openai.go` or `llm/anthropic.go`

```
client.Stream(ctx, Request{
    Model:        "llama3.1:8b",
    SystemPrompt: "You are...",
    Messages:     [...],
    Tools:        [schema1, schema2, ...],
}, chunkCh)
```

- Builds the provider-specific HTTP request (OpenAI or Anthropic format)
- POSTs to the LLM endpoint with `stream: true`
- Parses the SSE stream from the provider:
  - Content deltas -> `StreamChunk{Delta: "Hello"}` -> forwarded as `on_chat_model_stream`
  - Tool calls -> `StreamChunk{ToolCall: {Name: "read_file", Args: ...}}`
  - Done -> closes channel

---

## Phase 7: Tool Execution

**Native tool** (e.g., `calculate` from `wick_go/tools.go`):
```go
agent.FuncTool{
    Name: "calculate",
    Desc: "Evaluate a math expression",
    Func: func(ctx, args) (string, error) {
        return eval(args["expression"]), nil
    },
}
```

**Filesystem tool** (registered by `hooks/filesystem.go`):
```
write_file(path, content)
  -> backend.UploadFiles(path, content)
  -> If Docker: daemon TCP (~2ms) or docker exec fallback (~60ms)
  -> If Local: os.WriteFile() directly
```

**HTTP tool** (`agent/http_tool.go`):
```
HTTPTool.Execute(ctx, args)
  -> POST to external URL with tool call payload
  -> Returns response body as tool result
```

---

## Phase 8: SSE Response Streaming

**File:** `sse/writer.go`

The event channel emits these events that the frontend consumes:

| Event | When | Data |
|-------|------|------|
| `on_chat_model_stream` | Each LLM token | `{content: "Hel"}` |
| `on_tool_start` | Before tool runs | `{tool: "read_file", args: {...}}` |
| `on_tool_end` | After tool completes | `{tool: "read_file", result: "..."}` |
| `on_progress` | Todo/status updates | `{todos: [...]}` |
| `done` | Agent loop finished | `{trace_id: "...", thread_id: "..."}` |

The frontend (`wick_go/ui/src/hooks/useAgentStream.ts`) parses these via `EventSource` and updates the React UI.

---

## Hook Onion Ring Pattern

This is the key architectural pattern. Hooks wrap calls in layers like an onion:

```
Request -> TracingHook -> SummarizationHook -> [LLM Call] -> SummarizationHook -> TracingHook -> Response
              ^                                                                    ^
          outer layer                                                          outer layer
          (records timing)                                                     (records timing)
```

Each hook implements up to 5 phases:
1. **BeforeAgent** — one-time setup (register tools, load files)
2. **ModifyRequest** — alter system prompt or messages before each LLM call
3. **WrapModelCall** — wrap the LLM call (timing, retry, compression)
4. **AfterModel** — inspect/intercept tool calls after LLM responds
5. **WrapToolCall** — wrap individual tool executions (timing, result filtering)

---

## Thread Persistence

**File:** `agent/thread.go`

- `ThreadStore` is in-memory with a **1-hour TTL**
- Background goroutine evicts expired threads every 5 minutes
- Pass `thread_id` in requests to resume conversations
- `LoadOrCreate(threadID)` -> returns existing `AgentState` or creates a new one
- `Save(threadID, state)` -> persists after each agent run

---

## Key Data Structures & Connections

| Component | File | Purpose | Key Methods |
|-----------|------|---------|------------|
| **Registry** | `agent/registry.go` | Template + per-user agent instances | `RegisterTemplate()`, `GetOrClone()` |
| **Instance** | `agent/registry.go` | User-scoped agent config snapshot | Cached `Agent` for lazy init |
| **Agent** | `agent/loop.go` | Ready-to-run agent with LLM + hooks + tools | `Run()`, `RunStream()` |
| **AgentState** | `agent/state.go` | Thread state (messages, todos, files) | Stored in `ThreadStore`, 1h TTL |
| **Hook** | `agent/hook.go` | Middleware in onion-ring pattern | 5 phases (see above) |
| **Backend** | `backend/backend.go` | Command execution + file I/O | `Execute()`, `FS()` |
| **Trace** | `tracing/trace.go` | Request tracing (spans + events) | `StartSpan()`, `RecordEvent()` |
| **EventBus** | `handlers/events.go` | Pub/sub for config changes | `Broadcast()` |
| **ThreadStore** | `agent/thread.go` | In-memory conversation checkpoints | `LoadOrCreate()`, `Save()` |
| **ToolStore** | `handlers/tool_store.go` | Unified HTTP + native tool registry | `AddTool()`, `GetTools()` |

---

## Configuration Flow

```
agents.yaml (or API POST /agents/)
  |
AgentConfig (model, system_prompt, tools, backend, skills, memory, hooks)
  |
Registry.RegisterTemplate(agentID, config)
  |
On first user request:
  -> Registry.GetOrClone(agentID, username)
  -> Instance created (copy of template config)
  -> buildAgent() lazy-initializes Agent
  |
Agent instance cached in inst.Agent for reuse
```

---

## Summary: The 30-Second Version

1. **`wick_go/main.go`** creates a `Server`, registers agents + tools, calls `Start()`
2. **`server.go`** sets up HTTP routes, creates `Registry` and shared `Deps`
3. **`POST /agents/{id}/stream`** hits `handlers.go` -> parses request, resolves user
4. **`buildAgent()`** lazily initializes: resolves LLM client, backend, hooks, tools -> creates `Agent`
5. **`loop.go`** runs the core cycle: BeforeAgent -> (ModifyRequest -> WrapModelCall -> **LLM Call** -> AfterModel -> **Tool Execution** -> repeat) -> Save thread
6. **SSE events** stream back to the React frontend token-by-token
7. **Hooks** are middleware that modify prompts, wrap calls, register tools, and manage context
