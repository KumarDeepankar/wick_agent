# hooks — Agent Middleware System

> **Reading order:** This document is structured bottom-up. It starts with the data types you'll see everywhere, then builds up to the interfaces that use them, the hook implementations, and finally how everything connects in the agent loop. Read it top to bottom and you'll never hit an undefined term.

---

## 1. Foundational Types

These types appear in every hook signature. Learn them first.

### 1.1 Message (`agent/messages.go`)

A single entry in the conversation. Every chat turn — system prompt, user input, LLM reply, tool output — is a `Message`.

```go
type Message struct {
    Role       string     `json:"role"`                  // one of: "system", "user", "assistant", "tool"
    Content    string     `json:"content"`
    ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // only set when Role == "assistant" (see §1.2)
    ToolCallID string     `json:"tool_call_id,omitempty"` // only set when Role == "tool" (links back to ToolCall.ID)
    Name       string     `json:"name,omitempty"`         // only set when Role == "tool" (which tool produced this)
}
```

**The four roles** (enforced — see §1.5):

| Role | Who creates it | Content | Extra fields |
|------|---------------|---------|-------------|
| `"system"` | Framework (hooks inject into this) | System prompt text | — |
| `"user"` | End user via API | User's question | — |
| `"assistant"` | LLM response | LLM's text reply | `ToolCalls` if LLM wants to call tools |
| `"tool"` | Framework after executing a tool | Tool's output string | `ToolCallID` + `Name` |

**Sample — a 4-message conversation in JSON:**

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

### 1.2 ToolCall (`agent/messages.go`)

When the LLM wants to use a tool, it returns one or more `ToolCall` values inside an assistant message's `ToolCalls` field (see §1.1 above).

```go
type ToolCall struct {
    ID      string         `json:"id"`       // unique ID (e.g. "call_abc123"), assigned by the LLM
    Name    string         `json:"name"`     // which tool to invoke (e.g. "read_file")
    Args    map[string]any `json:"args"`     // arguments the LLM passed (e.g. {"path": "/workspace/main.py"})
    RawArgs string         `json:"-"`        // raw JSON from LLM — not serialized, used for token estimation
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

### 1.3 ToolResult (`agent/messages.go`)

After a tool executes, the framework wraps its output in a `ToolResult`. This becomes a `"tool"` role message (see §1.1).

```go
type ToolResult struct {
    ToolCallID string `json:"tool_call_id"` // matches the ToolCall.ID that triggered this
    Name       string `json:"name"`         // tool name (e.g. "grep")
    Output     string `json:"output"`       // tool's output text
    Error      string `json:"error,omitempty"` // set only on failure — empty string on success
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

### 1.4 Todo (`agent/state.go`)

A single task item. Managed by the TodoListHook (see §6.5).

```go
type Todo struct {
    ID     string `json:"id"`
    Title  string `json:"title"`
    Status string `json:"status"` // "pending", "in_progress", or "done"
}
```

### 1.5 Role Constants and Validation (`agent/messages.go`)

The four role strings from §1.1 are defined as constants. The framework **enforces** them — unknown roles are rejected.

```go
const (
    RoleSystem    = "system"
    RoleUser      = "user"
    RoleAssistant = "assistant"
    RoleTool      = "tool"
)

// ValidRole returns true if r is one of the four known roles.
// Used by Messages.Validate() — unknown roles cause an error.
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

### 1.6 Messages Chain (`agent/messages.go`)

`Messages` is a `[]Message` with builder, filtering, validation, and token estimation methods. Used throughout hooks and the agent loop. All the types it uses (`Message`, `ToolCall`) are defined above in §1.1–§1.2.

**Builder helpers** — standalone constructors for single messages:

```go
agent.System("You are a coding assistant.")                    // → Message{Role: "system", Content: "..."}
agent.Human("Read the main.py file")                           // → Message{Role: "user", Content: "..."}
agent.AI("Sure!", tc1, tc2)                                    // → Message{Role: "assistant", Content: "...", ToolCalls: [...]}
agent.ToolMsg("call_abc123", "read_file", "file content...")   // → Message{Role: "tool", Content: "...", ToolCallID: "...", Name: "..."}
```

**Chain builder** — fluent API for constructing multi-message conversations:

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
chain.ByRole("user")      // generic filter by any role string
```

**Accessors:**

```go
chain.Last()        // last Message (zero-value Message{} if empty)
chain.LastContent() // Content of the last message
chain.Len()         // number of messages
chain.Slice()       // underlying []Message
```

**Validation** — enforced by the framework, not just conventions:

```go
// Validate() checks the full message chain:
//   - Unknown roles → error (only the four roles from §1.5 allowed)
//   - "tool" messages must have ToolCallID and Name
//   - "assistant" messages must have Content or ToolCalls (not both empty)
//   - "assistant" ToolCalls must each have ID and Name
//   - "user"/"system" messages must have non-empty Content
err := chain.Validate()

// ValidateUserInput() — stricter, for external/user-submitted messages:
//   - Only "user" and "system" roles allowed (see UserInputRole in §1.5)
//   - Must be non-empty
//   - Content must be non-empty
err := chain.ValidateUserInput()
```

**Token estimation** — used by SummarizationHook (§6.4) to decide when to compress:

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

---

## 2. Tool System

Tools are Go functions the LLM can call. Hooks register tools at runtime (see §6). Here are the types that make that possible.

### 2.1 Tool Interface (`agent/tool.go`)

Every tool — filesystem operations, todos, HTTP callbacks, your custom tools — implements this interface.

```go
type Tool interface {
    Name() string                                                    // unique name (e.g. "read_file")
    Description() string                                             // shown to LLM so it knows when to use it
    Parameters() map[string]any                                      // JSON Schema object describing expected args
    Execute(ctx context.Context, args map[string]any) (string, error) // runs the tool, returns output string
}
```

### 2.2 FuncTool (`agent/tool.go`)

The most common way to create a tool — wraps a plain Go function. Used by hooks (§6) to register tools.

```go
type FuncTool struct {
    ToolName   string                                                     // implements Tool.Name()
    ToolDesc   string                                                     // implements Tool.Description()
    ToolParams map[string]any                                             // implements Tool.Parameters()
    Fn         func(ctx context.Context, args map[string]any) (string, error) // implements Tool.Execute()
}
```

**Sample — a complete tool definition (this is how FilesystemHook registers `ls` in §6.1):**

```go
&agent.FuncTool{
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
}
```

### 2.3 RegisterToolOnState (`agent/tool.go`)

Hooks call this function during `BeforeAgent` (§4) to add tools to the current session. These tools live in `AgentState.toolRegistry` (§3.1) and are rebuilt every run.

```go
func RegisterToolOnState(state *AgentState, tool Tool)
```

### 2.4 HTTPTool (`agent/http_tool.go`)

An alternative to `FuncTool` — forwards tool calls to a remote HTTP endpoint instead of running a Go function. Used for external tool integrations registered via the API.

```go
type HTTPTool struct {
    ToolName    string
    ToolDesc    string
    ToolParams  map[string]any
    CallbackURL string         // e.g. "http://127.0.0.1:9100"
    Client      *http.Client
}
```

---

## 3. Agent State and Storage

Now that you know Message (§1.1), Todo (§1.4), and Tool (§2.1), here's how they're stored together.

### 3.1 AgentState (`agent/state.go`)

The full conversation state for a single thread. This is what hooks read from and write to.

```go
type AgentState struct {
    ThreadID     string            `json:"thread_id"`
    Messages     []Message         `json:"messages"`               // full conversation history (§1.1)
    Todos        []Todo            `json:"todos,omitempty"`         // task list managed by TodoListHook (§6.5)
    Files        map[string]string `json:"files,omitempty"`         // path → content of files written by agent
    toolRegistry map[string]Tool   `json:"-"`                       // NOT serialized — rebuilt each run by BeforeAgent hooks (§4)
}
```

**Sample — mid-conversation state as JSON:**

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
- `toolRegistry` is `json:"-"` — never saved. Rebuilt from scratch each run by `BeforeAgent` hooks: FilesystemHook (§6.1) adds 7 tools, TodoListHook (§6.5) adds 1
- `Files` tracks every file the agent wrote or edited — FilesystemHook updates this in its `WrapToolCall` (§6.1)
- `Todos` is the full todo list, replaced wholesale by the `write_todos` tool (§6.5)

### 3.2 ThreadStore (`agent/thread.go`)

In-memory store that persists `AgentState` (§3.1) across HTTP requests. A user can send multiple messages in the same thread — the store keeps the conversation alive between requests.

```go
type ThreadStore struct {
    mu      sync.RWMutex
    threads map[string]*threadEntry   // key = thread ID
    ttl     time.Duration             // default: 1 hour
    stop    chan struct{}
}

type threadEntry struct {
    state      *AgentState    // the conversation state (§3.1)
    lastAccess time.Time      // when this thread was last read or written
}

var GlobalThreadStore = NewThreadStore() // shared singleton
```

**Key methods:**

```go
ts.LoadOrCreate("th_8f3a2b") // returns existing AgentState or creates a new empty one
ts.Save("th_8f3a2b", state)  // persists state, refreshes lastAccess timestamp
ts.Get("th_8f3a2b")          // returns AgentState or nil if not found
ts.Delete("th_8f3a2b")       // removes thread permanently
```

**Eviction:** A background goroutine runs every 5 minutes and removes threads not accessed within the TTL (1 hour). This prevents unbounded memory growth.

---

## 4. The Hook Interface

Now that you know all the types hooks work with — Message (§1.1), ToolCall (§1.2), ToolResult (§1.3), AgentState (§3.1), and Tool (§2.1) — here's the interface every hook must implement.

### 4.1 Hook (`agent/hook.go`)

```go
type Hook interface {
    Name() string        // unique identifier (e.g. "filesystem", "memory")
    Phases() []string    // which phases this hook participates in (see table below)

    // Phase 1: One-time setup. Register tools on state, load files, etc.
    BeforeAgent(ctx context.Context, state *AgentState) error

    // Phase 2: Transform the message list before each LLM call.
    ModifyRequest(ctx context.Context, msgs []Message) ([]Message, error)

    // Phase 3: Wrap the LLM call. Call `next` to pass through to inner hooks / actual LLM.
    WrapModelCall(ctx context.Context, msgs []Message, next ModelCallWrapFunc) (*llm.Response, error)

    // Phase 4: Wrap a tool execution. Call `next` to pass through to inner hooks / actual tool.
    WrapToolCall(ctx context.Context, call ToolCall, next ToolCallFunc) (*ToolResult, error)
}
```

**The four phases:**

| Phase | When it runs | Pattern | Example use |
|-------|-------------|---------|-------------|
| `before_agent` | Once, before the loop starts | Sequential — each hook runs in order | Register tools, discover skills |
| `modify_request` | Before **each** LLM call | Sequential — each hook transforms msgs | Inject memory into system prompt |
| `wrap_model_call` | Around **each** LLM call | Onion ring (§5) | Compress context, record timing |
| `wrap_tool_call` | Around **each** tool execution | Onion ring (§5) | Truncate large outputs, record timing |

### 4.2 Function Signatures for Wrapping

These are the `next` function types. A hook calls `next(...)` to pass control to the next hook (or the actual LLM/tool at the center).

```go
// The "next" function a model-call wrapper receives.
// Call it to pass through to the next hook or the actual LLM.
type ModelCallWrapFunc func(ctx context.Context, msgs []Message) (*llm.Response, error)

// The "next" function a tool-call wrapper receives.
// Call it to pass through to the next hook or the actual tool execution.
type ToolCallFunc func(ctx context.Context, call ToolCall) (*ToolResult, error)
```

### 4.3 BaseHook (`agent/hook.go`)

A convenience struct with **no-op defaults** for all four methods. Embed it in your hook so you only override the phases you need.

```go
type BaseHook struct{}

func (BaseHook) Name() string                          { return "base" }
func (BaseHook) Phases() []string                      { return []string{"before_agent", "modify_request", "wrap_model_call", "wrap_tool_call"} }
func (BaseHook) BeforeAgent(_ context.Context, _ *AgentState) error { return nil }
func (BaseHook) ModifyRequest(_ context.Context, msgs []Message) ([]Message, error) { return msgs, nil }
func (BaseHook) WrapModelCall(_ context.Context, msgs []Message, next ModelCallWrapFunc) (*llm.Response, error) { return next(ctx, msgs) }
func (BaseHook) WrapToolCall(_ context.Context, call ToolCall, next ToolCallFunc) (*ToolResult, error) { return next(ctx, call) }
```

**Every hook in §6 embeds `BaseHook`** and overrides only what it needs. For example, `TodoListHook` only overrides `BeforeAgent` — all other methods pass through via `BaseHook`.

---

## 5. Onion Ring Composition

Now you know the Hook interface (§4). Here's how multiple hooks are composed into a chain.

### 5.1 How It Works (`agent/loop.go`)

Hooks are iterated in **reverse** to build the chain. The first hook in the array becomes the **outermost** wrapper:

```go
fn := baseLLMCall  // the actual LLM call function
for i := len(hooks) - 1; i >= 0; i-- {
    hook := hooks[i]
    prev := fn
    fn = func(ctx context.Context, msgs []Message) (*llm.Response, error) {
        return hook.WrapModelCall(ctx, msgs, prev)  // each hook wraps the previous
    }
}
// Result: hook[0] wraps hook[1] wraps ... wraps hook[n] wraps baseLLMCall
```

Same pattern applies to `WrapToolCall`.

**Visual — what happens when the LLM is called:**

```
TracingHook.WrapModelCall(msgs, next=↓)     ← outermost (runs first, finishes last)
  SummarizationHook.WrapModelCall(msgs, next=↓)
    actual LLM call                          ← center
  SummarizationHook returns
TracingHook returns                          ← outermost finishes (records total time)
```

### 5.2 Registration Order (`handlers/handlers.go`)

Hooks are appended in this order. Position matters — it determines the onion ring layering:

```go
agentHooks := []agent.Hook{
    tracing.NewTracingHook(),                         // 1. outermost wrapper (times everything)
    hooks.NewTodoListHook(),                          // 2. always active
    hooks.NewFilesystemHook(backend),                 // 3. only when backend is configured
    hooks.NewSkillsHook(backend, cfg.Skills.Paths),   // 4. only when skills.paths is set
    hooks.NewMemoryHook(backend, cfg.Memory.Paths),   // 5. only when memory.paths is set
    hooks.NewSummarizationHook(llmClient, ctxWindow), // 6. innermost wrapper (closest to LLM)
}
```

---

## 6. Hook Implementations

Each section below describes one hook. They all embed `BaseHook` (§4.3) and override only the phases they need.

### 6.1 FilesystemHook (`hooks/filesystem.go`)

Registers file-operation tools and evicts oversized tool results.

**Phases:** `before_agent`, `wrap_tool_call`

**Struct:**

```go
type FilesystemHook struct {
    agent.BaseHook              // no-op defaults for unused phases (§4.3)
    fs          wickfs.FileSystem
    workdir     string
    resolvePath func(string) (string, error)
}

func NewFilesystemHook(b backend.Backend) *FilesystemHook
```

**BeforeAgent — registers 7 tools** on `AgentState` (§3.1) via `RegisterToolOnState()` (§2.3):

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

**WrapToolCall — large result eviction.** Truncates tool results exceeding 80,000 characters (~20k tokens):

```
[first 2000 chars]

... (truncated X characters) ...

[last 2000 chars]
```

**Excluded tools** (never evicted): `ls`, `glob`, `grep`, `read_file`, `edit_file`, `write_file`

Only `execute` and any custom tools are subject to eviction.

### 6.2 SkillsHook (`hooks/skills.go`)

Discovers skill definitions and injects a catalog into the system prompt for progressive loading.

**Phases:** `before_agent`, `modify_request`

**Struct:**

```go
type SkillsHook struct {
    agent.BaseHook              // §4.3
    backend backend.Backend
    paths   []string            // configured skill directory paths
    skills  []SkillEntry        // populated during BeforeAgent
}

type SkillEntry struct {
    Name        string // from YAML frontmatter "name" field
    Description string // from YAML frontmatter "description" field
    Path        string // full path to SKILL.md file
}

func NewSkillsHook(b backend.Backend, paths []string) *SkillsHook
```

**BeforeAgent — skill discovery:**

1. Runs `find` on each configured skill directory path
2. Locates `SKILL.md` files in subdirectories
3. Parses YAML frontmatter (`---\nname: ...\ndescription: ...\n---`)
4. Stores `SkillEntry{Name, Description, Path}` for each skill

**Sample — after BeforeAgent scans `/workspace/skills/`:**

```go
[]SkillEntry{
    {Name: "csv-analyzer", Description: "Analyze CSV files and generate charts", Path: "/workspace/skills/csv-analyzer/SKILL.md"},
    {Name: "code-review",  Description: "Review code for bugs and style issues",  Path: "/workspace/skills/code-review/SKILL.md"},
}
```

**ModifyRequest — catalog injection.** Appends a catalog to the system message (§1.1, role `"system"`):

```
[skill-name] description → Read /path/to/SKILL.md for full instructions
```

**Progressive loading:** Only metadata appears in the prompt. The agent calls `read_file` on the SKILL.md path when it needs the full skill instructions. This saves tokens by avoiding upfront inclusion of all skill content.

### 6.3 MemoryHook (`hooks/memory.go`)

Loads persistent agent memory from AGENTS.md files and injects it into the system prompt.

**Phases:** `before_agent`, `modify_request`

**Struct:**

```go
type MemoryHook struct {
    agent.BaseHook              // §4.3
    backend       backend.Backend
    paths         []string      // configured AGENTS.md file paths
    memoryContent string        // loaded content, populated during BeforeAgent
}

func NewMemoryHook(b backend.Backend, paths []string) *MemoryHook
```

**BeforeAgent — load memory:**

- Reads AGENTS.md files from configured paths via `cat` command
- Concatenates multiple files with `\n\n---\n\n` separator
- Missing files are silently skipped

**ModifyRequest — memory injection.** Wraps loaded content in XML tags and appends to the system message (§1.1, role `"system"`):

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

### 6.4 SummarizationHook (`hooks/summarization.go`)

Compresses conversation history when approaching the context window limit.

**Phases:** `wrap_model_call`

**Struct:**

```go
type SummarizationHook struct {
    agent.BaseHook              // §4.3
    llmClient     llm.Client   // used to call the LLM for summarization (§7.1)
    contextWindow int          // total context window in tokens (default: 128,000)
}

func NewSummarizationHook(client llm.Client, contextWindow int) *SummarizationHook
```

**WrapModelCall — context compression.**

**Trigger:** Total estimated tokens > 85% of context window. Token estimation uses the `len(content) / 4` heuristic (same as `Messages.EstimateTokens()` in §1.6).

**When triggered:**
1. Splits messages into old (to summarize) and recent (to keep)
2. Recent = last 10% of messages (minimum 2)
3. Truncates `write_file`/`edit_file` content in old messages to 2,000 chars
4. Calls LLM with summarization prompt requesting <2,000 word summary
5. Replaces old messages with a single summary message
6. Passes summarized + recent messages to `next` (the inner hook or actual LLM)

**Graceful degradation:** If the summarization LLM call fails, messages pass through unchanged to `next`.

### 6.5 TodoListHook (`hooks/todolist.go`)

Provides task tracking via a `write_todos` tool.

**Phases:** `before_agent`

**Struct:**

```go
type TodoListHook struct {
    agent.BaseHook    // §4.3 — only BeforeAgent is overridden
}

func NewTodoListHook() *TodoListHook
```

**BeforeAgent — tool registration.** Registers the `write_todos` tool on `AgentState` (§3.1) via `RegisterToolOnState()` (§2.3). Initializes `state.Todos` as an empty slice.

**The `write_todos` tool.** Takes a complete todo list and replaces `state.Todos` (§1.4):

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

Todos are persisted in `AgentState.Todos` and saved to the thread store (§3.2).

### 6.6 TracingHook (`tracing/hook.go`)

Records timing and debug information for observability. Uses the tracing interfaces defined in §7.2.

**Phases:** `wrap_model_call`, `wrap_tool_call`

**Struct:**

```go
type TracingHook struct {
    agent.BaseHook    // §4.3 — only WrapModelCall and WrapToolCall are overridden
}

func NewTracingHook() *TracingHook
```

**WrapModelCall** — creates span `"llm.call"` and records:
- `message_count`, `content_length`, `tool_calls_count`
- First 500 chars of content and tool call names
- Errors if present

**WrapToolCall** — creates span `"tool.call"` and records:
- `tool_name`, `tool_call_id`, `tool_args`
- `output_length`, first 500 chars of output
- Errors if present

**No-op** when trace recorder is nil (reads from context — see §7.2).

**Sample — what TracingHook records for a tool call:**

```go
span := trace.StartSpan("tool.call")
span.Set("tool_name", "read_file")
span.Set("tool_call_id", "call_abc123")
span.Set("tool_args", `{"path":"/workspace/main.py"}`)
// ... calls next(ctx, call) to execute the tool ...
span.Set("output_length", 342)
span.Set("output_preview", "from fastapi import FastAPI\napp = FastAPI()...")  // first 500 chars
span.End()
```

---

## 7. Supporting Types

Types referenced by hooks but not part of the core hook flow.

### 7.1 LLM Client (`llm/client.go`)

The LLM client interface used by SummarizationHook (§6.4) and the agent loop (§8).

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

**Note:** `llm.ToolCallResult.Args` uses the JSON key `"arguments"` (OpenAI convention), while `agent.ToolCall.Args` (§1.2) uses `"args"` (internal convention). The agent loop maps between them.

### 7.2 Tracing Interfaces (`agent/trace_iface.go`)

Used by TracingHook (§6.6). Pulled from context — if no recorder is set, tracing is a no-op.

```go
type TraceRecorder interface {
    StartSpan(name string) SpanHandle
    RecordEvent(name string, metadata map[string]any)
}

type SpanHandle interface {
    Set(key string, value any) SpanHandle  // chainable
    End()                                   // marks span as complete, records duration
}

// Context helpers
func WithTraceRecorder(ctx context.Context, tr TraceRecorder) context.Context
func TraceFromContext(ctx context.Context) TraceRecorder  // returns nil if not set
```

### 7.3 StreamEvent (`agent/events.go`)

Events emitted from the agent loop (§8) to the SSE handler during streaming. Not directly used by hooks, but useful to understand the full picture.

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

### 7.4 Agent Configuration (`agent/config.go`)

The YAML/JSON config that controls which hooks are activated. This is what `handlers.go` reads to decide which hooks to create (§5.2).

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
context_window: 128000          # → SummarizationHook (§6.4) uses this
backend:
  type: docker                  # → FilesystemHook (§6.1) activated (needs a backend)
  image: python:3.12-slim
  workdir: /workspace
skills:
  paths:                        # → SkillsHook (§6.2) activated
    - /workspace/skills
memory:
  paths:                        # → MemoryHook (§6.3) activated
    - /workspace/AGENTS.md
```

**Hook activation rules:**

| Hook | When it's created |
|------|------------------|
| TracingHook (§6.6) | Always |
| TodoListHook (§6.5) | Always |
| FilesystemHook (§6.1) | Only when `backend` is configured (non-nil) |
| SkillsHook (§6.2) | Only when `skills.paths` has entries **and** backend exists |
| MemoryHook (§6.3) | Only when `memory.paths` has entries **and** backend exists |
| SummarizationHook (§6.4) | Always |

---

## 8. Agent Loop Integration (`agent/loop.go`)

This is where everything comes together. The loop uses all the types and hooks described above.

```
1. Load or create AgentState (§3.1) from ThreadStore (§3.2)

2. Run BeforeAgent (§4.1 phase 1) — once, sequential, all hooks:
   - FilesystemHook (§6.1) registers 7 tools via RegisterToolOnState (§2.3)
   - TodoListHook (§6.5) registers write_todos tool
   - SkillsHook (§6.2) discovers SKILL.md files
   - MemoryHook (§6.3) loads AGENTS.md files

3. Build tool map from pre-registered tools + state.toolRegistry (§3.1)

4. LLM-tool loop (up to 25 iterations):

   a. ModifyRequest (§4.1 phase 2) — each hook transforms Messages (§1.6) sequentially:
      - SkillsHook (§6.2) injects skill catalog into system message
      - MemoryHook (§6.3) injects <agent_memory> into system message

   b. WrapModelCall (§4.1 phase 3) — onion ring (§5) around the LLM call:
      - TracingHook (§6.6) starts timing span
      - SummarizationHook (§6.4) compresses old messages if needed
      - Actual LLM call via llm.Client (§7.1)
      - Returns llm.Response with Content and/or ToolCalls

   c. If no ToolCalls in response → break (conversation turn is done)

   d. Execute ToolCalls (§1.2) in parallel goroutines, each wrapped by WrapToolCall onion ring (§5):
      - TracingHook (§6.6) records timing
      - FilesystemHook (§6.1) truncates large results
      - Tool.Execute() (§2.1) runs the actual function
      - Returns ToolResult (§1.3)

   e. Append ToolResults as "tool" role Messages (§1.1) to conversation

5. Save final AgentState (§3.1) to ThreadStore (§3.2)
```

---

## 9. Overview Diagram

Now that you know all the pieces, here's how they fit together:

```
Agent loop (§8)
  │
  ├── BeforeAgent          (one-time setup, sequential)
  │     FilesystemHook     → registers 7 file tools on state (§6.1)
  │     TodoListHook       → registers write_todos tool (§6.5)
  │     SkillsHook         → discovers SKILL.md files (§6.2)
  │     MemoryHook         → loads AGENTS.md files (§6.3)
  │
  ├── ModifyRequest        (before each LLM call, sequential)
  │     SkillsHook         → injects skill catalog into system message (§6.2)
  │     MemoryHook         → injects <agent_memory> into system message (§6.3)
  │
  ├── WrapModelCall        (onion ring around LLM call — §5)
  │     TracingHook        → outermost: records timing spans (§6.6)
  │     SummarizationHook  → innermost: compresses old messages (§6.4)
  │     actual LLM call    → center
  │
  └── WrapToolCall         (onion ring around tool execution — §5)
        TracingHook        → outermost: records timing spans (§6.6)
        FilesystemHook     → evicts results >80k chars (§6.1)
        actual tool exec   → center
```

---

## 10. File Structure

```
wick_deep_agent/server/
├── agent/
│   ├── state.go             # AgentState (§3.1), Todo (§1.4)
│   ├── config.go            # AgentConfig (§7.4), BackendCfg, SkillsCfg, MemoryCfg, SubAgentCfg, AgentInfo
│   ├── events.go            # StreamEvent (§7.3)
│   ├── messages.go          # Message (§1.1), ToolCall (§1.2), ToolResult (§1.3), Messages chain (§1.6), role constants (§1.5), validation, builders
│   ├── hook.go              # Hook interface (§4.1), BaseHook (§4.3), ModelCallWrapFunc, ToolCallFunc (§4.2)
│   ├── tool.go              # Tool interface (§2.1), FuncTool (§2.2), ToolRegistry, RegisterToolOnState (§2.3)
│   ├── http_tool.go         # HTTPTool (§2.4) — forwards tool calls to remote HTTP callback
│   ├── loop.go              # Agent struct, Run/RunStream, onion ring build (§5.1), LLM-tool loop (§8)
│   ├── registry.go          # Registry (templates + per-user instances), Template, Instance, HookOverrides
│   ├── thread.go            # ThreadStore (§3.2) — in-memory, 1h TTL, 5min eviction, GlobalThreadStore
│   └── trace_iface.go       # TraceRecorder / SpanHandle interfaces (§7.2), context helpers
│
├── hooks/
│   ├── filesystem.go        # FilesystemHook (§6.1) — 7 file tools + large result eviction
│   ├── skills.go            # SkillsHook (§6.2) — SKILL.md discovery + catalog injection
│   ├── memory.go            # MemoryHook (§6.3) — AGENTS.md loading + prompt injection
│   ├── summarization.go     # SummarizationHook (§6.4) — context compression
│   └── todolist.go          # TodoListHook (§6.5) — write_todos tool
│
├── tracing/
│   ├── hook.go              # TracingHook (§6.6) — timing spans for LLM and tool calls
│   └── trace.go             # Concrete TraceRecorder / SpanHandle implementation
│
└── handlers/
    ├── handlers.go          # HTTP routes, hook registration and composition (§5.2), agent creation
    ├── events.go            # EventBus — SSE event fan-out to connected clients
    ├── backends.go          # BackendStore — per-user backend lifecycle (create/get/stop/restart)
    ├── builtin_tools.go     # NewBuiltinTools() — built-in tools registered outside hooks
    └── tool_store.go        # ToolStore — unified store for HTTPTool (§2.4) + native agent.Tool (§2.1)
```

---

## 11. Constants Reference

| Constant | Value | Where | Description |
|----------|-------|-------|-------------|
| Max result chars | 80,000 | FilesystemHook (§6.1) | Truncation threshold for tool results |
| Eviction head/tail | 2,000 chars each | FilesystemHook (§6.1) | Preserved content when truncating |
| Context threshold | 85% of window | SummarizationHook (§6.4) | Triggers compression |
| Default context window | 128,000 tokens | SummarizationHook (§6.4) | Used when `context_window` not set in config |
| Token heuristic | `len / 4` | SummarizationHook (§6.4) | Chars-to-tokens estimate |
| Summary max words | 2,000 | SummarizationHook (§6.4) | Limit for summary output |
| Keep ratio | 10% (min 2) | SummarizationHook (§6.4) | Recent messages preserved during compression |
| Old message truncation | 2,000 chars | SummarizationHook (§6.4) | write_file/edit_file content in old messages |
| Max loop iterations | 25 | Agent loop (§8) | Prevents runaway agent loops |
| Thread TTL | 1 hour | ThreadStore (§3.2) | Idle threads evicted after this duration |
| Eviction interval | 5 minutes | ThreadStore (§3.2) | How often the eviction goroutine runs |
| Todo statuses | pending, in_progress, done | TodoListHook (§6.5) | Allowed status values |
