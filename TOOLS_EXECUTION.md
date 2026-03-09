# Tools Execution

How tools are defined, how the LLM knows about them, how tool call parameters are extracted
from the LLM response, and how tools are actually executed.

---

## 1. What is a Tool?

A tool is anything that implements this interface (`agent/tool.go:6-11`):

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]any           // JSON Schema
    Execute(ctx context.Context, args map[string]any) (string, error)
}
```

That's it. A name, a description, a JSON Schema for parameters, and an Execute function.

---

## 2. Three Types of Tools

### 2a. FuncTool — In-process Go function (`agent/tool.go:14-26`)

The simplest kind. A Go function wrapped as a Tool:

```go
type FuncTool struct {
    ToolName   string
    ToolDesc   string
    ToolParams map[string]any                                           // JSON Schema
    Fn         func(ctx context.Context, args map[string]any) (string, error)
}
```

Example from `wick_go/tools.go:55-72`:
```go
s.RegisterTool(&agent.FuncTool{
    ToolName: "calculate",
    ToolDesc: "Evaluate a mathematical expression. Supports +, -, *, /, ^, %, sqrt.",
    ToolParams: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "expression": map[string]any{
                "type":        "string",
                "description": "Mathematical expression to evaluate",
            },
        },
        "required": []string{"expression"},
    },
    Fn: func(ctx context.Context, args map[string]any) (string, error) {
        expr, _ := args["expression"].(string)
        return calculate(expr), nil
    },
})
```

### 2b. HTTPTool — Remote HTTP callback (`agent/http_tool.go:15-84`)

For tools hosted by external processes (e.g., a Python service):

```go
type HTTPTool struct {
    ToolName    string
    ToolDesc    string
    ToolParams  map[string]any
    CallbackURL string         // e.g. "http://127.0.0.1:9100"
}
```

When executed, it POSTs to `{CallbackURL}/tools/{toolName}` with `{"name": "...", "args": {...}}`
and expects `{"result": "...", "error": "..."}` back.

### 2c. Hook-registered tools — Registered at runtime by hooks

The FilesystemHook registers 7 tools in its `BeforeAgent` phase (`hooks/filesystem.go:46-100`):

```go
func (h *FilesystemHook) BeforeAgent(ctx context.Context, state *agent.AgentState) error {
    agent.RegisterToolOnState(state, &agent.FuncTool{
        ToolName: "ls",
        ...
    })
    agent.RegisterToolOnState(state, &agent.FuncTool{
        ToolName: "read_file",
        ...
    })
    // + write_file, edit_file, glob, grep, execute
}
```

These are also FuncTools, but they're registered on the `AgentState` (per-session) rather than
on the server (global). They use `RegisterToolOnState()` (`agent/tool.go:68-73`):

```go
func RegisterToolOnState(state *AgentState, tool Tool) {
    if state.toolRegistry == nil {
        state.toolRegistry = make(map[string]Tool)
    }
    state.toolRegistry[tool.Name()] = tool
}
```

---

## 3. How Tools Get Wired Into the Agent

There are two registration paths that merge at runtime:

### Path A: App-level tools (startup)

```
wick_go/main.go                       wick_deep_agent/server/app.go
───────────────                       ──────────────────────────────
s.RegisterTool(&FuncTool{             func (s *Server) RegisterTool(t agent.Tool) {
    ToolName: "calculate",                s.tools = append(s.tools, t)    // line 83
    ...                               }
})
                                      // In Start() — line 109:
                                      for _, t := range s.tools {
                                          s.deps.ExternalTools.AddTool(t)
                                      }
```

### Path B: Hook-registered tools (per-request)

```
loop.go:runLoop()
  → hook.BeforeAgent(ctx, state)                    // line 96
    → FilesystemHook registers tools on state       // state.toolRegistry
```

### Merge point (loop.go:108-117)

```go
// Agent-level tools (Path A — from ExternalTools store)
toolMap := make(map[string]Tool)
for _, t := range a.Tools {
    toolMap[t.Name()] = t
}

// Hook-registered tools (Path B — from FilesystemHook etc.)
if state.toolRegistry != nil {
    for name, t := range state.toolRegistry {
        toolMap[name] = t
    }
}
```

The final `toolMap` contains all tools from both paths. This is what the agent uses for
both telling the LLM what tools exist and executing them.

---

## 4. How the LLM Knows About Tools (JSON Schema)

Tools are converted to JSON Schema and sent to the LLM in the API request.

### Step 1: Build schemas (loop.go:120)

```go
toolSchemas := buildToolSchemas(toolMap)
```

`buildToolSchemas()` (loop.go:568-578):
```go
func buildToolSchemas(toolMap map[string]Tool) []llm.ToolSchema {
    schemas := make([]llm.ToolSchema, 0, len(toolMap))
    for _, t := range toolMap {
        schemas = append(schemas, llm.ToolSchema{
            Name:        t.Name(),
            Description: t.Description(),
            Parameters:  t.Parameters(),     // the JSON Schema from the tool
        })
    }
    return schemas
}
```

### Step 2: Send to LLM (provider-specific formatting)

**OpenAI format** (`llm/openai.go:258-271`):
```json
{
  "model": "llama3.1:8b",
  "messages": [...],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "calculate",
        "description": "Evaluate a mathematical expression...",
        "parameters": {
          "type": "object",
          "properties": {
            "expression": {"type": "string", "description": "Mathematical expression to evaluate"}
          },
          "required": ["expression"]
        }
      }
    }
  ]
}
```

**Anthropic format** (`llm/anthropic.go:255-265`):
```json
{
  "model": "claude-sonnet-4-20250514",
  "messages": [...],
  "tools": [
    {
      "name": "calculate",
      "description": "Evaluate a mathematical expression...",
      "input_schema": {
        "type": "object",
        "properties": {
          "expression": {"type": "string", "description": "Mathematical expression to evaluate"}
        },
        "required": ["expression"]
      }
    }
  ]
}
```

The key difference: OpenAI wraps it in `{"type": "function", "function": {...}}`,
Anthropic uses `"input_schema"` instead of `"parameters"`.

---

## 5. How Tool Call Parameters Are Extracted From the LLM Response

**This is NOT structured output.** It uses the LLM provider's native **tool calling / function calling**
feature. The LLM returns tool calls as a structured part of its response format — not as
free-form text that we parse.

### OpenAI / Ollama Response

The LLM responds with `tool_calls` in the message:

```json
{
  "choices": [{
    "message": {
      "role": "assistant",
      "content": null,
      "tool_calls": [{
        "id": "call_abc123",
        "type": "function",
        "function": {
          "name": "calculate",
          "arguments": "{\"expression\": \"2 + 2\"}"
        }
      }]
    },
    "finish_reason": "tool_calls"
  }]
}
```

Note: `arguments` is a **JSON string**, not an object. It must be parsed.

**Parsing** (`llm/openai.go:104-111`):
```go
for _, tc := range msg.ToolCalls {
    var args map[string]any
    json.Unmarshal([]byte(tc.Function.Arguments), &args)    // parse JSON string -> map
    result.ToolCalls = append(result.ToolCalls, ToolCallResult{
        ID:   tc.ID,                    // "call_abc123"
        Name: tc.Function.Name,         // "calculate"
        Args: args,                     // {"expression": "2 + 2"}
    })
}
```

### Anthropic Response

Anthropic returns tool calls as content blocks:

```json
{
  "content": [
    {"type": "text", "text": "Let me calculate that."},
    {
      "type": "tool_use",
      "id": "toolu_abc123",
      "name": "calculate",
      "input": {"expression": "2 + 2"}
    }
  ],
  "stop_reason": "tool_use"
}
```

Note: Anthropic's `input` is already a **parsed object**, not a JSON string.

**Parsing** (`llm/anthropic.go:98-109`):
```go
for _, block := range resp.Content {
    switch block.Type {
    case "text":
        result.Content += block.Text
    case "tool_use":
        result.ToolCalls = append(result.ToolCalls, ToolCallResult{
            ID:   block.ID,        // "toolu_abc123"
            Name: block.Name,      // "calculate"
            Args: block.Input,     // {"expression": "2 + 2"} — already a map
        })
    }
}
```

### Streaming: How Arguments Are Accumulated

During streaming, tool call arguments arrive in chunks. They must be accumulated before parsing.

**OpenAI streaming** (`llm/openai.go:142-209`):
```go
toolCalls := make(map[int]*ToolCallResult)
toolCallArgs := make(map[int]*strings.Builder)    // accumulate arg chunks

for scanner.Scan() {
    // ... parse SSE line ...
    for _, tc := range delta.ToolCalls {
        if existing, ok := toolCalls[idx]; ok {
            // Accumulate arguments chunk by chunk
            toolCallArgs[idx].WriteString(tc.Function.Arguments)  // e.g. "{\"exp" then "ression" then "\": \"2 + 2\"}"
        } else {
            // First chunk — capture ID and name
            toolCalls[idx] = &ToolCallResult{ID: tc.ID, Name: tc.Function.Name}
            toolCallArgs[idx] = &strings.Builder{}
            toolCallArgs[idx].WriteString(tc.Function.Arguments)
        }
    }

    // When finish_reason="tool_calls", parse accumulated JSON
    if finishReason == "tool_calls" {
        for idx, tc := range toolCalls {
            var args map[string]any
            json.Unmarshal([]byte(toolCallArgs[idx].String()), &args)  // parse complete JSON
            tc.Args = args
            ch <- StreamChunk{ToolCall: tc}
        }
    }
}
```

**Anthropic streaming** (`llm/anthropic.go:136-194`):
```go
var currentToolID, currentToolName string
var argsBuilder strings.Builder

for scanner.Scan() {
    switch event.Type {
    case "content_block_start":
        // New tool_use block — capture ID and name
        currentToolID = event.ContentBlock.ID
        currentToolName = event.ContentBlock.Name
        argsBuilder.Reset()

    case "content_block_delta":
        if delta.Type == "input_json_delta" {
            argsBuilder.WriteString(delta.PartialJSON)   // accumulate JSON fragments
        }

    case "content_block_stop":
        // Block complete — parse the accumulated JSON
        var args map[string]any
        json.Unmarshal([]byte(argsBuilder.String()), &args)
        ch <- StreamChunk{ToolCall: &ToolCallResult{
            ID:   currentToolID,
            Name: currentToolName,
            Args: args,                                  // fully parsed
        }}
    }
}
```

Both providers stream argument JSON in fragments. The client accumulates them in a
`strings.Builder` and only parses the complete JSON when the tool call block is finished.

---

## 6. Unified Internal Format

Regardless of provider, tool calls are normalized to this struct (`llm/client.go:62-66`):

```go
type ToolCallResult struct {
    ID   string         `json:"id"`        // provider-assigned unique ID
    Name string         `json:"name"`      // tool name (e.g. "calculate")
    Args map[string]any `json:"arguments"` // parsed parameters
}
```

The agent loop then converts this to its own `ToolCall` type (`agent/loop.go:456-461`):

```go
tc := ToolCall{
    ID:   chunk.ToolCall.ID,      // "call_abc123"
    Name: chunk.ToolCall.Name,    // "calculate"
    Args: chunk.ToolCall.Args,    // map[string]any{"expression": "2 + 2"}
}
```

---

## 7. Tool Execution With Onion Ring

When the agent has a tool call `{Name: "calculate", Args: {"expression": "2+2"}}`:

### Step 1: Build the chain (loop.go:317)

```go
toolCallFn := a.buildToolCallChain(toolMap)
```

This nests hook wrappers around the actual execution (`loop.go:529-546`):

```go
func (a *Agent) buildToolCallChain(toolMap map[string]Tool) ToolCallFunc {
    base := func(ctx context.Context, tc ToolCall) (*ToolResult, error) {
        r := a.executeTool(ctx, tc, toolMap)    // the real execution
        return &r, nil
    }

    fn := base
    for i := len(a.Hooks) - 1; i >= 0; i-- {   // reverse order
        hook := a.Hooks[i]
        prev := fn
        fn = func(ctx context.Context, tc ToolCall) (*ToolResult, error) {
            return hook.WrapToolCall(ctx, tc, prev)
        }
    }
    return fn
}
```

Result: `TracingHook( TodoListHook( FilesystemHook( SkillsHook( MemoryHook( SummarizationHook( executeTool ) ) ) ) ) )`

### Step 2: Call the chain (loop.go:318)

```go
result, err := toolCallFn(ctx, tc)
```

For `calculate`, most hooks are pass-through (just call `next`). The execution reaches
`executeTool()` (`loop.go:365-391`):

```go
func (a *Agent) executeTool(ctx context.Context, tc ToolCall, toolMap map[string]Tool) ToolResult {
    tool, ok := toolMap[tc.Name]              // lookup "calculate" in the map
    if !ok {
        return ToolResult{Error: "unknown tool: calculate"}
    }

    output, err := tool.Execute(ctx, tc.Args) // call FuncTool.Execute()
    if err != nil {
        return ToolResult{Error: err.Error()}
    }

    return ToolResult{
        ToolCallID: tc.ID,       // "call_abc123"
        Name:       tc.Name,     // "calculate"
        Output:     output,      // "4"
    }
}
```

### Step 3: FuncTool.Execute (agent/tool.go:24-26)

```go
func (f *FuncTool) Execute(ctx context.Context, args map[string]any) (string, error) {
    return f.Fn(ctx, args)    // calls the Go function you defined
}
```

For `calculate`, this calls the closure from `tools.go:65-71`:
```go
func(ctx context.Context, args map[string]any) (string, error) {
    expr, _ := args["expression"].(string)    // "2 + 2"
    return calculate(expr), nil               // "4"
}
```

### Step 4: Result flows back through the onion

```
executeTool returns ToolResult{Output: "4"}
  ← SummarizationHook: pass through
  ← MemoryHook: pass through
  ← SkillsHook: pass through
  ← FilesystemHook: checks len("4") > 80000? No → pass through
  ← TodoListHook: pass through
  ← TracingHook: records duration, pass through
→ final result: ToolResult{ToolCallID: "call_abc123", Name: "calculate", Output: "4"}
```

### Step 5: Result becomes a message (loop.go:346-348)

```go
state.Messages = append(state.Messages, ToolMsg(tc.ID, tc.Name, result.Output))
```

This creates a message like:
```json
{"role": "tool", "tool_call_id": "call_abc123", "name": "calculate", "content": "4"}
```

The LLM sees this on the next iteration and knows the tool returned "4".

---

## 8. Parallel Tool Execution

If the LLM returns multiple tool calls at once (e.g., `calculate("2+2")` AND
`current_datetime()`), they run in parallel (`loop.go:282-343`):

```go
var wg sync.WaitGroup
results := make([]ToolResult, len(response.ToolCalls))

for i, tc := range response.ToolCalls {
    wg.Add(1)
    go func(idx int, tc ToolCall) {
        defer wg.Done()
        toolCallFn := a.buildToolCallChain(toolMap)
        result, _ := toolCallFn(ctx, tc)
        results[idx] = *result
    }(i, tc)
}
wg.Wait()    // block until ALL tools finish
```

Each tool call gets its own goroutine. Results are collected in a fixed-size slice
(index-safe, no mutex needed). The loop continues only after all tools complete.

---

## 9. All Registered Tools

### App-level tools (from `wick_go/tools.go`)

| Tool | Parameters | What it does |
|------|-----------|--------------|
| `add` | `a: number, b: number` | Returns `a + b` |
| `weather` | `city: string` | Mock weather data |
| `calculate` | `expression: string` | Evaluates math (`+`, `-`, `*`, `/`, `^`, `%`, `sqrt`) |
| `current_datetime` | (none) | Returns UTC and local time |
| `internet_search` | `query: string` | Calls Tavily search API (if `TAVILY_API_KEY` set) |

### Hook-registered tools (from `hooks/filesystem.go`)

| Tool | Parameters | What it does |
|------|-----------|--------------|
| `ls` | `path: string` | List directory contents |
| `read_file` | `file_path: string` | Read file contents |
| `write_file` | `file_path: string, content: string` | Write/create a file |
| `edit_file` | `file_path: string, old_string: string, new_string: string` | Find-and-replace in file |
| `glob` | `pattern: string` | Find files by glob pattern |
| `grep` | `pattern: string, path: string` | Search file contents |
| `execute` | `command: string` | Run a shell command |

### Hook-registered tools (from `hooks/todolist.go`)

| Tool | Parameters | What it does |
|------|-----------|--------------|
| `write_todos` | `todos: array` | Set the full todo list |
| `update_todo` | `id: string, status: string` | Update a single todo item |

---

## 10. Key Takeaways

1. **Not structured output** — Tool calls use the LLM provider's native function calling API.
   The LLM returns tool calls as a structured part of its response, not as free-form text.

2. **JSON Schema tells the LLM what's available** — Each tool's `Parameters()` returns a
   JSON Schema that gets sent in the API request. The LLM uses this to generate valid arguments.

3. **Arguments come as JSON** — OpenAI sends arguments as a JSON string that must be parsed.
   Anthropic sends them as a parsed object. Both are normalized to `map[string]any`.

4. **Streaming accumulates fragments** — During streaming, argument JSON arrives in pieces.
   A `strings.Builder` accumulates chunks until the tool call block is complete, then the
   full JSON is parsed once.

5. **Three sources merge into one toolMap** — App-level tools (registered at startup),
   external HTTP tools (registered via API), and hook-registered tools (per-session) all
   merge into a single `map[string]Tool` that the agent loop uses.

6. **Onion ring wraps execution** — Every tool call passes through the hook chain. Hooks
   can observe, modify, or block tool calls. Most are pass-through for most tools.

7. **Tools run in parallel** — Multiple tool calls from a single LLM response execute
   concurrently via goroutines + WaitGroup.

8. **Results become messages** — Tool output is converted to a `"tool"` role message and
   appended to the conversation. The LLM sees it on the next loop iteration.
