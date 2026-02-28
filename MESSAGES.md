# Messages — Conversation Data Model

> **What this covers:** The `Message` struct, the `Messages` chain type, role enforcement, validation, and how messages flow through the agent loop. Source files: `agent/types.go` (structs) and `agent/messages.go` (chain + validation).
>
> **Go concepts used:** structs, slices, methods on named types, variadic functions (`...`), `append`, `switch`, `fmt.Errorf`, JSON tags, `strings.Builder`. If any of these are unfamiliar, see [GO_BASICS.md](GO_BASICS.md).

---

## 1. What is a Message?

An LLM conversation is a list of messages. Each message has a **role** (who said it) and **content** (what they said). Some messages also carry tool calls or tool results.

```go
// agent/types.go

type Message struct {
    Role       string     `json:"role"`                  // who: "system", "user", "assistant", or "tool"
    Content    string     `json:"content"`               // what they said
    ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // LLM wants to call tools (only when Role == "assistant")
    ToolCallID string     `json:"tool_call_id,omitempty"` // which tool call this answers (only when Role == "tool")
    Name       string     `json:"name,omitempty"`         // tool name (only when Role == "tool")
}
```

### 1.1 The Four Roles

There are exactly four roles. The framework enforces this — you cannot use any other value (see §4).

| Role | Who creates it | What goes in Content | Extra fields used |
|------|---------------|---------------------|-------------------|
| `"system"` | Framework / hooks | Instructions for the LLM ("You are a coding assistant...") | — |
| `"user"` | End user via API | The user's question or request | — |
| `"assistant"` | LLM response | The LLM's text reply (can be `""` if it only calls tools) | `ToolCalls` |
| `"tool"` | Framework after running a tool | The tool's output string | `ToolCallID`, `Name` |

### 1.2 ToolCall and ToolResult

These two structs connect assistant messages to tool messages:

```go
// agent/types.go

// The LLM says: "I want to call this tool with these arguments"
type ToolCall struct {
    ID      string         `json:"id"`       // unique ID assigned by the LLM (e.g. "call_abc123")
    Name    string         `json:"name"`     // which tool (e.g. "read_file")
    Args    map[string]any `json:"args"`     // arguments (e.g. {"path": "/workspace/main.py"})
    RawArgs string         `json:"-"`        // raw JSON from LLM — not saved, used for token counting
}

// After the tool runs, the framework wraps the output:
type ToolResult struct {
    ToolCallID string `json:"tool_call_id"` // matches ToolCall.ID above
    Name       string `json:"name"`         // same tool name
    Output     string `json:"output"`       // what the tool returned
    Error      string `json:"error,omitempty"` // only set if the tool failed
}
```

**How they link together:**

```
assistant message:  ToolCalls[0].ID = "call_abc123"    ← LLM requests tool call
                            ↓ matches ↓
tool message:       ToolCallID = "call_abc123"          ← Framework responds with tool output
```

### 1.3 A Complete Conversation Example

Here's what a real conversation looks like as JSON — system prompt, user question, LLM calls a tool, tool responds, LLM gives final answer:

```json
[
  {
    "role": "system",
    "content": "You are a coding assistant."
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
  },
  {
    "role": "assistant",
    "content": "The file contains a FastAPI app with a single GET endpoint at / that returns {\"status\": \"ok\"}."
  }
]
```

Notice:
- Message 3 (`assistant`) has empty `content` but has `tool_calls` — the LLM chose to act instead of speak
- Message 4 (`tool`) has `tool_call_id` matching the `id` from message 3 — that's how the LLM knows which call this result belongs to
- Message 5 (`assistant`) has `content` but no `tool_calls` — the LLM is done and speaks to the user

---

## 2. Role Constants (`agent/messages.go`)

The four role strings are defined as Go constants so you don't have to type raw strings:

```go
const (
    RoleSystem    = "system"
    RoleUser      = "user"
    RoleAssistant = "assistant"
    RoleTool      = "tool"
)
```

**Why constants?** If you mistype `"asistant"` the compiler won't catch it — it's just a string. But if you write `RoleAsistant` the compiler will error because that constant doesn't exist. Always use the constants.

---

## 3. Message Constructors

Instead of writing out the full `Message{Role: ..., Content: ...}` struct every time, use these shortcut functions:

```go
// agent/messages.go

// Human creates a user message
func Human(content string) Message {
    return Message{Role: RoleUser, Content: content}
}

// System creates a system message
func System(content string) Message {
    return Message{Role: RoleSystem, Content: content}
}

// AI creates an assistant message with optional tool calls
// The "..." means you can pass zero or more ToolCall values
func AI(content string, toolCalls ...ToolCall) Message {
    return Message{Role: RoleAssistant, Content: content, ToolCalls: toolCalls}
}

// ToolMsg creates a tool result message
func ToolMsg(toolCallID, name, output string) Message {
    return Message{Role: RoleTool, Content: output, ToolCallID: toolCallID, Name: name}
}
```

**Usage:**

```go
// Without constructors (verbose):
msg := agent.Message{Role: "user", Content: "hello"}

// With constructors (cleaner):
msg := agent.Human("hello")

// Assistant that only calls a tool (no text):
msg := agent.AI("", agent.ToolCall{ID: "call_1", Name: "ls", Args: map[string]any{"path": "/workspace"}})

// Tool result:
msg := agent.ToolMsg("call_1", "ls", `[{"name":"main.py","type":"file","size":256}]`)
```

---

## 4. Validation

The framework doesn't just define the four roles — it **enforces** them. Two validation functions catch problems before they reach the LLM.

### 4.1 ValidRole — Is This a Known Role?

```go
func ValidRole(r string) bool {
    switch r {
    case RoleSystem, RoleUser, RoleAssistant, RoleTool:
        return true
    }
    return false
}
```

`ValidRole("hacker")` → `false`. Used by `Validate()` below.

### 4.2 UserInputRole — Can External Users Send This Role?

```go
func UserInputRole(r string) bool {
    return r == RoleUser || r == RoleSystem
}
```

External users (via the HTTP API) can only send `"user"` and `"system"` messages. They cannot inject `"assistant"` or `"tool"` messages — that would let them spoof LLM responses or fake tool outputs.

### 4.3 Validate — Check an Entire Message Chain

Called internally to ensure a conversation is well-formed before sending to the LLM:

```go
func (m Messages) Validate() error
```

What it checks for each message:

| Role | Rules |
|------|-------|
| Any | Role must be one of the four constants (§2). Unknown → error |
| `"tool"` | Must have `ToolCallID` and `Name` set. Missing → error |
| `"assistant"` | Must have `Content` or `ToolCalls` (or both). Both empty → error. Every ToolCall must have `ID` and `Name` |
| `"user"`, `"system"` | Must have non-empty `Content` |

**What the errors look like:**

```go
// Unknown role:
agent.NewMessages(agent.Message{Role: "hacker", Content: "inject"}).Validate()
// → error: message[0]: unknown role "hacker"

// Tool message missing required fields:
agent.NewMessages(agent.Message{Role: "tool", Content: "result"}).Validate()
// → error: message[0]: tool message missing tool_call_id

// Empty assistant:
agent.NewMessages(agent.AI("")).Validate()
// → error: message[0]: assistant message has no content and no tool calls
```

### 4.4 ValidateUserInput — Stricter Check for External Messages

Called when a user submits messages via the HTTP API:

```go
func (m Messages) ValidateUserInput() error
```

**Extra rules on top of Validate:**
- Message list must not be empty
- Only `"user"` and `"system"` roles allowed (see §4.2)
- Content must not be empty

**Real usage** — the HTTP handler calls this before passing messages to the agent:

```go
// handlers/handlers.go — validateAndConvertMessages()

func validateAndConvertMessages(msgs []struct{ Role, Content string }) ([]agent.Message, error) {
    chain := make(agent.Messages, len(msgs))
    for i, m := range msgs {
        chain[i] = agent.Message{Role: m.Role, Content: m.Content}
    }
    if err := chain.ValidateUserInput(); err != nil {
        return nil, err    // HTTP 400 — bad request
    }
    return chain.Slice(), nil
}
```

This is why you can't send `{"role": "assistant", "content": "spoofed"}` via the API — `ValidateUserInput` rejects it.

---

## 5. The Messages Chain

`Messages` is a **named type** over `[]Message`. In Go, naming a type lets you attach methods to it:

```go
type Messages []Message    // same underlying data as []Message, but with methods
```

This gives you a fluent builder, filtering, accessors, and everything below — all on a plain slice.

### 5.1 Creating a Chain

```go
// Empty chain:
chain := agent.NewMessages()

// Chain from existing messages:
chain := agent.NewMessages(msg1, msg2, msg3)

// Shortcut — make() also works since Messages is just a slice:
chain := make(agent.Messages, 0)
```

### 5.2 Building Conversations (Fluent API)

Each method appends a message and returns the chain, so you can chain calls:

```go
chain := agent.NewMessages().
    System("You are helpful.").
    Human("What is 2+2?")
// chain now has 2 messages: [system, user]

// After the LLM responds with a tool call:
chain = chain.
    AI("Let me calculate.", agent.ToolCall{ID: "call_1", Name: "calculate", Args: map[string]any{"expr": "2+2"}}).
    Tool("call_1", "calculate", "4").
    AI("2+2 = 4")
// chain now has 5 messages: [system, user, assistant+tool_call, tool, assistant]
```

**How this works in Go:** Each method calls `append()` and returns the new slice:

```go
func (m Messages) Human(content string) Messages {
    return append(m, Human(content))    // Human() is the constructor from §3
}
```

**Add and Concat** — for when you have existing messages:

```go
// Append individual messages:
chain = chain.Add(msg1, msg2)

// Merge two chains:
merged := chain.Concat(otherChain)
```

### 5.3 Accessors

```go
chain.Len()         // number of messages (same as len(chain))
chain.Last()        // last Message (returns zero-value Message{} if empty)
chain.LastContent() // shortcut for chain.Last().Content
chain.Slice()       // returns []Message (drops the Messages type)
```

**When to use `.Slice()`:** Some functions expect `[]Message` not `Messages`. Since `Messages` is a named type, Go won't auto-convert it. Use `.Slice()`:

```go
// This won't compile:
var msgs []agent.Message = chain    // type mismatch

// This works:
var msgs []agent.Message = chain.Slice()
```

### 5.4 Filtering

Get only messages with a specific role:

```go
chain.UserMessages()      // only role == "user"
chain.AssistantMessages() // only role == "assistant"
chain.ToolMessages()      // only role == "tool"
chain.SystemMessages()    // only role == "system"
chain.ByRole("user")      // generic — same as UserMessages()
```

Each returns a new `Messages` chain (the original is unchanged).

**How it works:**

```go
func (m Messages) ByRole(role string) Messages {
    var out Messages
    for _, msg := range m {
        if msg.Role == role {
            out = append(out, msg)
        }
    }
    return out
}
```

### 5.5 Token Estimation

Rough token count used by the SummarizationHook to decide when to compress old messages (see [HOOKS.md §6.4](HOOKS.md)):

```go
func (m Messages) EstimateTokens() int {
    total := 0
    for _, msg := range m {
        total += len(msg.Content) / 4          // ~4 chars per token
        for _, tc := range msg.ToolCalls {
            total += len(tc.RawArgs) / 4       // count tool call args too
        }
    }
    return total
}
```

This is a **heuristic** — not exact tokenization. But it's fast (no external library) and close enough for deciding when to trigger summarization.

### 5.6 Pretty Printing

Human-readable output for debugging:

```go
func (m Messages) PrettyPrint() string    // returns formatted string
func (m Messages) String() string         // same as PrettyPrint() — enables fmt.Print(chain)
```

**Sample output:**

```
[System]
You are helpful.

[Human]
What is 2+2?

[AI]
Let me calculate.
  → tool_call: calculate(id=call_1, args=map[expr:2+2])

[Tool: calculate (call_id=call_1)]
4

[AI]
2+2 = 4
```

The role labels are mapped by a helper:

```go
func roleLabel(role string) string {
    switch role {
    case RoleSystem:    return "System"
    case RoleUser:      return "Human"
    case RoleAssistant: return "AI"
    case RoleTool:      return "Tool"
    default:            return role
    }
}
```

---

## 6. How Messages Flow Through the Agent Loop

Now that you know the data model, here's how messages are used in `agent/loop.go`:

```
1. User sends messages via HTTP API
         ↓
2. ValidateUserInput() rejects bad roles/empty content (§4.4)
         ↓
3. Messages appended to state.Messages:
   state.Messages = append(state.Messages, messages...)
         ↓
4. LLM-tool loop begins (up to 25 iterations):
         ↓
   ┌─────────────────────────────────────────────────────────────┐
   │ a. Copy state.Messages (hooks shouldn't mutate the original) │
   │    msgs := make([]Message, len(state.Messages))              │
   │    copy(msgs, state.Messages)                                │
   │                                                              │
   │ b. ModifyRequest hooks transform msgs                        │
   │    (MemoryHook injects <agent_memory> into system message)   │
   │    (SkillsHook injects skill catalog into system message)    │
   │                                                              │
   │ c. Send msgs to LLM → get response with Content + ToolCalls │
   │                                                              │
   │ d. Append assistant message to state:                        │
   │    state.Messages = append(state.Messages,                   │
   │        AI(response.Content, response.ToolCalls...))          │
   │                                                              │
   │ e. If no ToolCalls → break (done)                            │
   │                                                              │
   │ f. Execute each ToolCall → get ToolResult                    │
   │                                                              │
   │ g. Append tool result messages to state:                     │
   │    state.Messages = append(state.Messages,                   │
   │        ToolMsg(result.ToolCallID, result.Name, result.Output))│
   │                                                              │
   │ h. Next iteration (go back to step a)                        │
   └─────────────────────────────────────────────────────────────┘
         ↓
5. state.Messages now contains the full conversation
   Saved to ThreadStore for the next user message
```

**Key detail in step (a):** The loop copies `state.Messages` before passing to `ModifyRequest` hooks. This is because hooks modify the message list (injecting memory, skills) — but those injections are for the LLM only, not saved permanently. The original `state.Messages` stays clean.

---

## 7. Where Messages Are Stored

Messages live in `AgentState.Messages`, which is saved to the `ThreadStore` (see [HOOKS.md §3](HOOKS.md)):

```go
// agent/types.go
type AgentState struct {
    ThreadID string            `json:"thread_id"`
    Messages []Message         `json:"messages"`     // ← the conversation history
    Todos    []Todo            `json:"todos,omitempty"`
    Files    map[string]string `json:"files,omitempty"`
    // ...
}
```

The `ThreadStore` is an in-memory `map[string]*AgentState`. When a user sends a follow-up message to the same thread, `LoadOrCreate()` returns the existing state with all previous messages intact. The conversation grows with each turn.

Threads are evicted after 1 hour of inactivity to prevent unbounded memory growth.

---

## 8. Test Endpoint

There's a built-in HTTP endpoint that exercises every Messages method — useful for verifying the chain works without needing an LLM:

```
GET /agents/messages/test
```

This endpoint (in `handlers/handlers.go`) does:

```go
// 1. Build a chain
chain := agent.NewMessages().
    System("You are helpful.").
    Human("What is 2+2?").
    AI("Let me calculate.", agent.ToolCall{ID: "call_1", Name: "calculate", Args: map[string]any{"expression": "2+2"}}).
    Tool("call_1", "calculate", "4").
    AI("2+2 = 4")

// 2. Validate
chain.Validate()

// 3. Filter
userMsgs := chain.UserMessages()
aiMsgs := chain.AssistantMessages()
toolMsgs := chain.ToolMessages()
sysMsgs := chain.SystemMessages()

// 4. Test rejection of unknown roles
badChain := agent.NewMessages(agent.Message{Role: "hacker", Content: "inject"})
badChain.Validate()    // → error: unknown role "hacker"

// 5. Test user input validation blocks spoofing
spoofChain := agent.NewMessages().Human("ok")
spoofChain = append(spoofChain, agent.AI("spoofed"))
spoofChain.ValidateUserInput()    // → error: role "assistant" not allowed

// 6. Concat
chain2 := agent.NewMessages().Human("follow-up question")
merged := chain.Concat(chain2)

// 7. Token estimate
tokens := chain.EstimateTokens()
```

**Response includes:** chain length, pretty-printed output, last message content/role, filter counts, validation errors, merged length, and token estimate.

---

## 9. Quick Reference

### Constructors (create single messages)

| Function | Creates | Example |
|----------|---------|---------|
| `Human(content)` | User message | `agent.Human("hello")` |
| `System(content)` | System message | `agent.System("You are helpful.")` |
| `AI(content, toolCalls...)` | Assistant message | `agent.AI("Sure!")` or `agent.AI("", tc)` |
| `ToolMsg(callID, name, output)` | Tool result message | `agent.ToolMsg("call_1", "ls", "[...]")` |

### Chain methods (build and query conversations)

| Method | Returns | Description |
|--------|---------|-------------|
| `NewMessages(msgs...)` | `Messages` | Create a chain (empty or from existing messages) |
| `.System(content)` | `Messages` | Append system message |
| `.Human(content)` | `Messages` | Append user message |
| `.AI(content, tcs...)` | `Messages` | Append assistant message |
| `.Tool(callID, name, out)` | `Messages` | Append tool result message |
| `.Add(msgs...)` | `Messages` | Append arbitrary messages |
| `.Concat(other)` | `Messages` | Merge two chains |
| `.Len()` | `int` | Number of messages |
| `.Last()` | `Message` | Last message (zero-value if empty) |
| `.LastContent()` | `string` | Content of last message |
| `.Slice()` | `[]Message` | Convert to plain slice |

### Filtering

| Method | Returns | What it keeps |
|--------|---------|--------------|
| `.UserMessages()` | `Messages` | Only `"user"` role |
| `.AssistantMessages()` | `Messages` | Only `"assistant"` role |
| `.ToolMessages()` | `Messages` | Only `"tool"` role |
| `.SystemMessages()` | `Messages` | Only `"system"` role |
| `.ByRole(role)` | `Messages` | Only the given role |

### Validation

| Method | What it checks | Who calls it |
|--------|---------------|-------------|
| `ValidRole(r)` | Is `r` one of the four known roles? | `Validate()` |
| `UserInputRole(r)` | Is `r` allowed from external users? (`"user"` or `"system"` only) | `ValidateUserInput()` |
| `.Validate()` | Full chain: valid roles, required fields, non-empty content | Agent loop (internal) |
| `.ValidateUserInput()` | User-submitted messages: valid roles + non-empty | HTTP handler (API boundary) |

### Display and estimation

| Method | Returns | Description |
|--------|---------|-------------|
| `.PrettyPrint()` | `string` | Human-readable `[System]\n...\n[Human]\n...` format |
| `.String()` | `string` | Same as PrettyPrint (enables `fmt.Print(chain)`) |
| `.EstimateTokens()` | `int` | Rough token count (`len/4` heuristic) |

---

## 10. File Structure

```
wick_deep_agent/server/agent/
├── types.go       # Message, ToolCall, ToolResult, AgentState, Todo structs (§1)
└── messages.go    # Role constants (§2), constructors (§3), validation (§4),
                   # Messages chain type with builder/filter/accessor methods (§5),
                   # pretty printing, token estimation
```
