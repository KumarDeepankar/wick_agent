# Agent Loop — Complete Execution Flow

This document traces every line of execution in `runLoop()` (`agent/loop.go`), including all data types, SSE events, hook invocations, onion ring construction/execution, LLM streaming, tool execution, and thread persistence.

---

## Entry Points

There are two ways to start the agent loop:

```
┌─── Run() — Synchronous ──────────────────────────────────────────────┐
│                                                                       │
│  ch = make(chan StreamEvent, 64)                                       │
│  goroutine: state, runErr = a.runLoop(ctx, messages, threadID, ch)    │
│  main: drain ch (discard all events)                                  │
│  return state, runErr                                                 │
│                                                                       │
└───────────────────────────────────────────────────────────────────────┘

┌─── RunStream() — Streaming (SSE) ────────────────────────────────────┐
│                                                                       │
│  defer close(eventCh)                                                 │
│  state, err = a.runLoop(ctx, messages, threadID, eventCh)             │
│                                                                       │
│  IF err:                                                              │
│    eventCh ← StreamEvent{Event:"error", Data:{error: err.Error()}}   │
│  ELSE:                                                                │
│    eventCh ← StreamEvent{Event:"done", ThreadID, Data:{thread_id}}   │
│                                                                       │
└───────────────────────────────────────────────────────────────────────┘
```

---

## Core Data Types

```
Agent {                              AgentState {
  ID           string                  ThreadID     string
  Config       *AgentConfig            Messages     []Message
  LLM          llm.Client              Todos        []Todo          // managed by TodoListHook
  Tools        []Tool                  Files        map[string]string // path→content (tracked writes)
  Hooks        []Hook                  toolRegistry map[string]Tool  // runtime-registered by hooks, not serialized
  threadStore  *ThreadStore          }
}
                                     Message {
Tool interface {                       Role       string     // "system"|"user"|"assistant"|"tool"
  Name() string                        Content    string
  Description() string                 ToolCalls  []ToolCall // set when Role=="assistant"
  Parameters() map[string]any          ToolCallID string     // set when Role=="tool"
  Execute(ctx, args) (string, error)   Name       string     // tool name when Role=="tool"
}                                    }

FuncTool {                           ToolCall {
  ToolName   string                    ID       string
  ToolDesc   string                    Name     string
  ToolParams map[string]any            Args     map[string]any
  Fn         func(ctx, args)→(s,err)   RawArgs  string         // raw JSON from LLM
}                                    }

Hook interface {                     ToolResult {
  Name() string                        ToolCallID string
  Phases() []string                    Name       string
  BeforeAgent(ctx, *AgentState)→err    Output     string
  ModifyRequest(ctx, sp, msgs)         Error      string
    →(sp, msgs, err)                 }
  WrapModelCall(ctx, msgs, next)
    →(*llm.Response, err)            StreamEvent {
  AfterModel(ctx, state, []TC)         Event    string  // event type (see SSE Events below)
    →(map[id]ToolResult, err)          Name     string  // tool or model name
  WrapToolCall(ctx, TC, next)          RunID    string  // tool call ID
    →(*ToolResult, err)                Data     any     // event-specific payload
}                                      ThreadID string  // set on "done"
                                     }

ThreadStore {                        TraceRecorder interface {
  mu      sync.RWMutex                StartSpan(name) → SpanHandle
  threads map[string]*threadEntry     RecordEvent(name, metadata)
  ttl     1h                         }
  stop    chan struct{}               SpanHandle interface {
}                                      Set(key, value) → SpanHandle
threadEntry {                          End()
  state      *AgentState             }
  lastAccess time.Time
}

llm.Client interface {               llm.Request {
  Call(ctx, Request)→(*Response,err)   Model, Messages, Tools []ToolSchema
  Stream(ctx, Request, chan)→error     SystemPrompt, MaxTokens, *Temperature
  BuildRequestJSON(Request)→JSON     }
}
                                     llm.Response {Content, ToolCalls []ToolCallResult}
llm.StreamChunk {                    llm.ToolCallResult {ID, Name, Args}
  Delta    string                    llm.ToolSchema {Name, Description, Parameters}
  ToolCall *ToolCallResult           llm.Message {Role, Content, ToolCallID, Name, ToolCalls}
  Done     bool
  Error    error
}
```

---

## Complete runLoop() Execution

```
runLoop(ctx, messages []Message, threadID string, eventCh chan<- StreamEvent)
│
│
│ ═══════════════════════════════════════════════════════════════════════
│  PHASE 0: INITIALIZATION
│ ═══════════════════════════════════════════════════════════════════════
│
├── startTime = time.Now()
│
├── Thread State Management
│   ├── state = a.threadStore.LoadOrCreate(threadID)
│   │   │
│   │   │   ┌─── ThreadStore.LoadOrCreate ──────────────────────────┐
│   │   │   │  ts.mu.Lock() / defer ts.mu.Unlock()                  │
│   │   │   │                                                       │
│   │   │   │  IF threadID exists in ts.threads:                    │
│   │   │   │    entry.lastAccess = time.Now()                      │
│   │   │   │    return entry.state   // resume conversation        │
│   │   │   │                                                       │
│   │   │   │  ELSE (new thread):                                   │
│   │   │   │    state = &AgentState{                               │
│   │   │   │      ThreadID: threadID,                              │
│   │   │   │      Messages: [],                                    │
│   │   │   │      Files:    {},                                    │
│   │   │   │    }                                                  │
│   │   │   │    ts.threads[threadID] = {state, lastAccess: now}    │
│   │   │   │    return state                                       │
│   │   │   └───────────────────────────────────────────────────────┘
│   │   │
│   │   │   Note: ThreadStore has background evictLoop():
│   │   │     ticker every 5min → delete threads where
│   │   │     lastAccess < now - 1h (defaultThreadTTL)
│   │   │
│   ├── state.Messages = append(state.Messages, messages...)
│   │   // messages = new user input (validated as user/system roles)
│   │   // state.Messages may already contain history from previous turns
│   │
│   └── ctx = WithState(ctx, state)
│       // stores *AgentState in context via context.WithValue
│       // retrievable by hooks via StateFromContext(ctx)
│
├── tr = TraceFromContext(ctx)
│   // extracts TraceRecorder from context (may be nil)
│   // all trace calls below are nil-guarded
│
│
│ ═══════════════════════════════════════════════════════════════════════
│  PHASE 1: BEFORE AGENT (runs once)
│ ═══════════════════════════════════════════════════════════════════════
│
├── for _, hook := range a.Hooks:  (sequential, ordered)
│   │
│   ├── tr?.StartSpan("hook.before_agent/" + hook.Name())
│   │
│   ├── hook.BeforeAgent(ctx, state)
│   │   │
│   │   ├── FilesystemHook.BeforeAgent:
│   │   │   └── RegisterToolOnState(state, tool) × 7:
│   │   │       ├── "ls"         — h.fs.Ls(ctx, resolved)
│   │   │       ├── "read_file"  — h.fs.ReadFile(ctx, resolved)
│   │   │       ├── "write_file" — h.fs.WriteFile(ctx, resolved, content)
│   │   │       │                  + state.Files[resolved] = content
│   │   │       ├── "edit_file"  — h.fs.EditFile(ctx, resolved, old, new)
│   │   │       │                  + re-reads file → state.Files[resolved]
│   │   │       ├── "glob"       — h.fs.Glob(ctx, pattern, resolved)
│   │   │       ├── "grep"       — h.fs.Grep(ctx, pattern, resolved)
│   │   │       └── "execute"    — h.fs.Exec(ctx, command)
│   │   │       All tools: resolve path via h.resolvePath(), return JSON
│   │   │
│   │   ├── SkillsHook.BeforeAgent:
│   │   │   ├── allPaths = h.paths + h.prefs?.ExtraPaths
│   │   │   ├── seen = {existing skill paths}  // dedup
│   │   │   └── for each skillsDir in allPaths:
│   │   │       ├── h.backend.Execute("find {dir} -name SKILL.md -type f")
│   │   │       └── for each SKILL.md found:
│   │   │           ├── skip if seen[mdPath]
│   │   │           ├── h.backend.Execute("cat {mdPath}")
│   │   │           ├── entry.Name = directory name (fallback)
│   │   │           ├── parse YAML frontmatter (?s)\A---\s*\n(.*?\n)---\s*\n
│   │   │           │   ├── entry.Name = front["name"]
│   │   │           │   └── entry.Description = front["description"]
│   │   │           └── h.skills = append(h.skills, entry)
│   │   │
│   │   ├── MemoryHook.BeforeAgent:
│   │   │   ├── for each path in h.paths:
│   │   │   │   ├── h.backend.Execute("cat {path} 2>/dev/null")
│   │   │   │   └── if exit 0 && non-empty → parts = append(parts, output)
│   │   │   └── h.memoryContent = strings.Join(parts, "\n\n---\n\n")
│   │   │
│   │   ├── TodoListHook.BeforeAgent:
│   │   │   ├── state.Todos = []Todo{}  // initialize
│   │   │   └── RegisterToolOnState(state, tool) × 2:
│   │   │       ├── "write_todos" — replaces entire state.Todos with input array
│   │   │       │   params: {todos: [{id, title, status:"pending"|"in_progress"|"done"}]}
│   │   │       └── "update_todo" — finds todo by ID, updates status
│   │   │           params: {id, status}
│   │   │
│   │   └── SummarizationHook.BeforeAgent:
│   │       └── no-op (BaseHook)
│   │
│   ├── ON ERROR: return nil, "hook {name} BeforeAgent: {err}"
│   └── tr?.span.End()
│
│
│ ═══════════════════════════════════════════════════════════════════════
│  TOOL MAP CONSTRUCTION (after BeforeAgent, before loop)
│ ═══════════════════════════════════════════════════════════════════════
│
├── Build Tool Map
│   ├── toolMap = map[string]Tool{}
│   │
│   ├── for each t in a.Tools:              // server-registered tools (via RegisterTool)
│   │   └── toolMap[t.Name()] = t           // e.g. HTTPTool, custom FuncTool
│   │
│   ├── for each name, t in state.toolRegistry:  // hook-registered tools
│   │   └── toolMap[name] = t               // ls, read_file, write_file, edit_file,
│   │       // Note: hook tools override                glob, grep, execute,
│   │       // server tools with same name              write_todos, update_todo
│   │
│   └── toolSchemas = buildToolSchemas(toolMap)
│       └── for each tool in toolMap:
│           └── schemas = append(schemas, llm.ToolSchema{
│                 Name:        t.Name(),
│                 Description: t.Description(),
│                 Parameters:  t.Parameters(),  // JSON Schema
│               })
│
├── Trace: tr?.RecordEvent("tools.available", {count, tools: [names]})
│
│
│ ═══════════════════════════════════════════════════════════════════════
│  PHASE 2-5: LLM-TOOL LOOP (max 25 iterations)
│ ═══════════════════════════════════════════════════════════════════════
│
└── for iter := 0; iter < 25; iter++:
    │
    ├── Context Cancellation Check
    │   └── select { case <-ctx.Done(): return state, ctx.Err() }
    │
    │
    │ ─────────────────────────────────────────────────────────────────
    │  PHASE 2: MODIFY REQUEST (every iteration, sequential)
    │ ─────────────────────────────────────────────────────────────────
    │
    ├── Prepare Input
    │   ├── msgs = copy(state.Messages)     // shallow copy, don't mutate state
    │   └── systemPrompt = a.Config.SystemPrompt   // base system prompt from config
    │
    ├── for _, hook := range a.Hooks:
    │   │
    │   ├── tr?.StartSpan("hook.modify_request/"+hook.Name())
    │   │   tr?.span.Set("iteration", iter)
    │   │   tr?.span.Set("message_count_before", len(msgs))
    │   │
    │   ├── systemPrompt, msgs, err = hook.ModifyRequest(ctx, systemPrompt, msgs)
    │   │   │
    │   │   │   ┌─── System Prompt Modification Chain ─────────────────────────┐
    │   │   │   │                                                              │
    │   │   │   │  FilesystemHook.ModifyRequest:                               │
    │   │   │   │    → no-op, returns systemPrompt unchanged                   │
    │   │   │   │                                                              │
    │   │   │   │  SkillsHook.ModifyRequest:                                   │
    │   │   │   │    IF len(h.skills) == 0: pass-through                       │
    │   │   │   │    ELSE:                                                     │
    │   │   │   │      sb = "\n\nAvailable Skills:\n"                          │
    │   │   │   │      for each skill (skip if h.prefs.Disabled[name]):        │
    │   │   │   │        sb += "- [{name}] {desc} → Read {path} for full...\n"│
    │   │   │   │      IF count > 0: systemPrompt += sb                        │
    │   │   │   │                                                              │
    │   │   │   │  MemoryHook.ModifyRequest:                                   │
    │   │   │   │    IF h.memoryContent == "": pass-through                    │
    │   │   │   │    ELSE:                                                     │
    │   │   │   │      systemPrompt += "\n\n<agent_memory>\n"                  │
    │   │   │   │                      + h.memoryContent                       │
    │   │   │   │                      + "\n</agent_memory>\n\n"               │
    │   │   │   │                      + "Guidelines for agent memory:\n"      │
    │   │   │   │                      + "- This memory persists across...\n"  │
    │   │   │   │                      + "- You can update it by using...\n"   │
    │   │   │   │                      + "- Use it to track important...\n"    │
    │   │   │   │                      + "- Keep entries concise..."           │
    │   │   │   │                                                              │
    │   │   │   │  TodoListHook.ModifyRequest:                                 │
    │   │   │   │    systemPrompt += "\n\n" + h.systemPrompt                   │
    │   │   │   │      // ~50 lines of guidance: when to use write_todos,      │
    │   │   │   │      // task states, management rules, update_todo usage     │
    │   │   │   │    state = StateFromContext(ctx)                              │
    │   │   │   │    IF state.Todos not empty:                                 │
    │   │   │   │      systemPrompt += "\n\n## Current Task Progress\n"        │
    │   │   │   │      for each todo:                                          │
    │   │   │   │        "- [{status}] {id}: {title}"                          │
    │   │   │   │                                                              │
    │   │   │   │  SummarizationHook.ModifyRequest:                            │
    │   │   │   │    → no-op, returns systemPrompt unchanged                   │
    │   │   │   │    (summarization happens in WrapModelCall, not here)         │
    │   │   │   │                                                              │
    │   │   │   └──────────────────────────────────────────────────────────────┘
    │   │   │
    │   │   │   Final systemPrompt structure:
    │   │   │   ┌──────────────────────────────────────┐
    │   │   │   │ a.Config.SystemPrompt (base)         │
    │   │   │   │                                      │
    │   │   │   │ Available Skills:                    │
    │   │   │   │ - [csv-analyzer] Analyze CSV files...│
    │   │   │   │                                      │
    │   │   │   │ <agent_memory>                       │
    │   │   │   │ {AGENTS.md content}                  │
    │   │   │   │ </agent_memory>                      │
    │   │   │   │ Guidelines for agent memory: ...     │
    │   │   │   │                                      │
    │   │   │   │ ## write_todos                       │
    │   │   │   │ {todo usage guidance}                │
    │   │   │   │                                      │
    │   │   │   │ ## Current Task Progress             │
    │   │   │   │ - [in_progress] t1: Fix bug          │
    │   │   │   │ - [pending] t2: Write tests          │
    │   │   │   └──────────────────────────────────────┘
    │   │
    │   ├── ON ERROR: return nil, "hook {name} ModifyRequest: {err}"
    │   └── tr?.span.Set("message_count_after", len(msgs)).End()
    │
    ├── Trace: tr?.RecordEvent("llm.input", {...})
    │   └── inputEvent = {
    │         iteration: iter,
    │         message_count: len(msgs),
    │         system_prompt: sp (≤1000 chars, else truncated + "...(truncated)"),
    │         messages: [
    │           {
    │             role: m.Role,
    │             content_length: len(m.Content),
    │             content: m.Content (≤500 chars, else truncated),
    │             tool_calls: [tc.Name, ...],        // if present
    │             tool_call_id: m.ToolCallID,         // if present
    │           }, ...
    │         ]
    │       }
    │
    │
    │ ─────────────────────────────────────────────────────────────────
    │  PHASE 3: WRAP MODEL CALL (onion ring around LLM)
    │ ─────────────────────────────────────────────────────────────────
    │
    ├── modelCall = a.buildModelChain(systemPrompt, toolSchemas)
    │   │
    │   │   ┌─── Onion Ring Construction ──────────────────────────────────────────┐
    │   │   │                                                                      │
    │   │   │  Step 1: Define base function (innermost — actual LLM call)          │
    │   │   │                                                                      │
    │   │   │  base = func(ctx, msgs, eventCh) → (*ModelResponse, error)           │
    │   │   │    // (full expansion below)                                         │
    │   │   │                                                                      │
    │   │   │  Step 2: Wrap in reverse order (last hook = innermost wrapper)       │
    │   │   │                                                                      │
    │   │   │  fn = base                                                           │
    │   │   │  for i = len(a.Hooks)-1 downto 0:                                    │
    │   │   │    hook = a.Hooks[i]                                                 │
    │   │   │    prev = fn                                                         │
    │   │   │    fn = func(ctx, msgs, eventCh) {                                   │
    │   │   │      wrapped, err = hook.WrapModelCall(ctx, msgs,                    │
    │   │   │        func(c, m) {              // ← this is "next" for the hook   │
    │   │   │          resp, err = prev(c, m, eventCh)                             │
    │   │   │          if err: return nil, err                                     │
    │   │   │          // Convert ModelResponse → llm.Response                     │
    │   │   │          return &llm.Response{Content, ToolCalls→ToolCallResult}     │
    │   │   │        })                                                            │
    │   │   │      if err: return nil, err                                         │
    │   │   │      if wrapped == nil: return prev(ctx, msgs, eventCh)              │
    │   │   │      // Convert llm.Response → ModelResponse                         │
    │   │   │      return &ModelResponse{Content, ToolCalls→ToolCall}              │
    │   │   │    }                                                                 │
    │   │   │                                                                      │
    │   │   │  Example with hooks [Filesystem, Skills, Memory, TodoList, Summ]:    │
    │   │   │                                                                      │
    │   │   │  Call order:                                                          │
    │   │   │  Filesystem.WrapModelCall → next:                                    │
    │   │   │    Skills.WrapModelCall → next:                                      │
    │   │   │      Memory.WrapModelCall → next:                                    │
    │   │   │        TodoList.WrapModelCall → next:                                │
    │   │   │          Summarization.WrapModelCall → next:                         │
    │   │   │            base (actual LLM call)                                    │
    │   │   │                                                                      │
    │   │   │  Return order (response flows back out):                             │
    │   │   │            base returns ModelResponse                                │
    │   │   │          Summarization receives llm.Response (can transform)         │
    │   │   │        TodoList receives llm.Response (pass-through)                 │
    │   │   │      Memory receives llm.Response (pass-through)                     │
    │   │   │    Skills receives llm.Response (pass-through)                       │
    │   │   │  Filesystem receives llm.Response (pass-through)                     │
    │   │   │  → final ModelResponse                                               │
    │   │   │                                                                      │
    │   │   └──────────────────────────────────────────────────────────────────────┘
    │
    ├── eventCh ← StreamEvent{Event: "on_chat_model_start", Name: a.Config.ModelStr()}
    │
    ├── response, err = modelCall(ctx, msgs, eventCh)
    │   │
    │   │   ┌─── Onion Ring Execution ─────────────────────────────────────────────┐
    │   │   │                                                                      │
    │   │   │  REQUEST PATH (inward) ────────────────────────────────────────────  │
    │   │   │                                                                      │
    │   │   │  ┌─ Hook[0] FilesystemHook.WrapModelCall ────────────────────────┐  │
    │   │   │  │  return next(ctx, msgs)  // pass-through (BaseHook)           │  │
    │   │   │  │                                                               │  │
    │   │   │  │  ┌─ Hook[1] SkillsHook.WrapModelCall ──────────────────────┐  │  │
    │   │   │  │  │  return next(ctx, msgs)  // pass-through                │  │  │
    │   │   │  │  │                                                         │  │  │
    │   │   │  │  │  ┌─ Hook[2] MemoryHook.WrapModelCall ────────────────┐  │  │  │
    │   │   │  │  │  │  return next(ctx, msgs)  // pass-through          │  │  │  │
    │   │   │  │  │  │                                                   │  │  │  │
    │   │   │  │  │  │  ┌─ Hook[3] TodoListHook.WrapModelCall ────────┐  │  │  │  │
    │   │   │  │  │  │  │  return next(ctx, msgs)  // pass-through    │  │  │  │  │
    │   │   │  │  │  │  │                                             │  │  │  │  │
    │   │   │  │  │  │  │  ┌─ Hook[4] SummarizationHook.WrapModelCall │  │  │  │  │
    │   │   │  │  │  │  │  │                                          │  │  │  │  │
    │   │   │  │  │  │  │  │  totalTokens = estimateTokens(msgs)      │  │  │  │  │
    │   │   │  │  │  │  │  │    // sum(len(m.Content)/4 for m in msgs)│  │  │  │  │
    │   │   │  │  │  │  │  │  threshold = contextWindow * 0.85        │  │  │  │  │
    │   │   │  │  │  │  │  │    // default contextWindow = 128,000    │  │  │  │  │
    │   │   │  │  │  │  │  │    // threshold = 108,800 tokens         │  │  │  │  │
    │   │   │  │  │  │  │  │                                          │  │  │  │  │
    │   │   │  │  │  │  │  │  IF totalTokens ≤ threshold:             │  │  │  │  │
    │   │   │  │  │  │  │  │  │  return next(ctx, msgs)               │  │  │  │  │
    │   │   │  │  │  │  │  │  │  // no compression needed             │  │  │  │  │
    │   │   │  │  │  │  │  │  │                                       │  │  │  │  │
    │   │   │  │  │  │  │  │  IF totalTokens > threshold:             │  │  │  │  │
    │   │   │  │  │  │  │  │  │                                       │  │  │  │  │
    │   │   │  │  │  │  │  │  │  keepCount = len(msgs)/10 (min 2)     │  │  │  │  │
    │   │   │  │  │  │  │  │  │  oldMsgs = msgs[:len-keepCount]       │  │  │  │  │
    │   │   │  │  │  │  │  │  │  recentMsgs = msgs[len-keepCount:]    │  │  │  │  │
    │   │   │  │  │  │  │  │  │                                       │  │  │  │  │
    │   │   │  │  │  │  │  │  │  // Build summarization prompt        │  │  │  │  │
    │   │   │  │  │  │  │  │  │  sb = "Summarize the following        │  │  │  │  │
    │   │   │  │  │  │  │  │  │       conversation context            │  │  │  │  │
    │   │   │  │  │  │  │  │  │       concisely. Preserve key         │  │  │  │  │
    │   │   │  │  │  │  │  │  │       decisions, file paths, tool     │  │  │  │  │
    │   │   │  │  │  │  │  │  │       results, and important          │  │  │  │  │
    │   │   │  │  │  │  │  │  │       details. Keep the summary       │  │  │  │  │
    │   │   │  │  │  │  │  │  │       under 2000 words.\n\n"          │  │  │  │  │
    │   │   │  │  │  │  │  │  │  for each m in oldMsgs:               │  │  │  │  │
    │   │   │  │  │  │  │  │  │    content = m.Content                │  │  │  │  │
    │   │   │  │  │  │  │  │  │    // truncate write_file/edit_file   │  │  │  │  │
    │   │   │  │  │  │  │  │  │    // to 2000 chars + "...[truncated]"│  │  │  │  │
    │   │   │  │  │  │  │  │  │    sb += "[{role}] {content}\n\n"     │  │  │  │  │
    │   │   │  │  │  │  │  │  │                                       │  │  │  │  │
    │   │   │  │  │  │  │  │  │  // Separate LLM call for summary     │  │  │  │  │
    │   │   │  │  │  │  │  │  │  summaryResp = h.llmClient.Call(ctx,  │  │  │  │  │
    │   │   │  │  │  │  │  │  │    llm.Request{                       │  │  │  │  │
    │   │   │  │  │  │  │  │  │      Messages: [{role:"user",         │  │  │  │  │
    │   │   │  │  │  │  │  │  │                  content: sb}],       │  │  │  │  │
    │   │   │  │  │  │  │  │  │      MaxTokens: 2000,                 │  │  │  │  │
    │   │   │  │  │  │  │  │  │    })                                 │  │  │  │  │
    │   │   │  │  │  │  │  │  │                                       │  │  │  │  │
    │   │   │  │  │  │  │  │  │  ON ERROR:                            │  │  │  │  │
    │   │   │  │  │  │  │  │  │    return next(ctx, msgs)             │  │  │  │  │
    │   │   │  │  │  │  │  │  │    // degraded: no compression        │  │  │  │  │
    │   │   │  │  │  │  │  │  │                                       │  │  │  │  │
    │   │   │  │  │  │  │  │  │  ON SUCCESS:                          │  │  │  │  │
    │   │   │  │  │  │  │  │  │    summaryMsg = Message{              │  │  │  │  │
    │   │   │  │  │  │  │  │  │      Role: "system",                  │  │  │  │  │
    │   │   │  │  │  │  │  │  │      Content: "[Conversation Summary] │  │  │  │  │
    │   │   │  │  │  │  │  │  │        \n" + summaryResp.Content,     │  │  │  │  │
    │   │   │  │  │  │  │  │  │    }                                  │  │  │  │  │
    │   │   │  │  │  │  │  │  │    compressed = [summaryMsg]          │  │  │  │  │
    │   │   │  │  │  │  │  │  │                 + recentMsgs          │  │  │  │  │
    │   │   │  │  │  │  │  │  │    return next(ctx, compressed)       │  │  │  │  │
    │   │   │  │  │  │  │  │  │                                       │  │  │  │  │
    │   │   │  │  │  │  │  └──│───────────────────────────────────────┘  │  │  │  │
    │   │   │  │  │  │  │     │                                          │  │  │  │
    │   │   │  │  │  │  │     ▼                                          │  │  │  │
    │   │   │  │  │  │  │                                                │  │  │  │
    │   │   │  │  │  │  │  ┌─ BASE FUNCTION (actual LLM call) ────────────────────┐
    │   │   │  │  │  │  │  │                                                      │
    │   │   │  │  │  │  │  │  1. CONVERT MESSAGES                                 │
    │   │   │  │  │  │  │  │     llmMsgs = convertMessages(msgs)                  │
    │   │   │  │  │  │  │  │     └── for each agent.Message → llm.Message:        │
    │   │   │  │  │  │  │  │         ├── map Role, Content, ToolCallID, Name      │
    │   │   │  │  │  │  │  │         └── for each ToolCall → llm.ToolCallInfo:    │
    │   │   │  │  │  │  │  │             └── {ID, Name, Args}                     │
    │   │   │  │  │  │  │  │                                                      │
    │   │   │  │  │  │  │  │  2. BUILD LLM REQUEST                                │
    │   │   │  │  │  │  │  │     req = llm.Request{                               │
    │   │   │  │  │  │  │  │       Model:        a.Config.ModelStr(),              │
    │   │   │  │  │  │  │  │       Messages:     llmMsgs,                         │
    │   │   │  │  │  │  │  │       Tools:        toolSchemas,                     │
    │   │   │  │  │  │  │  │       MaxTokens:    4096,                            │
    │   │   │  │  │  │  │  │       SystemPrompt: systemPrompt,                    │
    │   │   │  │  │  │  │  │     }                                                │
    │   │   │  │  │  │  │  │                                                      │
    │   │   │  │  │  │  │  │  3. EMIT DEBUG EVENT                                 │
    │   │   │  │  │  │  │  │     eventCh ← StreamEvent{                           │
    │   │   │  │  │  │  │  │       Event: "on_llm_input",                         │
    │   │   │  │  │  │  │  │       Name:  model,                                  │
    │   │   │  │  │  │  │  │       Data:  a.LLM.BuildRequestJSON(req),            │
    │   │   │  │  │  │  │  │     }                                                │
    │   │   │  │  │  │  │  │     // provider-specific JSON (see LLM Providers)    │
    │   │   │  │  │  │  │  │                                                      │
    │   │   │  │  │  │  │  │  4. STREAMING LLM CALL (two goroutines)              │
    │   │   │  │  │  │  │  │                                                      │
    │   │   │  │  │  │  │  │     chunkCh = make(chan llm.StreamChunk, 64)          │
    │   │   │  │  │  │  │  │     var llmErr error                                 │
    │   │   │  │  │  │  │  │     var llmDone sync.WaitGroup                       │
    │   │   │  │  │  │  │  │                                                      │
    │   │   │  │  │  │  │  │     ┌─ Producer goroutine ─────────────────────────┐ │
    │   │   │  │  │  │  │  │     │  llmDone.Add(1)                              │ │
    │   │   │  │  │  │  │  │     │  defer llmDone.Done()                        │ │
    │   │   │  │  │  │  │  │     │  llmErr = a.LLM.Stream(ctx, req, chunkCh)   │ │
    │   │   │  │  │  │  │  │     │                                              │ │
    │   │   │  │  │  │  │  │     │  ┌─── LLM Provider Implementations ──────┐  │ │
    │   │   │  │  │  │  │  │     │  │                                        │  │ │
    │   │   │  │  │  │  │  │     │  │  OpenAIClient.Stream:                  │  │ │
    │   │   │  │  │  │  │  │     │  │    POST {baseURL}/chat/completions     │  │ │
    │   │   │  │  │  │  │  │     │  │    Headers:                            │  │ │
    │   │   │  │  │  │  │  │     │  │      Content-Type: application/json    │  │ │
    │   │   │  │  │  │  │  │     │  │      Authorization: Bearer {apiKey}    │  │ │
    │   │   │  │  │  │  │  │     │  │    Body (buildRequest):                │  │ │
    │   │   │  │  │  │  │  │     │  │      {                                 │  │ │
    │   │   │  │  │  │  │  │     │  │        model: "gpt-4o",                │  │ │
    │   │   │  │  │  │  │  │     │  │        stream: true,                   │  │ │
    │   │   │  │  │  │  │  │     │  │        max_tokens: 4096,               │  │ │
    │   │   │  │  │  │  │  │     │  │        messages: [                     │  │ │
    │   │   │  │  │  │  │  │     │  │          {role:"system",               │  │ │
    │   │   │  │  │  │  │  │     │  │           content: systemPrompt},      │  │ │
    │   │   │  │  │  │  │  │     │  │          {role:"user", content:...},   │  │ │
    │   │   │  │  │  │  │  │     │  │          {role:"assistant",            │  │ │
    │   │   │  │  │  │  │  │     │  │           tool_calls: [{id, type:      │  │ │
    │   │   │  │  │  │  │  │     │  │            "function", function:       │  │ │
    │   │   │  │  │  │  │  │     │  │            {name, arguments}}]},       │  │ │
    │   │   │  │  │  │  │  │     │  │          {role:"tool",                 │  │ │
    │   │   │  │  │  │  │  │     │  │           tool_call_id, content}       │  │ │
    │   │   │  │  │  │  │  │     │  │        ],                              │  │ │
    │   │   │  │  │  │  │  │     │  │        tools: [{type:"function",       │  │ │
    │   │   │  │  │  │  │  │     │  │          function:{name, description,  │  │ │
    │   │   │  │  │  │  │  │     │  │          parameters}}]                 │  │ │
    │   │   │  │  │  │  │  │     │  │      }                                 │  │ │
    │   │   │  │  │  │  │  │     │  │    HTTP timeout: 5 min                 │  │ │
    │   │   │  │  │  │  │  │     │  │    Response: SSE stream                │  │ │
    │   │   │  │  │  │  │  │     │  │    Parse (bufio.Scanner):              │  │ │
    │   │   │  │  │  │  │  │     │  │      skip lines without "data: "       │  │ │
    │   │   │  │  │  │  │  │     │  │      "data: [DONE]" → break            │  │ │
    │   │   │  │  │  │  │  │     │  │      parse JSON → openaiResponse       │  │ │
    │   │   │  │  │  │  │  │     │  │      delta.Content → ch ← {Delta}     │  │ │
    │   │   │  │  │  │  │  │     │  │      delta.ToolCalls:                  │  │ │
    │   │   │  │  │  │  │  │     │  │        new TC: store ID+Name,          │  │ │
    │   │   │  │  │  │  │  │     │  │               init argsBuilder         │  │ │
    │   │   │  │  │  │  │  │     │  │        existing: append to argsBuilder │  │ │
    │   │   │  │  │  │  │  │     │  │      finish_reason "tool_calls"|"stop":│  │ │
    │   │   │  │  │  │  │  │     │  │        for each accumulated TC:        │  │ │
    │   │   │  │  │  │  │  │     │  │          unmarshal argsBuilder → args   │  │ │
    │   │   │  │  │  │  │  │     │  │          ch ← {ToolCall: {ID,Name,A}} │  │ │
    │   │   │  │  │  │  │  │     │  │        reset accumulators              │  │ │
    │   │   │  │  │  │  │  │     │  │    ch ← StreamChunk{Done: true}        │  │ │
    │   │   │  │  │  │  │  │     │  │    close(ch)                           │  │ │
    │   │   │  │  │  │  │  │     │  │                                        │  │ │
    │   │   │  │  │  │  │  │     │  │  AnthropicClient.Stream:               │  │ │
    │   │   │  │  │  │  │  │     │  │    POST api.anthropic.com/v1/messages  │  │ │
    │   │   │  │  │  │  │  │     │  │    Headers:                            │  │ │
    │   │   │  │  │  │  │  │     │  │      Content-Type: application/json    │  │ │
    │   │   │  │  │  │  │  │     │  │      x-api-key: {apiKey}              │  │ │
    │   │   │  │  │  │  │  │     │  │      anthropic-version: 2023-06-01     │  │ │
    │   │   │  │  │  │  │  │     │  │    Body (buildRequest):                │  │ │
    │   │   │  │  │  │  │  │     │  │      {                                 │  │ │
    │   │   │  │  │  │  │  │     │  │        model: "claude-sonnet-...",      │  │ │
    │   │   │  │  │  │  │  │     │  │        system: systemPrompt,           │  │ │
    │   │   │  │  │  │  │  │     │  │        max_tokens: 4096,               │  │ │
    │   │   │  │  │  │  │  │     │  │        stream: true,                   │  │ │
    │   │   │  │  │  │  │  │     │  │        messages: [                     │  │ │
    │   │   │  │  │  │  │  │     │  │          // system → skip (separate)   │  │ │
    │   │   │  │  │  │  │  │     │  │          {role:"user", content:"..."},  │  │ │
    │   │   │  │  │  │  │  │     │  │          {role:"assistant",            │  │ │
    │   │   │  │  │  │  │  │     │  │           content: [                   │  │ │
    │   │   │  │  │  │  │  │     │  │             {type:"text", text:...},   │  │ │
    │   │   │  │  │  │  │  │     │  │             {type:"tool_use",          │  │ │
    │   │   │  │  │  │  │  │     │  │              id, name, input}          │  │ │
    │   │   │  │  │  │  │  │     │  │           ]},                          │  │ │
    │   │   │  │  │  │  │  │     │  │          {role:"user", content: [      │  │ │
    │   │   │  │  │  │  │  │     │  │            {type:"tool_result",        │  │ │
    │   │   │  │  │  │  │  │     │  │             tool_use_id, content}      │  │ │
    │   │   │  │  │  │  │  │     │  │          ]}                            │  │ │
    │   │   │  │  │  │  │  │     │  │        ],                              │  │ │
    │   │   │  │  │  │  │  │     │  │        tools: [{name, description,     │  │ │
    │   │   │  │  │  │  │  │     │  │          input_schema}]                │  │ │
    │   │   │  │  │  │  │  │     │  │      }                                 │  │ │
    │   │   │  │  │  │  │  │     │  │    Scanner buffer: 1MB                 │  │ │
    │   │   │  │  │  │  │  │     │  │    HTTP timeout: 5 min                 │  │ │
    │   │   │  │  │  │  │  │     │  │    Parse SSE stream:                   │  │ │
    │   │   │  │  │  │  │  │     │  │      "content_block_start":            │  │ │
    │   │   │  │  │  │  │  │     │  │        if type=="tool_use":            │  │ │
    │   │   │  │  │  │  │  │     │  │          save currentToolID + Name     │  │ │
    │   │   │  │  │  │  │  │     │  │          argsBuilder.Reset()           │  │ │
    │   │   │  │  │  │  │  │     │  │      "content_block_delta":            │  │ │
    │   │   │  │  │  │  │  │     │  │        "text_delta":                   │  │ │
    │   │   │  │  │  │  │  │     │  │          ch ← {Delta: text}           │  │ │
    │   │   │  │  │  │  │  │     │  │        "input_json_delta":             │  │ │
    │   │   │  │  │  │  │  │     │  │          argsBuilder += partialJSON    │  │ │
    │   │   │  │  │  │  │  │     │  │      "content_block_stop":             │  │ │
    │   │   │  │  │  │  │  │     │  │        if currentToolID != "":         │  │ │
    │   │   │  │  │  │  │  │     │  │          unmarshal argsBuilder → args   │  │ │
    │   │   │  │  │  │  │  │     │  │          ch ← {ToolCall:{ID,Name,A}}  │  │ │
    │   │   │  │  │  │  │  │     │  │          reset currentTool state       │  │ │
    │   │   │  │  │  │  │  │     │  │      "message_stop":                   │  │ │
    │   │   │  │  │  │  │  │     │  │        ch ← {Done: true}              │  │ │
    │   │   │  │  │  │  │  │     │  │        return nil                      │  │ │
    │   │   │  │  │  │  │  │     │  │    close(ch)                           │  │ │
    │   │   │  │  │  │  │  │     │  │                                        │  │ │
    │   │   │  │  │  │  │  │     │  │  HTTPProxyClient.Stream:               │  │ │
    │   │   │  │  │  │  │  │     │  │    POST {callbackURL}/llm/{model}/stream│ │ │
    │   │   │  │  │  │  │  │     │  │    Body: raw llm.Request JSON          │  │ │
    │   │   │  │  │  │  │  │     │  │    HTTP timeout: 5 min                 │  │ │
    │   │   │  │  │  │  │  │     │  │    Parse SSE: "data: {...}" lines      │  │ │
    │   │   │  │  │  │  │  │     │  │      unmarshal → StreamChunk           │  │ │
    │   │   │  │  │  │  │  │     │  │      ch ← chunk                       │  │ │
    │   │   │  │  │  │  │  │     │  │      if chunk.Done → break             │  │ │
    │   │   │  │  │  │  │  │     │  │    close(ch)                           │  │ │
    │   │   │  │  │  │  │  │     │  │                                        │  │ │
    │   │   │  │  │  │  │  │     │  └────────────────────────────────────────┘  │ │
    │   │   │  │  │  │  │  │     └──────────────────────────────────────────────┘ │
    │   │   │  │  │  │  │  │                                                      │
    │   │   │  │  │  │  │  │     ┌─ Consumer (main goroutine) ───────────────────┐│
    │   │   │  │  │  │  │  │     │                                               ││
    │   │   │  │  │  │  │  │     │  var content string                           ││
    │   │   │  │  │  │  │  │     │  var toolCalls []ToolCall                     ││
    │   │   │  │  │  │  │  │     │                                               ││
    │   │   │  │  │  │  │  │     │  for chunk := range chunkCh:                  ││
    │   │   │  │  │  │  │  │     │  │                                            ││
    │   │   │  │  │  │  │  │     │  │  IF chunk.Error != nil:                    ││
    │   │   │  │  │  │  │  │     │  │    return nil, chunk.Error                 ││
    │   │   │  │  │  │  │  │     │  │                                            ││
    │   │   │  │  │  │  │  │     │  │  IF chunk.Delta != "":                     ││
    │   │   │  │  │  │  │  │     │  │    content += chunk.Delta                  ││
    │   │   │  │  │  │  │  │     │  │    eventCh ← StreamEvent{                  ││
    │   │   │  │  │  │  │  │     │  │      Event: "on_chat_model_stream",        ││
    │   │   │  │  │  │  │  │     │  │      Name:  model,                         ││
    │   │   │  │  │  │  │  │     │  │      Data: {chunk:{content: delta}},       ││
    │   │   │  │  │  │  │  │     │  │    }                                       ││
    │   │   │  │  │  │  │  │     │  │                                            ││
    │   │   │  │  │  │  │  │     │  │  IF chunk.ToolCall != nil:                 ││
    │   │   │  │  │  │  │  │     │  │    toolCalls = append(toolCalls, ToolCall{ ││
    │   │   │  │  │  │  │  │     │  │      ID:   chunk.ToolCall.ID,              ││
    │   │   │  │  │  │  │  │     │  │      Name: chunk.ToolCall.Name,            ││
    │   │   │  │  │  │  │  │     │  │      Args: chunk.ToolCall.Args,            ││
    │   │   │  │  │  │  │  │     │  │    })                                      ││
    │   │   │  │  │  │  │  │     │  │                                            ││
    │   │   │  │  │  │  │  │     │  └── (loop until chunkCh closed)              ││
    │   │   │  │  │  │  │  │     │                                               ││
    │   │   │  │  │  │  │  │     │  llmDone.Wait()                               ││
    │   │   │  │  │  │  │  │     │  IF llmErr != nil: return nil, llmErr         ││
    │   │   │  │  │  │  │  │     │                                               ││
    │   │   │  │  │  │  │  │     │  return &ModelResponse{                       ││
    │   │   │  │  │  │  │  │     │    Content:   content,                        ││
    │   │   │  │  │  │  │  │     │    ToolCalls: toolCalls,                      ││
    │   │   │  │  │  │  │  │     │  }                                            ││
    │   │   │  │  │  │  │  │     └───────────────────────────────────────────────┘│
    │   │   │  │  │  │  │  │                                                      │
    │   │   │  │  │  │  │  └──────────────────────────────────────────────────────┘
    │   │   │  │  │  │  │
    │   │   │  │  │  │  │  RESPONSE PATH (outward) ──────────────────────────────
    │   │   │  │  │  │  │
    │   │   │  │  │  │  │  base returns ModelResponse
    │   │   │  │  │  │  │         │
    │   │   │  │  │  │  │         ▼ convert: ModelResponse → llm.Response
    │   │   │  │  │  │  │         │ {Content, ToolCalls[]→ToolCallResult[]}
    │   │   │  │  │  │  │         │
    │   │   │  │  │  │  │  SummarizationHook receives llm.Response
    │   │   │  │  │  │  │  (returns as-is, no outward transform)
    │   │   │  │  │  │  │         │
    │   │   │  │  │  │  │         ▼ convert: llm.Response → ModelResponse
    │   │   │  │  │  │  │         │ {Content, ToolCalls[]→ToolCall[]}
    │   │   │  │  │  │  │         │
    │   │   │  │  │  └──│─────────│────────────────────────────────────────────────
    │   │   │  │  │     │         │
    │   │   │  │  │  TodoList receives (pass-through)
    │   │   │  │  │              │
    │   │   │  │  └──────────────│─────────────────────────────────────────────────
    │   │   │  │                 │
    │   │   │  │  Memory receives (pass-through)
    │   │   │  │                 │
    │   │   │  └─────────────────│─────────────────────────────────────────────────
    │   │   │                    │
    │   │   │  Skills receives (pass-through)
    │   │   │                    │
    │   │   └────────────────────│─────────────────────────────────────────────────
    │   │                        │
    │   │  Filesystem receives (pass-through)
    │   │                        │
    │   │                        ▼
    │   │
    │   │  Final ModelResponse returned
    │   │    .Content   = accumulated text
    │   │    .ToolCalls = [{ID, Name, Args}, ...]
    │   │
    │   └──────────────────────────────────────────────────────────────────────────
    │
    ├── ON ERROR: return nil, "LLM call: {err}"
    │
    ├── eventCh ← StreamEvent{Event: "on_chat_model_end", Name: model}
    │
    ├── eventCh ← StreamEvent{
    │     Event: "on_llm_output",
    │     Name:  model,
    │     Data: {
    │       iteration:       iter,
    │       content:         response.Content,
    │       content_length:  len(response.Content),
    │       tool_call_count: len(response.ToolCalls),
    │       tool_calls: [                          // only if len > 0
    │         {id: tc.ID, name: tc.Name, args: tc.Args}, ...
    │       ],
    │     },
    │   }
    │
    ├── state.Messages = append(state.Messages, AI(response.Content, response.ToolCalls...))
    │   // AI() creates Message{Role:"assistant", Content, ToolCalls}
    │
    │
    │ ─────────────────────────────────────────────────────────────────
    │  EXIT CHECK
    │ ─────────────────────────────────────────────────────────────────
    │
    ├── IF len(response.ToolCalls) == 0:
    │   └── break   // LLM produced text-only response → conversation done
    │
    │
    │ ─────────────────────────────────────────────────────────────────
    │  PHASE 4: AFTER MODEL (intercept before dispatch)
    │ ─────────────────────────────────────────────────────────────────
    │
    ├── intercepted = map[string]ToolResult{}
    │
    ├── for _, hook := range a.Hooks:  (sequential)
    │   │
    │   ├── tr?.StartSpan("hook.after_model/" + hook.Name())
    │   │
    │   ├── got, err = hook.AfterModel(ctx, state, response.ToolCalls)
    │   │   │
    │   │   ├── FilesystemHook: return nil, nil  (BaseHook)
    │   │   ├── SkillsHook:    return nil, nil  (BaseHook)
    │   │   ├── MemoryHook:    return nil, nil  (BaseHook)
    │   │   │
    │   │   ├── TodoListHook.AfterModel:
    │   │   │   │
    │   │   │   │  Scan toolCalls, count writeCalls and updateCalls:
    │   │   │   │
    │   │   │   ├── RULE 1: 2+ write_todos in same turn
    │   │   │   │   └── reject ALL write_todos calls:
    │   │   │   │       intercepted[tc.ID] = ToolResult{
    │   │   │   │         Error:  "parallel write_todos rejected",
    │   │   │   │         Output: "Error: The write_todos tool should never
    │   │   │   │                  be called multiple times in parallel.
    │   │   │   │                  Please call it only once per model
    │   │   │   │                  invocation.",
    │   │   │   │       }
    │   │   │   │
    │   │   │   ├── RULE 2: 2+ update_todo in same turn
    │   │   │   │   └── reject ALL update_todo calls:
    │   │   │   │       intercepted[tc.ID] = ToolResult{
    │   │   │   │         Error:  "parallel update_todo rejected",
    │   │   │   │         Output: "Error: The update_todo tool should never
    │   │   │   │                  be called multiple times in parallel.
    │   │   │   │                  Use write_todos to update multiple
    │   │   │   │                  tasks at once.",
    │   │   │   │       }
    │   │   │   │
    │   │   │   └── RULE 3: 1 write_todos + any update_todo in same turn
    │   │   │       └── reject ALL update_todo calls:
    │   │   │           intercepted[tc.ID] = ToolResult{
    │   │   │             Error:  "update_todo rejected (write_todos in same turn)",
    │   │   │             Output: "Error: write_todos and update_todo should
    │   │   │                      not be called in the same turn. write_todos
    │   │   │                      replaces the entire list.",
    │   │   │           }
    │   │   │
    │   │   └── SummarizationHook: return nil, nil  (BaseHook)
    │   │
    │   ├── for id, r := range got:
    │   │   └── intercepted[id] = r   // merge results from all hooks
    │   │
    │   ├── ON ERROR: return nil, "hook {name} AfterModel: {err}"
    │   └── tr?.span.Set("intercepted", [ids]).End()
    │
    │
    │ ─────────────────────────────────────────────────────────────────
    │  PHASE 5: TOOL EXECUTION (parallel, with onion ring)
    │ ─────────────────────────────────────────────────────────────────
    │
    ├── var wg sync.WaitGroup
    ├── results = make([]ToolResult, len(response.ToolCalls))
    │
    ├── for i, tc := range response.ToolCalls:
    │   │
    │   │ ┌─── PATH A: INTERCEPTED (pre-built result) ────────────────┐
    │   │ │                                                             │
    │   │ │  IF intercepted[tc.ID] exists:                              │
    │   │ │    results[i] = intercepted result (error message)          │
    │   │ │    eventCh ← StreamEvent{                                   │
    │   │ │      Event:"on_tool_start", Name:tc.Name, RunID:tc.ID,     │
    │   │ │      Data:{input: tc.Args}                                  │
    │   │ │    }                                                        │
    │   │ │    eventCh ← StreamEvent{                                   │
    │   │ │      Event:"on_tool_end", Name:tc.Name, RunID:tc.ID,       │
    │   │ │      Data:{output: ir.Output}                               │
    │   │ │    }                                                        │
    │   │ │    continue  // skip execution entirely                     │
    │   │ │                                                             │
    │   │ └─────────────────────────────────────────────────────────────┘
    │   │
    │   │ ┌─── PATH B: NORMAL EXECUTION (in goroutine) ───────────────┐
    │   │ │                                                             │
    │   │ │  wg.Add(1)                                                  │
    │   │ │  go func(idx int, tc ToolCall):                             │
    │   │ │    defer wg.Done()                                          │
    │   │ │                                                             │
    │   │ │    eventCh ← StreamEvent{                                   │
    │   │ │      Event:"on_tool_start", Name:tc.Name, RunID:tc.ID,     │
    │   │ │      Data:{input: tc.Args}                                  │
    │   │ │    }                                                        │
    │   │ │                                                             │
    │   │ │    toolCallFn = a.buildToolCallChain(toolMap)                │
    │   │ │    │                                                        │
    │   │ │    │  ┌─── Tool Call Onion Ring Construction ────────────┐  │
    │   │ │    │  │                                                  │  │
    │   │ │    │  │  base = func(ctx, tc) → (*ToolResult, error)    │  │
    │   │ │    │  │    r = a.executeTool(ctx, tc, toolMap)           │  │
    │   │ │    │  │    return &r, nil                                │  │
    │   │ │    │  │                                                  │  │
    │   │ │    │  │  fn = base                                      │  │
    │   │ │    │  │  for i = len(hooks)-1 downto 0:                 │  │
    │   │ │    │  │    hook = hooks[i]                               │  │
    │   │ │    │  │    prev = fn                                     │  │
    │   │ │    │  │    fn = func(ctx, tc) {                          │  │
    │   │ │    │  │      return hook.WrapToolCall(ctx, tc, prev)     │  │
    │   │ │    │  │    }                                             │  │
    │   │ │    │  │                                                  │  │
    │   │ │    │  │  Call order:                                     │  │
    │   │ │    │  │  Hook[0].WrapToolCall → next:                   │  │
    │   │ │    │  │    Hook[1].WrapToolCall → next:                 │  │
    │   │ │    │  │      Hook[2].WrapToolCall → next:               │  │
    │   │ │    │  │        ...                                      │  │
    │   │ │    │  │          Hook[N].WrapToolCall → next:           │  │
    │   │ │    │  │            base (executeTool)                   │  │
    │   │ │    │  │                                                  │  │
    │   │ │    │  └──────────────────────────────────────────────────┘  │
    │   │ │    │                                                        │
    │   │ │    │  ┌─── Tool Call Onion Ring Execution ───────────────┐  │
    │   │ │    │  │                                                  │  │
    │   │ │    │  │  REQUEST PATH (inward):                          │  │
    │   │ │    │  │                                                  │  │
    │   │ │    │  │  ┌─ FilesystemHook.WrapToolCall ──────────────┐  │  │
    │   │ │    │  │  │  result, err = next(ctx, tc)               │  │  │
    │   │ │    │  │  │  // calls next first, then checks result   │  │  │
    │   │ │    │  │  │                                            │  │  │
    │   │ │    │  │  │  ┌─ SkillsHook.WrapToolCall ─────────┐    │  │  │
    │   │ │    │  │  │  │  return next(ctx, tc)  // passthru │    │  │  │
    │   │ │    │  │  │  │                                    │    │  │  │
    │   │ │    │  │  │  │  ┌─ MemoryHook.WrapToolCall ──┐   │    │  │  │
    │   │ │    │  │  │  │  │  return next(ctx, tc)      │   │    │  │  │
    │   │ │    │  │  │  │  │                            │   │    │  │  │
    │   │ │    │  │  │  │  │  ┌─ TodoList.WrapToolCall┐ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  return next(ctx, tc) │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │                       │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  ┌─ Summ.WrapToolCall │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  return next(ctx,tc│ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │                    │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  ┌─ BASE ────────┐ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │               │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │ executeTool:  │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │               │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │ tool = toolMap│ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │   [tc.Name]   │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │               │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │ IF not found: │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │   ToolResult{ │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │    Error:"un- │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │    known tool"│ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │   }           │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │               │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │ output, err = │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │  tool.Execute │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │  (ctx, tc.Args│ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │  )            │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │               │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │ IF err:       │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │   ToolResult{ │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │    Error: err │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │    Output:    │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │     "Error:…" │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │   }           │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │               │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │ ELSE:         │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │   ToolResult{ │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │    ToolCallID │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │    Name       │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │    Output     │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │   }           │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  │               │ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  └─ return &r ───┘ │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │                    │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  RESPONSE PATH     │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  (outward):        │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │                    │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  │  Summ: pass-thru   │ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  └────────────────────┘ │   │    │  │  │
    │   │ │    │  │  │  │  │  │  TodoList: pass-thru    │   │    │  │  │
    │   │ │    │  │  │  │  │  └─────────────────────────┘   │    │  │  │
    │   │ │    │  │  │  │  │  Memory: pass-thru             │    │  │  │
    │   │ │    │  │  │  │  └────────────────────────────────┘    │  │  │
    │   │ │    │  │  │  │  Skills: pass-thru                     │  │  │
    │   │ │    │  │  │  └────────────────────────────────────────┘  │  │
    │   │ │    │  │  │                                              │  │
    │   │ │    │  │  │  Filesystem: LARGE RESULT EVICTION           │  │
    │   │ │    │  │  │  IF len(result.Output) > 80,000 chars        │  │
    │   │ │    │  │  │  AND tool NOT in {ls, glob, grep,            │  │
    │   │ │    │  │  │      read_file, edit_file, write_file}:      │  │
    │   │ │    │  │  │    head = result.Output[:2000]                │  │
    │   │ │    │  │  │    tail = result.Output[last 2000]            │  │
    │   │ │    │  │  │    result.Output = head + "\n\n... [Output    │  │
    │   │ │    │  │  │      truncated: {N} chars total. Showing      │  │
    │   │ │    │  │  │      first and last 2000 chars] ...\n\n"      │  │
    │   │ │    │  │  │      + tail                                   │  │
    │   │ │    │  │  │                                              │  │
    │   │ │    │  │  └──────────────────────────────────────────────┘  │
    │   │ │    │  │                                                    │
    │   │ │    │  │  Final *ToolResult returned                       │
    │   │ │    │  └────────────────────────────────────────────────────┘
    │   │ │    │                                                        │
    │   │ │    wrapped, err = toolCallFn(ctx, tc)                       │
    │   │ │                                                             │
    │   │ │    IF err:                                                   │
    │   │ │      result = ToolResult{                                    │
    │   │ │        ToolCallID: tc.ID,                                    │
    │   │ │        Name:       tc.Name,                                  │
    │   │ │        Error:      err.Error(),                              │
    │   │ │        Output:     "Error: " + err.Error(),                  │
    │   │ │      }                                                       │
    │   │ │    ELSE IF wrapped != nil:                                   │
    │   │ │      result = *wrapped                                       │
    │   │ │                                                             │
    │   │ │    results[idx] = result                                     │
    │   │ │                                                             │
    │   │ │    eventCh ← StreamEvent{                                   │
    │   │ │      Event:"on_tool_end", Name:tc.Name, RunID:tc.ID,       │
    │   │ │      Data:{output: result.Output}                           │
    │   │ │    }                                                        │
    │   │ │                                                             │
    │   │ │  (end goroutine)                                            │
    │   │ │                                                             │
    │   │ └─────────────────────────────────────────────────────────────┘
    │   │
    │   └── (next tool call, also in parallel goroutine)
    │
    ├── wg.Wait()   // block until ALL parallel tool goroutines complete
    │
    │
    │ ─────────────────────────────────────────────────────────────────
    │  APPEND TOOL RESULTS TO STATE
    │ ─────────────────────────────────────────────────────────────────
    │
    ├── for _, result := range results:
    │   └── state.Messages = append(state.Messages,
    │         ToolMsg(result.ToolCallID, result.Name, result.Output))
    │       // ToolMsg creates Message{Role:"tool", Content:output,
    │       //   ToolCallID:id, Name:name}
    │
    │
    │ ─────────────────────────────────────────────────────────────────
    │  NEXT ITERATION (back to ModifyRequest)
    │ ─────────────────────────────────────────────────────────────────
    │
    └── (loop continues with updated state.Messages)


═══════════════════════════════════════════════════════════════════════
 POST-LOOP: FINALIZATION
═══════════════════════════════════════════════════════════════════════

├── elapsed = time.Since(startTime).Milliseconds()
│
├── a.threadStore.Save(threadID, state)
│   │
│   │   ┌─── ThreadStore.Save ───────────────────────────┐
│   │   │  ts.mu.Lock() / defer ts.mu.Unlock()           │
│   │   │  ts.threads[threadID] = &threadEntry{           │
│   │   │    state:      state,                           │
│   │   │    lastAccess: time.Now(),  // refresh TTL      │
│   │   │  }                                              │
│   │   └─────────────────────────────────────────────────┘
│
├── state.Files = map[string]string{}   // reset placeholder
│
└── return state, nil

    ┌─── Back in RunStream (caller) ────────────────────────────┐
    │                                                            │
    │  IF err != nil:                                            │
    │    eventCh ← StreamEvent{                                  │
    │      Event: "error",                                       │
    │      Data:  {"error": err.Error()},                        │
    │    }                                                       │
    │                                                            │
    │  ELSE:                                                     │
    │    eventCh ← StreamEvent{                                  │
    │      Event:    "done",                                     │
    │      ThreadID: state.ThreadID,                             │
    │      Data:     {"thread_id": state.ThreadID},              │
    │    }                                                       │
    │                                                            │
    │  close(eventCh)   // via defer                             │
    └────────────────────────────────────────────────────────────┘
```

---

## SSE Events (Complete List)

Events emitted to `eventCh` in chronological order per iteration:

| # | Event | Name | RunID | Data | Emitted By |
|---|-------|------|-------|------|------------|
| 1 | `on_chat_model_start` | model name | — | — | runLoop |
| 2 | `on_llm_input` | model name | — | `json.RawMessage` (provider-specific request body) | base function |
| 3 | `on_chat_model_stream` | model name | — | `{chunk:{content: delta}}` | base function (per chunk) |
| 4 | `on_chat_model_end` | model name | — | — | runLoop |
| 5 | `on_llm_output` | model name | — | `{iteration, content, content_length, tool_call_count, tool_calls[]}` | runLoop |
| 6 | `on_tool_start` | tool name | tool call ID | `{input: tc.Args}` | runLoop (per tool) |
| 7 | `on_tool_end` | tool name | tool call ID | `{output: result.Output}` | runLoop (per tool) |
| 8 | `error` | — | — | `{error: message}` | RunStream |
| 9 | `done` | — | — | `{thread_id: id}` | RunStream |

Events 1-7 repeat per iteration. Events 6-7 repeat per tool call within an iteration.
Event 8 OR 9 is sent exactly once at the end.

---

## State Mutation Timeline

```
                    state.Messages grows over time:

Before loop:     [user_msg₁]
                       ↓ BeforeAgent (no message changes, but toolRegistry populated)
Iteration 0:
  After LLM:     [user_msg₁, assistant_msg₁{toolCalls}]
  After tools:   [user_msg₁, assistant_msg₁, tool_result₁, tool_result₂]
Iteration 1:
  After LLM:     [..., assistant_msg₂{toolCalls}]
  After tools:   [..., assistant_msg₂, tool_result₃]
Iteration 2:
  After LLM:     [..., assistant_msg₃]   ← no toolCalls → break
                       ↓
Post-loop:       threadStore.Save(threadID, state)
                 // persisted for next user message on same thread
```

```
                    state.Todos mutated by tool functions:

BeforeAgent:     []
write_todos:     [{id:"t1", title:"Fix bug", status:"in_progress"},
                  {id:"t2", title:"Write tests", status:"pending"}]
update_todo:     [{id:"t1", ..., status:"done"},
                  {id:"t2", ..., status:"in_progress"}]
```

```
                    state.Files mutated by filesystem tools:

write_file:      state.Files["/app/main.go"] = "package main..."
edit_file:       state.Files["/app/main.go"] = "package main... (edited)"
```
