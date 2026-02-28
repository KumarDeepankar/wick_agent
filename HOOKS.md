# hooks — Agent Middleware System

## Overview

`hooks` is the middleware layer for the agent loop. Each hook implements one or more phases to inject tools, modify prompts, wrap LLM calls, or wrap tool executions. Hooks compose in an onion-ring pattern — the first hook registered is the outermost wrapper.

```
Agent loop
  │
  ├── BeforeAgent          (one-time setup, sequential)
  │     FilesystemHook     → registers 7 file tools on state
  │     TodoListHook       → registers write_todos tool
  │     SkillsHook         → discovers SKILL.md files
  │     MemoryHook         → loads AGENTS.md files
  │
  ├── ModifyRequest        (before each LLM call, sequential)
  │     SkillsHook         → injects skill catalog into system message
  │     MemoryHook         → injects <agent_memory> into system message
  │
  ├── WrapModelCall        (onion ring around LLM call)
  │     TracingHook        → outermost: records timing spans
  │     SummarizationHook  → innermost: compresses old messages
  │     actual LLM call    → center
  │
  └── WrapToolCall         (onion ring around tool execution)
        TracingHook        → outermost: records timing spans
        FilesystemHook     → evicts results >80k chars
        actual tool exec   → center
```

---

## Hook Interface (`agent/hook.go`)

```go
type Hook interface {
    Name() string
    Phases() []string
    BeforeAgent(ctx context.Context, state *AgentState) error
    WrapModelCall(ctx context.Context, msgs []Message, next ModelCallWrapFunc) (*llm.Response, error)
    WrapToolCall(ctx context.Context, call ToolCall, next ToolCallFunc) (*ToolResult, error)
    ModifyRequest(ctx context.Context, msgs []Message) ([]Message, error)
}
```

**Four phases:**

| Phase | When | Pattern |
|-------|------|---------|
| `before_agent` | Once, before agent loop starts | Sequential — each hook runs in order |
| `modify_request` | Before each LLM call | Sequential — each hook transforms the message list |
| `wrap_model_call` | Around each LLM call | Onion ring — hooks wrap inner function |
| `wrap_tool_call` | Around each tool execution | Onion ring — hooks wrap inner function |

**BaseHook** provides no-op defaults for all methods. Implementations embed it and override only the phases they need.

---

## Onion Ring Composition (`agent/loop.go`)

Hooks are iterated in reverse to build the chain. The first hook in the array becomes the outermost wrapper:

```go
fn := baseLLMCall
for i := len(hooks) - 1; i >= 0; i-- {
    hook := hooks[i]
    prev := fn
    fn = func(ctx, msgs, eventCh) {
        return hook.WrapModelCall(ctx, msgs, prev)
    }
}
// fn is now: hook[0] → hook[1] → ... → hook[n] → baseLLMCall
```

Same pattern applies to `WrapToolCall`.

---

## Hook Registration Order (`handlers/handlers.go`)

Hooks are built and appended during agent creation:

```go
agentHooks := []agent.Hook{
    tracing.NewTracingHook(),                         // 1. outermost wrapper
    hooks.NewTodoListHook(),                          // 2. always active
    hooks.NewFilesystemHook(backend),                 // 3. when backend available
    hooks.NewSkillsHook(backend, cfg.Skills.Paths),   // 4. when configured
    hooks.NewMemoryHook(backend, cfg.Memory.Paths),   // 5. when configured
    hooks.NewSummarizationHook(llmClient, ctxWindow), // 6. innermost wrapper
}
```

---

## FilesystemHook (`hooks/filesystem.go`)

Registers file-operation tools and evicts oversized tool results.

**Phases:** `before_agent`, `wrap_tool_call`

### BeforeAgent — Tool Registration

Registers 7 tools on `AgentState` via `RegisterToolOnState()`:

| Tool | Description | Key behavior |
|------|-------------|-------------|
| `ls` | List directory entries | Returns JSON array of `{name, type, size}` |
| `read_file` | Read file contents | Delegates to `backend.FS().ReadFile()` |
| `write_file` | Write file | Creates parent dirs automatically |
| `edit_file` | Replace text in file | Exact match of `old_text`, replaces first occurrence |
| `glob` | Find files by pattern | Filename matching via `filepath.Match` |
| `grep` | Search file contents | Regex pattern matching |
| `execute` | Run shell command | Delegates to `backend.FS().Exec()` |

All tools delegate to `backend.FS()` which returns either `LocalFS` (direct stdlib) or `RemoteFS` (via `wickfs` CLI in container). See [WICKFS.md](WICKFS.md) for details.

Files written or edited are tracked in `state.Files[path] = content` for state persistence.

### WrapToolCall — Large Result Eviction

Truncates tool results exceeding 80,000 characters (~20k tokens):

```
[first 2000 chars]

... (truncated X characters) ...

[last 2000 chars]
```

**Excluded tools** (never evicted): `ls`, `glob`, `grep`, `read_file`, `edit_file`, `write_file`

Only `execute` and any custom tools are subject to eviction.

---

## SkillsHook (`hooks/skills.go`)

Discovers skill definitions and injects a catalog into the system prompt for progressive loading.

**Phases:** `before_agent`, `modify_request`

### BeforeAgent — Skill Discovery

1. Runs `find` on each configured skill directory path
2. Locates `SKILL.md` files in subdirectories
3. Parses YAML frontmatter (`---\nname: ...\ndescription: ...\n---`)
4. Stores `SkillEntry{Name, Description, Path}` for each skill

### ModifyRequest — Catalog Injection

Appends a catalog to the system message listing each skill:

```
[skill-name] description → Read /path/to/SKILL.md for full instructions
```

**Progressive loading:** Only metadata appears in the prompt. The agent calls `read_file` on the SKILL.md path when it needs the full skill instructions. This saves tokens by avoiding upfront inclusion of all skill content.

---

## MemoryHook (`hooks/memory.go`)

Loads persistent agent memory from AGENTS.md files and injects it into the system prompt.

**Phases:** `before_agent`, `modify_request`

### BeforeAgent — Load Memory

- Reads AGENTS.md files from configured paths via `cat` command
- Concatenates multiple files with `\n\n---\n\n` separator
- Missing files are silently skipped

### ModifyRequest — Memory Injection

Wraps loaded content in XML tags and appends to the system message:

```
<agent_memory>
[AGENTS.md content]
</agent_memory>

Guidelines for agent memory:
- This memory persists across conversations
- You can update it by using edit_file on the AGENTS.md file
- Use it to track important context, decisions, and patterns
- Keep entries concise and organized
```

The agent can update its own memory by editing the AGENTS.md file during execution.

---

## SummarizationHook (`hooks/summarization.go`)

Compresses conversation history when approaching the context window limit.

**Phases:** `wrap_model_call`

### WrapModelCall — Context Compression

**Trigger:** Total estimated tokens > 85% of context window

**Token estimation:** `len(content) / 4` (rough heuristic)

**When triggered:**
1. Splits messages into old (to summarize) and recent (to keep)
2. Recent = last 10% of messages (minimum 2)
3. Truncates `write_file`/`edit_file` content in old messages to 2,000 chars
4. Calls LLM with summarization prompt requesting <2,000 word summary
5. Replaces old messages with a single summary message
6. Passes summarized + recent messages to the next function in the chain

**Graceful degradation:** If the summarization LLM call fails, messages pass through unchanged.

### Configuration

```go
hooks.NewSummarizationHook(llmClient, contextWindow)
```

| Parameter | Default | Description |
|-----------|---------|-------------|
| `contextWindow` | 128,000 | Total context window in tokens |

---

## TodoListHook (`hooks/todolist.go`)

Provides task tracking via a `write_todos` tool.

**Phases:** `before_agent`

### BeforeAgent — Tool Registration

Registers `write_todos` tool on state. Initializes `state.Todos` as empty slice.

### write_todos Tool

Takes a complete todo list and replaces `state.Todos`:

```json
{
  "todos": [
    {"id": "1", "title": "Parse config file", "status": "done"},
    {"id": "2", "title": "Implement handler",  "status": "in_progress"},
    {"id": "3", "title": "Write tests",        "status": "pending"}
  ]
}
```

**Statuses:** `pending`, `in_progress`, `done`

Todos are persisted in `AgentState.Todos` and saved to the thread store.

---

## TracingHook (`tracing/hook.go`)

Records timing and debug information for observability.

**Phases:** `wrap_model_call`, `wrap_tool_call`

### WrapModelCall

Creates span `"llm.call"` and records:
- `message_count`, `content_length`, `tool_calls_count`
- First 500 chars of content and tool call names
- Errors if present

### WrapToolCall

Creates span `"tool.call"` and records:
- `tool_name`, `tool_call_id`, `tool_args`
- `output_length`, first 500 chars of output
- Errors if present

**No-op** when trace recorder is nil (reads from `agent.TraceFromContext(ctx)`).

---

## Agent Loop Integration (`agent/loop.go`)

The agent loop runs hooks in this order each iteration:

1. **Load/create thread state** from thread store
2. **BeforeAgent** — run once (sequential, all hooks)
3. **Build tool map** — pre-registered + state-registered tools
4. **LLM-tool loop** (up to 25 iterations):
   - a. **ModifyRequest** — each hook transforms messages sequentially
   - b. **WrapModelCall** — onion ring around LLM call
   - c. **Execute tool calls** — parallel goroutines, each wrapped by onion ring
   - d. **Append results** to conversation
5. **Save final state** to thread store

---

## File Structure

```
wick_deep_agent/server/
├── agent/
│   └── hook.go              # Hook interface + BaseHook
│
├── hooks/
│   ├── filesystem.go        # FilesystemHook — 7 file tools + large result eviction
│   ├── skills.go            # SkillsHook — SKILL.md discovery + catalog injection
│   ├── memory.go            # MemoryHook — AGENTS.md loading + prompt injection
│   ├── summarization.go     # SummarizationHook — context compression
│   └── todolist.go          # TodoListHook — write_todos tool
│
├── tracing/
│   └── hook.go              # TracingHook — timing spans for LLM and tool calls
│
└── handlers/
    └── handlers.go          # Hook registration and composition
```

---

## Constants

| Constant | Value | Hook | Description |
|----------|-------|------|-------------|
| Max result chars | 80,000 | Filesystem | Truncation threshold for tool results |
| Eviction head/tail | 2,000 chars each | Filesystem | Preserved content when truncating |
| Context threshold | 85% of window | Summarization | Triggers compression |
| Default context window | 128,000 tokens | Summarization | Used when not configured |
| Token heuristic | `len / 4` | Summarization | Chars-to-tokens estimate |
| Summary max words | 2,000 | Summarization | Limit for summary output |
| Keep ratio | 10% (min 2) | Summarization | Recent messages preserved |
| Old message truncation | 2,000 chars | Summarization | write_file/edit_file content in old messages |
| Max loop iterations | 25 | Agent loop | Prevents runaway agent loops |
| Todo statuses | pending, in_progress, done | TodoList | Allowed status values |
