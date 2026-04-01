# Agent Loop & Hook Architecture

```
═══════════════════════════════════════════════════════════════
                    AGENT LOOP (max 25 iterations)
═══════════════════════════════════════════════════════════════


 ┌─────────────────────────────────────────────────────────┐
 │  PHASE 1: BeforeAgent (runs ONCE at start)              │
 │                                                         │
 │  Truncation      ←── pass-through                       │
 │    Tracing       ←── pass-through                       │
 │      TodoList    ←── registers todo tools                │
 │        Filesystem ←── registers file tools               │
 │          LazySkills ←── registers load/unload/list_skill │
 │            Memory    ←── loads memory files              │
 │              Subagent ←── registers delegate_to_agent    │
 │                Summarization ←── pass-through            │
 │                  done                                    │
 │                                                         │
 │  OUTPUT: AgentState populated with tools & memory       │
 └────────────────────────┬────────────────────────────────┘
                          │
                          ▼
 ┌────────────────────────────────────────────────────────────┐
 │  ╔══════════════════════════════════════════════════════╗   │
 │  ║           LOOP (up to 25 iterations)                ║   │
 │  ╚══════════════════════════════════════════════════════╝   │
 │                                                            │
 │         ┌─────────────────────────────────┐                │
 │         │  INPUT: systemPrompt + messages │                │
 │         └────────────────┬────────────────┘                │
 │                          ▼                                 │
 │  ┌──────────────────────────────────────────────────────┐  │
 │  │  PHASE 2: ModifyRequest (before each LLM call)       │  │
 │  │                                                      │  │
 │  │  Truncation      ←── pass-through                    │  │
 │  │    Tracing       ←── pass-through                    │  │
 │  │      TodoList    ←── ✏️ INTERCEPTS systemPrompt       │  │
 │  │        │              (injects todo guidance)         │  │
 │  │        Filesystem ←── pass-through                   │  │
 │  │          LazySkills ←── ✏️ INTERCEPTS messages         │  │
 │  │            │              (injects active skill)      │  │
 │  │            Memory    ←── ✏️ INTERCEPTS systemPrompt    │  │
 │  │              │              (injects memory content)  │  │
 │  │              Subagent ←── pass-through               │  │
 │  │                Summarization ←── ✏️ INTERCEPTS msgs    │  │
 │  │                  │    (compresses old msgs if >85%)   │  │
 │  │                  ▼                                   │  │
 │  │          return (systemPrompt', messages')           │  │
 │  └──────────────────────┬───────────────────────────────┘  │
 │                         │                                  │
 │         ┌───────────────┴───────────────────┐              │
 │         │  MODIFIED: systemPrompt' + msgs'  │              │
 │         └───────────────┬───────────────────┘              │
 │                         ▼                                  │
 │  ┌──────────────────────────────────────────────────────┐  │
 │  │  PHASE 3: WrapModelCall (around LLM call)            │  │
 │  │                                                      │  │
 │  │  Truncation      ←── pass-through                    │  │
 │  │    Tracing       ←── 👁️ OBSERVES request & response   │  │
 │  │      │                (records span: timing, tokens)  │  │
 │  │      (all others pass-through)                       │  │
 │  │                  ┌─────────────────┐                 │  │
 │  │                  │  LLM API call   │  ←── core       │  │
 │  │                  └─────────────────┘                 │  │
 │  └──────────────────────┬───────────────────────────────┘  │
 │                         │                                  │
 │         ┌───────────────┴───────────────────┐              │
 │         │  LLM RESPONSE:                    │              │
 │         │    content: "Here's what I found" │              │
 │         │    tool_calls: [                  │              │
 │         │      {id:"1", name:"execute"...}, │              │
 │         │      {id:"2", name:"write_todos"} │              │
 │         │    ]                              │              │
 │         └───────────────┬───────────────────┘              │
 │                         │                                  │
 │                         ▼                                  │
 │           ┌─── no tool calls? ───► BREAK (exit loop) ──►───┤
 │           │                                                │
 │           ▼ has tool calls                                 │
 │  ┌──────────────────────────────────────────────────────┐  │
 │  │  PHASE 4: AfterModel (intercept before dispatch)     │  │
 │  │  ⚠️  NOT an onion ring — runs sequentially            │  │
 │  │                                                      │  │
 │  │  tool_calls: [{id:"1", execute}, {id:"2", todos}]    │  │
 │  │                         │                            │  │
 │  │  Truncation      ─── pass-through                    │  │
 │  │  Tracing         ─── pass-through                    │  │
 │  │  TodoList        ─── 🚫 INTERCEPTS id:"2"            │  │
 │  │    │                  (duplicate write_todos →        │  │
 │  │    │                   returns pre-built result,      │  │
 │  │    │                   skips actual execution)        │  │
 │  │  (all others pass-through)                           │  │
 │  │                         │                            │  │
 │  │  intercepted: {id:"2" → pre-built result}            │  │
 │  │  to execute:  [id:"1" execute]                       │  │
 │  └──────────────────────┬───────────────────────────────┘  │
 │                         │                                  │
 │         ┌───────────────┴───────────────────┐              │
 │         │  DISPATCH:                        │              │
 │         │    id:"1" → execute (run normally)│              │
 │         │    id:"2" → write_todos (SKIPPED, │              │
 │         │             use pre-built result) │              │
 │         └───────────────┬───────────────────┘              │
 │                         │                                  │
 │                         ▼                                  │
 │  ┌──────────────────────────────────────────────────────┐  │
 │  │  PHASE 5: WrapToolCall (around each tool, parallel)  │  │
 │  │                                                      │  │
 │  │  For each NON-intercepted tool call (goroutines):    │  │
 │  │                                                      │  │
 │  │  ┌────────────────────────────────────────────────┐  │  │
 │  │  │ Tool: "execute" (id:"1")                       │  │  │
 │  │  │                                                │  │  │
 │  │  │ Truncation ─── ✂️ INTERCEPTS result              │  │  │
 │  │  │   │             (truncates if > maxChars,       │  │  │
 │  │  │   │              skips filesystem tool names)   │  │  │
 │  │  │   Tracing ──── 👁️ OBSERVES call & result        │  │  │
 │  │  │     │            (records span: timing, I/O)    │  │  │
 │  │  │     (all others pass-through)                   │  │  │
 │  │  │                 ┌─────────────────┐             │  │  │
 │  │  │                 │ tool.Execute()  │  ←── core   │  │  │
 │  │  │                 └────────┬────────┘             │  │  │
 │  │  │                         ▼                       │  │  │
 │  │  │              raw output (possibly huge)         │  │  │
 │  │  │                         ▼                       │  │  │
 │  │  │              Tracing 👁️ records output           │  │  │
 │  │  │                         ▼                       │  │  │
 │  │  │              Truncation ✂️ head+tail if >maxChars │  │  │
 │  │  │                         ▼                       │  │  │
 │  │  │              truncated output returned          │  │  │
 │  │  └────────────────────────────────────────────────┘  │  │
 │  │                                                      │  │
 │  │  ┌────────────────────────────────────────────────┐  │  │
 │  │  │ Tool: "read_file" (filesystem — NOT truncated) │  │  │
 │  │  │                                                │  │  │
 │  │  │ Truncation ─── ⏭️ SKIPS (name in excluded list)  │  │  │
 │  │  │   Tracing ──── 👁️ OBSERVES                      │  │  │
 │  │  │     (all others pass-through)                   │  │  │
 │  │  │                 ┌─────────────────┐             │  │  │
 │  │  │                 │ tool.Execute()  │             │  │  │
 │  │  │                 └────────┬────────┘             │  │  │
 │  │  │                         ▼                       │  │  │
 │  │  │              full output returned (no truncation)│  │  │
 │  │  └────────────────────────────────────────────────┘  │  │
 │  └──────────────────────┬───────────────────────────────┘  │
 │                         │                                  │
 │         ┌───────────────┴───────────────────┐              │
 │         │  ALL RESULTS:                     │              │
 │         │    id:"1" → truncated output      │              │
 │         │    id:"2" → pre-built (AfterModel)│              │
 │         │                                   │              │
 │         │  Appended to state.Messages       │              │
 │         └───────────────┬───────────────────┘              │
 │                         │                                  │
 │                         ▼                                  │
 │              ┌──── next iteration ────┐                    │
 │              └────────────────────────┘                    │
 │                                                            │
 └────────────────────────────────────────────────────────────┘
                          │
                          ▼
                Save thread to ThreadStore
                Emit "done" event


═══════════════════════════════════════════════════════════════
              INTERCEPTION SUMMARY
═══════════════════════════════════════════════════════════════

  ✏️  MODIFIES data flowing through (changes prompt/messages)
  ✂️  TRUNCATES output on the way back up (reduces size)
  🚫 BLOCKS execution (returns pre-built result, tool never runs)
  👁️  OBSERVES only (records metrics, no data change)
  ⏭️  SKIPS processing (excluded by name, passes through unchanged)

  Phase 2 ModifyRequest:
    TodoList ✏️ systemPrompt │ LazySkills ✏️ messages │
    Memory ✏️ systemPrompt   │ Summarization ✏️ messages

  Phase 3 WrapModelCall:
    Tracing 👁️ request + response

  Phase 4 AfterModel:
    TodoList 🚫 duplicate write_todos

  Phase 5 WrapToolCall:
    Truncation ✂️ large output (⏭️ skips filesystem tools)
    Tracing 👁️ call + result
```

## Onion Ring Pattern Explained

The `WrapModelCall` and `WrapToolCall` phases use an **onion ring** pattern. The key insight: one function handles both "before" and "after" — split by the `next()` call.

### How it works

```go
// agent/hook.go — Hook interface
type Hook interface {
    WrapModelCall(ctx context.Context, msgs []Message, next ModelCallWrapFunc) (*llm.Response, error)
    WrapToolCall(ctx context.Context, call ToolCall, next ToolCallFunc) (*ToolResult, error)
    // ...
}
```

Each hook receives a `next` function. Code before `next()` runs on the way **in**, code after `next()` runs on the way **out**:

```go
func (h *SomeHook) WrapModelCall(ctx context.Context, msgs []Message, next ModelCallWrapFunc) (*llm.Response, error) {
    // ──── BEFORE LLM CALL ────
    //   modify msgs, log request, start timer, etc.

    response, err := next(ctx, msgs)   // ← this IS the LLM call (or next hook's wrapper)

    // ──── AFTER LLM CALL ────
    //   inspect response, record timing, modify result, etc.

    return response, err
}
```

Same pattern for tool calls:

```go
func (h *TruncationHook) WrapToolCall(ctx context.Context, call ToolCall, next ToolCallFunc) (*ToolResult, error) {
    // ──── BEFORE TOOL ────
    //   (nothing to do here for truncation)

    result, err := next(ctx, call)     // ← this IS the tool execution (or next hook's wrapper)

    // ──── AFTER TOOL ────
    //   truncate result.Output if too large

    return result, err
}
```

### Nesting with multiple hooks

The chain is built in `buildToolCallChain()` (`agent/loop.go:529`). Hooks wrap each other — outermost runs first on the way in, last on the way out:

```
WrapModelCall nesting:

  Truncation.WrapModelCall:  before (pass-through)
    Tracing.WrapModelCall:   before (start timer)
      (all others pass-through)
        ┌─────────────────┐
        │  LLM API call   │  ←── next() at the center
        └─────────────────┘
      (all others pass-through)
    Tracing.WrapModelCall:   after (record duration, token count)
  Truncation.WrapModelCall:  after (pass-through)


WrapToolCall nesting:

  Truncation.WrapToolCall:   before (nothing)
    Tracing.WrapToolCall:    before (start timer)
      (all others pass-through)
        ┌─────────────────┐
        │ tool.Execute()  │  ←── next() at the center
        └─────────────────┘
      (all others pass-through)
    Tracing.WrapToolCall:    after (record duration, I/O)
  Truncation.WrapToolCall:   after (truncate if > maxChars)
```

### Chain construction

```go
// agent/loop.go:529 — builds the onion ring
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
```

The loop iterates hooks in **reverse** so that hook[0] (Truncation) becomes the outermost wrapper. When `fn` is called:
1. Truncation's `WrapToolCall` runs → calls `next` (which is Tracing's wrapper)
2. Tracing's `WrapToolCall` runs → calls `next` (which is the next hook's wrapper)
3. ... until `base` is reached → `tool.Execute()` runs
4. Results bubble back up through each hook's "after" code

### System prompt is rebuilt every iteration

The system prompt is NOT built once. Each loop iteration starts fresh:

```go
// agent/loop.go:119 — fresh copy each iteration
systemPrompt := a.Config.SystemPrompt

// hooks modify the copy sequentially
for _, hook := range a.Hooks {
    systemPrompt, msgs, err = hook.ModifyRequest(ctx, systemPrompt, msgs)
}
```

This means each iteration reflects the latest state:
- Iteration 1: no active skill → skill prompt absent
- LLM calls `activate_skill("csv-analyzer")`
- Iteration 2: skill active → SKILL.md content injected into system prompt
- LLM calls `deactivate_skill`
- Iteration 3: skill deactivated → skill prompt removed again
