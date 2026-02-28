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

### Function Signatures

```go
// The "next" function a model-call wrapper receives. Call it to pass through.
type ModelCallWrapFunc func(ctx context.Context, msgs []Message) (*llm.Response, error)

// The "next" function a tool-call wrapper receives. Call it to execute the tool.
type ToolCallFunc func(ctx context.Context, call ToolCall) (*ToolResult, error)
```

---

## Core Data Structures

These are the types that flow through hooks. Understanding them is essential.

### Message (`agent/types.go`)

Every message in the conversation — system prompts, user inputs, assistant replies, and tool results.

```go
type Message struct {
    Role       string     `json:"role"`                  // "system", "user", "assistant", "tool"
    Content    string     `json:"content"`
    ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // set when Role == "assistant" and LLM wants to call tools
    ToolCallID string     `json:"tool_call_id,omitempty"` // set when Role == "tool" (matches ToolCall.ID)
    Name       string     `json:"name,omitempty"`         // tool name when Role == "tool"
}
```

**Sample — a 4-message conversation:**

```json
[
  {
    "role": "system",
    "content": "You are a coding assistant.\n\n<agent_memory>\n# Project notes\n- Using FastAPI\n</agent_memory>"
  },
  {
    "role": "user",
    "content": "Read the main.py file"
  },
  {
    "role": "assistant",
    "content": "",
    "tool_calls": [
      {
        "id": "call_abc123",
        "name": "read_file",
        "args": {"path": "/workspace/main.py"}
      }
    ]
  },
  {
    "role": "tool",
    "content": "from fastapi import FastAPI\napp = FastAPI()\n\n@app.get('/')\ndef root():\n    return {'status': 'ok'}",
    "tool_call_id": "call_abc123",
    "name": "read_file"
  }
]
```

**Role constants and validation (`agent/messages.go`):**

```go
const (
    RoleSystem    = "system"
    RoleUser      = "user"
    RoleAssistant = "assistant"
    RoleTool      = "tool"
)

// ValidRole returns true if r is one of the four known roles.
// Used by Validate() — unknown roles are rejected.
func ValidRole(r string) bool {
    switch r {
    case RoleSystem, RoleUser, RoleAssistant, RoleTool:
        return true
    }
    return false
}

// UserInputRole returns true if the role is allowed in user-submitted messages.
// Only "user" and "system" — blocks "assistant" and "tool" from external input.
func UserInputRole(r string) bool {
    return r == RoleUser || r == RoleSystem
}
```

**Builder helpers** — used in hooks and tests to construct messages:

```go
agent.System("You are a coding assistant.")
agent.Human("Read the main.py file")
agent.AI("", agent.ToolCall{ID: "call_abc123", Name: "read_file", Args: map[string]any{"path": "/workspace/main.py"}})
agent.ToolMsg("call_abc123", "read_file", "file content here...")
```

### Messages Chain (`agent/messages.go`)

`Messages` is an ordered `[]Message` with builder, filtering, validation, and token estimation methods. Used throughout hooks and the agent loop.

**Chain builder** — fluent API for constructing conversations:

```go
chain := agent.NewMessages().
    System("You are helpful.").
    Human("What is 2+2?")

// After LLM responds with a tool call:
chain = chain.
    AI("", agent.ToolCall{ID: "call_1", Name: "calculate", Args: map[string]any{"expr": "2+2"}}).
    Tool("call_1", "calculate", "4").
    AI("2+2 = 4")
```

**Filtering:**

```go
chain.UserMessages()      // only role == "user"
chain.AssistantMessages() // only role == "assistant"
chain.ToolMessages()      // only role == "tool"
chain.SystemMessages()    // only role == "system"
chain.ByRole("user")      // generic filter
```

**Accessors:**

```go
chain.Last()        // last Message (zero value if empty)
chain.LastContent() // last message's Content string
chain.Len()         // number of messages
chain.Slice()       // underlying []Message
```

**Validation** — enforced by the framework, not just conventions:

```go
// Validate() checks the full message chain:
//   - Unknown roles → error (only system/user/assistant/tool allowed)
//   - "tool" messages must have ToolCallID and Name
//   - "assistant" messages must have Content or ToolCalls (not both empty)
//   - "assistant" ToolCalls must each have ID and Name
//   - "user"/"system" messages must have non-empty Content
err := chain.Validate()

// ValidateUserInput() — stricter check for external/user-submitted messages:
//   - Only "user" and "system" roles allowed
//   - Must be non-empty
//   - Content must be non-empty
err := chain.ValidateUserInput()
```

**Token estimation** — used by SummarizationHook to decide when to compress:

```go
tokens := chain.EstimateTokens() // heuristic: len(content)/4 + len(rawArgs)/4
```

**Pretty printing:**

```go
fmt.Print(chain.PrettyPrint())
// Output:
// [System]
// You are helpful.
//
// [Human]
// What is 2+2?
//
// [AI]
//   → tool_call: calculate(id=call_1, args=map[expr:2+2])
//
// [Tool: calculate (call_id=call_1)]
// 4
//
// [AI]
// 2+2 = 4
```

### ToolCall (`agent/types.go`)

The LLM's request to invoke a tool. Attached to assistant messages.

```go
type ToolCall struct {
    ID      string         `json:"id"`
    Name    string         `json:"name"`
    Args    map[string]any `json:"args"`
    RawArgs string         `json:"-"` // raw JSON string from LLM, not serialized
}
```

**Sample — LLM asks to edit a file:**

```json
{
  "id": "call_xyz789",
  "name": "edit_file",
  "args": {
    "path": "/workspace/main.py",
    "old_text": "return {'status': 'ok'}",
    "new_text": "return {'status': 'ok', 'version': '1.0'}"
  }
}
```

### ToolResult (`agent/types.go`)

The output of executing a tool. Becomes a `"tool"` role message.

```go
type ToolResult struct {
    ToolCallID string `json:"tool_call_id"`
    Name       string `json:"name"`
    Output     string `json:"output"`
    Error      string `json:"error,omitempty"` // set only on failure
}
```

**Sample — successful grep:**

```json
{
  "tool_call_id": "call_grep01",
  "name": "grep",
  "output": "{\"matches\":[{\"file\":\"/workspace/main.py\",\"line\":4,\"text\":\"def root():\"}],\"truncated\":false}"
}
```

**Sample — failed edit:**

```json
{
  "tool_call_id": "call_edit01",
  "name": "edit_file",
  "output": "",
  "error": "old_text not found in file"
}
```

### AgentState (`agent/types.go`)

The full conversation state for a thread. Persisted to thread store between requests.

```go
type AgentState struct {
    ThreadID     string            `json:"thread_id"`
    Messages     []Message         `json:"messages"`
    Todos        []Todo            `json:"todos,omitempty"`
    Files        map[string]string `json:"files,omitempty"` // path → content of files written by agent
    toolRegistry map[string]Tool   `json:"-"`               // not serialized — rebuilt each run by BeforeAgent hooks
}
```

**Sample — mid-conversation state:**

```json
{
  "thread_id": "th_8f3a2b",
  "messages": [
    {"role": "system", "content": "You are a coding assistant."},
    {"role": "user", "content": "Create a hello.py file"},
    {"role": "assistant", "content": "", "tool_calls": [
      {"id": "call_w01", "name": "write_file", "args": {"path": "/workspace/hello.py", "content": "print('hello')"}}
    ]},
    {"role": "tool", "content": "{\"path\":\"/workspace/hello.py\",\"bytes_written\":14}", "tool_call_id": "call_w01", "name": "write_file"},
    {"role": "assistant", "content": "Created hello.py with a simple print statement."}
  ],
  "todos": [
    {"id": "1", "title": "Create hello.py", "status": "done"},
    {"id": "2", "title": "Add unit tests", "status": "pending"}
  ],
  "files": {
    "/workspace/hello.py": "print('hello')"
  }
}
```

**Key points:**
- `toolRegistry` is `json:"-"` — rebuilt from scratch each run by `BeforeAgent` hooks (FilesystemHook adds 7 tools, TodoListHook adds 1)
- `Files` tracks every file the agent wrote or edited — hooks update this in `WrapToolCall`
- `Todos` is the full todo list, replaced wholesale by the `write_todos` tool

### Todo (`agent/types.go`)

A single task item managed by the TodoListHook.

```go
type Todo struct {
    ID     string `json:"id"`
    Title  string `json:"title"`
    Status string `json:"status"` // "pending", "in_progress", "done"
}
```

### Tool Interface (`agent/tool.go`)

Every tool (filesystem, todos, HTTP callbacks, custom) implements this interface.

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]any // JSON Schema object
    Execute(ctx context.Context, args map[string]any) (string, error)
}
```

**FuncTool** — wraps a plain Go function as a Tool (used by hooks):

```go
type FuncTool struct {
    ToolName   string
    ToolDesc   string
    ToolParams map[string]any
    Fn         func(ctx context.Context, args map[string]any) (string, error)
}
```

**Sample — how FilesystemHook registers the `ls` tool internally:**

```go
agent.RegisterToolOnState(state, &agent.FuncTool{
    ToolName: "ls",
    ToolDesc: "List files and directories at the given path",
    ToolParams: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "path": map[string]any{
                "type":        "string",
                "description": "Directory path to list",
            },
        },
        "required": []string{"path"},
    },
    Fn: func(ctx context.Context, args map[string]any) (string, error) {
        path := args["path"].(string)
        entries, err := fs.Ls(ctx, path)
        // ... marshal to JSON and return
    },
})
```

### LLM Types (`llm/client.go`)

The LLM client interface and request/response types that hooks interact with.

```go
type Client interface {
    Call(ctx context.Context, req Request) (*Response, error)
    Stream(ctx context.Context, req Request, ch chan<- StreamChunk) error
}

type Request struct {
    Model        string       `json:"model"`
    Messages     []Message    `json:"messages"`
    Tools        []ToolSchema `json:"tools,omitempty"`
    SystemPrompt string       `json:"system_prompt,omitempty"`
    MaxTokens    int          `json:"max_tokens,omitempty"`
    Temperature  *float64     `json:"temperature,omitempty"`
}

type Response struct {
    Content   string           `json:"content"`
    ToolCalls []ToolCallResult `json:"tool_calls,omitempty"`
}

type ToolSchema struct {
    Name        string         `json:"name"`
    Description string         `json:"description"`
    Parameters  map[string]any `json:"parameters"` // JSON Schema
}

type ToolCallResult struct {
    ID   string         `json:"id"`
    Name string         `json:"name"`
    Args map[string]any `json:"arguments"` // note: "arguments" not "args"
}
```

**Note:** `llm.ToolCallResult.Args` uses the JSON key `"arguments"` (OpenAI convention), while `agent.ToolCall.Args` uses `"args"` (internal convention). The agent loop maps between them.

### StreamEvent (`agent/types.go`)

Events emitted from the agent loop to the SSE handler during streaming.

```go
type StreamEvent struct {
    Event    string `json:"event"`              // "on_chat_model_stream", "on_tool_start", "on_tool_end", "done", "error"
    Name     string `json:"name,omitempty"`     // tool name or model name
    RunID    string `json:"run_id,omitempty"`
    Data     any    `json:"data,omitempty"`
    ThreadID string `json:"thread_id,omitempty"` // set on "done" event
}
```

**Sample — events emitted during one tool-call cycle:**

```json
{"event": "on_chat_model_stream", "data": {"delta": "Let me read"}}
{"event": "on_chat_model_stream", "data": {"delta": " that file."}}
{"event": "on_tool_start", "name": "read_file", "data": {"args": {"path": "/workspace/main.py"}}}
{"event": "on_tool_end",   "name": "read_file", "data": {"output": "from fastapi import FastAPI\n..."}}
{"event": "on_chat_model_stream", "data": {"delta": "The file contains a FastAPI app."}}
{"event": "done", "thread_id": "th_8f3a2b"}
```

### SkillEntry (`hooks/skills.go`)

A discovered skill from SKILL.md frontmatter.

```go
type SkillEntry struct {
    Name        string // from YAML frontmatter "name" field
    Description string // from YAML frontmatter "description" field
    Path        string // full path to SKILL.md file
}
```

**Sample — after BeforeAgent scans `/workspace/skills/`:**

```go
[]SkillEntry{
    {Name: "csv-analyzer", Description: "Analyze CSV files and generate charts", Path: "/workspace/skills/csv-analyzer/SKILL.md"},
    {Name: "code-review",  Description: "Review code for bugs and style issues",  Path: "/workspace/skills/code-review/SKILL.md"},
}
```

### Tracing Types (`agent/trace_iface.go`)

The tracing interface that TracingHook uses. Pulled from context.

```go
type TraceRecorder interface {
    StartSpan(name string) SpanHandle
    RecordEvent(name string, metadata map[string]any)
}

type SpanHandle interface {
    Set(key string, value any) SpanHandle
    End()
}
```

**Sample — what TracingHook records for a tool call:**

```go
span := trace.StartSpan("tool.call")
span.Set("tool_name", "read_file")
span.Set("tool_call_id", "call_abc123")
span.Set("tool_args", `{"path":"/workspace/main.py"}`)
// ... execute tool ...
span.Set("output_length", 342)
span.Set("output_preview", "from fastapi import FastAPI\napp = FastAPI()...")  // first 500 chars
span.End()
```

### Agent Configuration (`agent/types.go`)

The YAML/JSON config that controls which hooks are activated.

```go
type AgentConfig struct {
    Name          string         `yaml:"name" json:"name"`
    Model         any            `yaml:"model" json:"model"`
    SystemPrompt  string         `yaml:"system_prompt" json:"system_prompt"`
    Tools         []string       `yaml:"tools" json:"tools"`
    Backend       *BackendCfg    `yaml:"backend" json:"backend"`
    Skills        *SkillsCfg     `yaml:"skills" json:"skills"`
    Memory        *MemoryCfg     `yaml:"memory" json:"memory"`
    ContextWindow int            `yaml:"context_window" json:"context_window"`
    // ... other fields omitted for brevity
}

type SkillsCfg struct {
    Paths []string `yaml:"paths" json:"paths"`
}

type MemoryCfg struct {
    Paths []string `yaml:"paths" json:"paths"`
}
```

**Sample `agents.yaml` — shows what triggers each hook:**

```yaml
name: code-assistant
model: claude-sonnet-4-20250514
system_prompt: "You are a coding assistant."
context_window: 128000          # → SummarizationHook uses this
backend:
  type: docker                  # → FilesystemHook activated (needs a backend)
  image: python:3.12-slim
  workdir: /workspace
skills:
  paths:                        # → SkillsHook activated
    - /workspace/skills
memory:
  paths:                        # → MemoryHook activated
    - /workspace/AGENTS.md
```

**Hook activation rules:**
- `TracingHook` — always active
- `TodoListHook` — always active
- `FilesystemHook` — only when `backend` is configured (non-nil)
- `SkillsHook` — only when `skills.paths` has entries and backend exists
- `MemoryHook` — only when `memory.paths` has entries and backend exists
- `SummarizationHook` — always active

### Thread Store (`agent/thread.go`)

In-memory store for persisting `AgentState` across requests. TTL-based eviction.

```go
type ThreadStore struct {
    mu      sync.RWMutex
    threads map[string]*threadEntry
    ttl     time.Duration            // default: 1 hour
    stop    chan struct{}
}

type threadEntry struct {
    state      *AgentState
    lastAccess time.Time
}

var GlobalThreadStore = NewThreadStore()
```

**Key methods:**

```go
ts.LoadOrCreate("th_8f3a2b") // returns existing state or creates empty one
ts.Save("th_8f3a2b", state)  // persists state, updates lastAccess
ts.Get("th_8f3a2b")          // returns state or nil
ts.Delete("th_8f3a2b")       // removes thread
```

Eviction runs every 5 minutes — removes threads not accessed within the TTL (1 hour).

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
│   ├── hook.go              # Hook interface + BaseHook
│   ├── types.go             # Message, ToolCall, ToolResult, AgentState, Todo, StreamEvent
│   └── messages.go          # Messages chain, role constants, validation, builders, token estimation
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
