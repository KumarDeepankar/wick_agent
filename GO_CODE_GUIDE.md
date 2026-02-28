# Go Code Guide — wick_server Internals

A beginner-friendly walkthrough of the wick_server codebase for developers new to Go. For foundational Go language concepts (pointers, slices, maps, goroutines, error handling), see [GO_BASICS.md](GO_BASICS.md).

---

## Go Concepts You'll Encounter

**`package`** — Every `.go` file starts with `package something`. Files in the same directory must use the same package name. `package main` is special — it means "this is an executable program" and must have a `func main()`.

**`import`** — Pulls in other packages. Standard library (`fmt`, `log`, `os`) or your own (`wick_server/agent`).

**`func`** — Functions. `func main()` is the entry point, like Python's `if __name__ == "__main__"`.

**`struct`** — Like a Python class but data-only. Methods are attached separately.

**`interface`** — A contract: "any type that has these methods". Like Python's ABC but implicit — you don't need to declare "I implement this".

**`:=`** — Short variable declaration. `x := 5` means "create variable x, infer it's an int".

**`*` and `&`** — Pointers. `&x` = "address of x", `*ptr` = "value at address". Used to avoid copying large structs and to allow mutations.

**`ctx context.Context`** — A "bag of metadata" passed through every function call. It carries two things: **cancellation signals** (e.g. user closed browser → cancel all in-progress work) and **deadlines/timeouts**. Almost every function in the codebase takes `ctx` as its first parameter — this is a Go convention, not unique to this project.

```go
// How it's used in this codebase:
func (f *FuncTool) Execute(ctx context.Context, args map[string]any) (string, error)
func (h *MemoryHook) BeforeAgent(ctx context.Context, state *AgentState) error
func (a *Agent) Run(ctx context.Context, messages []Message, threadID string) (*AgentState, error)

// Checking if the request was cancelled:
select {
case <-ctx.Done():       // channel closes when cancelled
    return ctx.Err()     // returns "context canceled" or "context deadline exceeded"
default:
    // not cancelled, keep going
}
```

Python equivalent: imagine every function receives a `cancelled: threading.Event` parameter that gets set when the HTTP request disconnects. `ctx` is that, but standardized across the entire Go ecosystem.

### Quick Syntax Cheat Sheet

| Go | Python equivalent |
|---|---|
| `x := 5` | `x = 5` |
| `var x int = 5` | `x: int = 5` |
| `func add(a, b int) int` | `def add(a: int, b: int) -> int:` |
| `if err != nil { return err }` | `if err: raise err` |
| `for i, v := range list` | `for i, v in enumerate(list)` |
| `for k, v := range myMap` | `for k, v in my_dict.items()` |
| `go func() { ... }()` | `threading.Thread(target=...).start()` |
| `make(map[string]int)` | `dict()` or `{}` |
| `make([]string, 0)` | `list()` or `[]` |
| `append(slice, item)` | `list.append(item)` |
| `defer cleanup()` | `finally: cleanup()` |
| `struct{...}` | `@dataclass` or class |
| `interface{...}` | `ABC` with `@abstractmethod` |

### Exported vs Unexported

Go uses **capitalization** instead of `public`/`private`:

```go
type Tool interface { ... }    // Capitalized = exported (public), accessible as agent.Tool
type toolHelper struct { ... } // lowercase = unexported (private), only usable inside the package
```

### `agent` is a Package, Not a Type

```go
import "wick_server/agent"

tools []agent.Tool              // agent = package, Tool = interface inside it
agents map[string]*agent.AgentConfig  // AgentConfig = struct inside agent package
```

Python equivalent:
```python
from agent import Tool, AgentConfig
tools: list[Tool]
agents: dict[str, AgentConfig]
```

---

## 1. Entry Point: `wick_go/main.go`

```go
package main  // This is an executable

import (
    wickserver "wick_server"     // imports the library, aliased as "wickserver"
    "wick_server/agent"          // imports the agent sub-package
)

func main() {
    // Create the server
    s := wickserver.New(wickserver.WithPort(8000))

    // Register an agent (like defining a chatbot personality)
    s.RegisterAgent("default", &agent.AgentConfig{
        Name:  "Ollama Local",
        Model: "ollama:llama3.1:8b",
        Tools: []string{"calculate"},
        Backend: &agent.BackendCfg{Type: "local", Workdir: "./workspace"},
    })

    // Register a custom tool (a Go function the LLM can call)
    s.RegisterTool(&agent.FuncTool{
        ToolName: "add",
        Fn: func(ctx context.Context, args map[string]any) (string, error) {
            a := args["a"].(float64)
            b := args["b"].(float64)
            return fmt.Sprintf("%g", a+b), nil
        },
    })

    s.Start()  // blocks forever, running the HTTP server
}
```

Key Go concepts:
- `&agent.AgentConfig{...}` — creates a struct and passes its address (pointer). The `&` means "give me a pointer to this".
- `[]string{"a", "b"}` — a slice (like Python list) of strings.
- `map[string]any` — a map (like Python dict) with string keys and any-type values.
- `func(...) (string, error)` — returns **two values**. Go functions can return multiple values. The `error` return is how Go handles errors (no exceptions).

---

## 2. Server Struct: `server/app.go`

```go
type Server struct {
    port   int
    tools  []agent.Tool
    agents map[string]*agent.AgentConfig
}
```

This is like a Python class with attributes. The **functional options** pattern is used for configuration:

```go
type Option func(*Server)  // an Option is a function that modifies a Server

func WithPort(port int) Option {
    return func(s *Server) { s.port = port }  // returns a closure
}

func New(opts ...Option) *Server {  // ...Option means "any number of Options"
    s := &Server{port: 8000}       // default value
    for _, o := range opts {
        o(s)                        // apply each option
    }
    return s
}
```

This is equivalent to Python's `Server(port=8000, host="0.0.0.0")` but more extensible.

---

## 3. Interfaces: `agent/tool.go`

The most important Go concept:

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]any
    Execute(ctx context.Context, args map[string]any) (string, error)
}
```

An **interface** says: "anything that has these 4 methods is a Tool". Unlike Python, you **don't** write `implements Tool` — if your struct has the methods, it automatically satisfies the interface.

`FuncTool` satisfies `Tool` because it has all 4 methods:

```go
type FuncTool struct {
    ToolName   string
    ToolDesc   string
    ToolParams map[string]any
    Fn         func(ctx context.Context, args map[string]any) (string, error)
}

func (f *FuncTool) Name() string              { return f.ToolName }      // method 1
func (f *FuncTool) Description() string       { return f.ToolDesc }      // method 2
func (f *FuncTool) Parameters() map[string]any { return f.ToolParams }   // method 3
func (f *FuncTool) Execute(ctx context.Context, args map[string]any) (string, error) {
    return f.Fn(ctx, args)                                               // method 4
}
```

Each field maps to a method:

```
Struct field       ->  Method it powers     ->  Interface requirement
ToolName string    ->  Name() string        ->  Tool.Name()
ToolDesc string    ->  Description() string ->  Tool.Description()
ToolParams map     ->  Parameters() map     ->  Tool.Parameters()
Fn func(...)       ->  Execute(...)         ->  Tool.Execute()
```

The `(f *FuncTool)` before the method name is called a **receiver** — it's like Python's `self`.

---

## 4. Agent Loop: `agent/loop.go`

### Agent Struct

```go
const MaxIterations = 25   // safety limit prevents infinite loops

type Agent struct {
    ID           string          // e.g. "default"
    Config       *AgentConfig    // settings (model, system prompt, etc.)
    LLM          llm.Client      // the LLM client (Ollama, OpenAI, Anthropic)
    Tools        []Tool          // list of tools this agent can use
    Hooks        []Hook          // middleware (filesystem, memory, etc.)
    threadStore  *ThreadStore    // conversation history storage (lowercase = private)
}
```

### AgentState — the conversation session

`AgentState` is defined in `agent/types.go`. It holds everything about one chat thread:

```go
type AgentState struct {
    ThreadID string              // "thread-abc-123"
    Messages []Message           // conversation history
    Todos    []Todo              // task tracking list
    Files    map[string]string   // files the agent wrote: path -> content

    toolRegistry map[string]Tool // tools registered by hooks (lowercase = private)
}
```

A populated `AgentState` looks like:

```
AgentState {
    ThreadID: "thread-abc-123"
    Messages: [
        {role: "user",      content: "Add 2+3"},
        {role: "assistant", content: "I'll use the add tool", tool_calls: [{name:"add"}]},
        {role: "tool",      content: "5", name: "add"},
        {role: "assistant", content: "The sum is 5"},
    ]
    Todos: [{id:"1", title:"Fix bug", status:"done"}]
    Files: {"/workspace/hello.py": "print('hello')"}
    toolRegistry: {ls: ..., read_file: ..., write_file: ...}
}
```

### Pointers: `*AgentState` vs `AgentState`

Throughout the code you'll see `*AgentState` (with a `*`). The `*` means **pointer** — the variable holds a **memory address** pointing to an `AgentState`, not the struct itself.

Think of it like a house vs an address card:

```
AgentState   = the actual house (data in memory)
*AgentState  = an address card pointing to that house
```

Why use a pointer? Two reasons:

**1. It can be `nil` (empty/nothing)**

```go
var state *AgentState    // state = nil (points to nothing)
                         // Python equivalent: state = None

var state AgentState     // state = AgentState{ThreadID:"", Messages:[], ...}
                         // always has a value, can never be nil
```

**2. Sharing — multiple variables see the same data**

```go
// WITHOUT pointer (copies):
var a AgentState = AgentState{ThreadID: "abc"}
var b AgentState = a         // b is a COPY of a
b.ThreadID = "xyz"           // only changes b
// a.ThreadID is still "abc" — they're separate copies

// WITH pointer (shared):
var a *AgentState = &AgentState{ThreadID: "abc"}
var b *AgentState = a        // b points to SAME data as a
b.ThreadID = "xyz"           // changes the shared data
// a.ThreadID is now "xyz" too — both point to same house
```

This is critical in the agent loop because `runLoop` modifies `state` (adds messages, todos, files) and the caller needs to see those changes.

**Why return `*AgentState` not `AgentState`?**

```go
// AgentState (no pointer) — returns a COPY every time
// copies entire Messages slice, Files map, Todos = expensive
func (a *Agent) Run(...) (AgentState, error)

// *AgentState (pointer) — returns an address (8 bytes)
// cheap, and caller sees same data
func (a *Agent) Run(...) (*AgentState, error)
```

**Python comparison:**

```python
# Python — everything is a reference (pointer) by default
state = None                    # like: var state *AgentState
state = AgentState()            # like: state = &AgentState{}
state.messages.append(msg)      # modifies the shared object

# Go — you choose: value (copy) or pointer (reference)
var state AgentState            # value: always has data, copies on assignment
var state *AgentState           # pointer: can be nil, shares on assignment
```

**Quick pointer cheat sheet:**

```go
&  = "get the address of"      ->  p := &AgentState{}     // create + get pointer
*  = "the type is a pointer"   ->  var p *AgentState       // p holds an address
*  = "follow the pointer"      ->  value := *p             // get data at address
.  = works on both             ->  p.ThreadID              // Go auto-dereferences
```

That last one is nice — `p.ThreadID` works whether `p` is a value or a pointer. Go automatically follows the pointer. In C you'd need `p->ThreadID`, but Go handles it for you.

### Run — synchronous mode

```go
func (a *Agent) Run(ctx context.Context, messages []Message, threadID string) (*AgentState, error) {
    ch := make(chan StreamEvent, 64)    // create a buffered channel (queue of size 64)
    var state *AgentState              // declare variable, initially nil (see above)
    var runErr error

    go func() {                        // launch goroutine (lightweight thread)
        defer close(ch)                // when goroutine finishes, close the channel
        state, runErr = a.runLoop(ctx, messages, threadID, ch)
    }()

    for range ch {                     // read from channel until it's closed
    }                                  // (discard all events — we don't need them)

    return state, runErr
}
```

`Run` is for non-streaming use (REST `/invoke` endpoint). It runs the loop in a goroutine but ignores streaming events.

- **`chan StreamEvent`** — a channel. Thread-safe pipe: one goroutine sends, another reads.
- **`go func() { ... }()`** — launches a goroutine (lightweight thread).
- **`defer close(ch)`** — schedules `close(ch)` to run when function exits.
- **`for range ch`** — reads from channel until closed.

### RunStream — streaming mode

```go
func (a *Agent) RunStream(ctx context.Context, messages []Message, threadID string, eventCh chan<- StreamEvent) {
    defer close(eventCh)    // close channel when done (tells browser "stream ended")

    state, err := a.runLoop(ctx, messages, threadID, eventCh)
    if err != nil {
        eventCh <- StreamEvent{Event: "error", Data: map[string]string{"error": err.Error()}}
        return
    }

    eventCh <- StreamEvent{Event: "done", ThreadID: state.ThreadID}
}
```

`chan<- StreamEvent` — the `<-` direction means "send-only channel". This function can only **write** to the channel. The HTTP handler reads from it and sends SSE to the browser.

```
RunStream -> writes events to channel -> HTTP handler reads channel -> sends SSE to browser
```

### runLoop — THE BRAIN

#### Phase A: Setup

```go
func (a *Agent) runLoop(ctx context.Context, messages []Message, threadID string, eventCh chan<- StreamEvent) (*AgentState, error) {
    startTime := time.Now()

    // Load existing conversation or create empty one
    state := a.threadStore.LoadOrCreate(threadID)
    state.Messages = append(state.Messages, messages...)
    //               append = add items to slice. ... unpacks the slice (like Python's *messages)

    tr := TraceFromContext(ctx)    // extract trace recorder (can be nil)
```

#### Phase A1: BeforeAgent hooks (one-time setup)

```go
    for _, hook := range a.Hooks {
        if err := hook.BeforeAgent(ctx, state); err != nil {
            return nil, fmt.Errorf("hook %s BeforeAgent: %w", hook.Name(), err)
        }
    }
```

`if err := ...; err != nil` — Go's error handling pattern. Short-variable-declaration inside the `if`.

Each hook sets things up:
- **FilesystemHook** registers 7 tools (ls, read_file, write_file, edit_file, glob, grep, execute)
- **SkillsHook** scans for SKILL.md files and parses their YAML frontmatter
- **MemoryHook** reads AGENTS.md file content
- **TodoListHook** registers the write_todos tool

(See "Hook System" section below for full details.)

#### Phase A2: Build tool map

```go
    toolMap := make(map[string]Tool)
    for _, t := range a.Tools {              // agent-level tools (add, weather)
        toolMap[t.Name()] = t
    }
    if state.toolRegistry != nil {
        for name, t := range state.toolRegistry {   // hook-registered tools (ls, read_file...)
            toolMap[name] = t
        }
    }
    // toolMap = {add, weather, ls, read_file, write_file, edit_file, glob, grep, execute}

    toolSchemas := buildToolSchemas(toolMap)   // convert to JSON Schema for LLM
```

#### Phase B: The main loop

```go
    for iter := 0; iter < MaxIterations; iter++ {
        select {
        case <-ctx.Done():          // was the request cancelled?
            return state, ctx.Err()
        default:                    // no, continue
        }
```

`select` — Go's way to check channels without blocking. `ctx.Done()` closes when the HTTP request is cancelled.

##### Step 1: ModifyRequest hooks

```go
        msgs := make([]Message, len(state.Messages))
        copy(msgs, state.Messages)              // copy so hooks don't mutate original
        for _, hook := range a.Hooks {
            msgs, err = hook.ModifyRequest(ctx, msgs)   // each hook modifies the copy
        }
```

Each hook modifies the messages before the LLM sees them:
- **MemoryHook** appends AGENTS.md content to system prompt
- **SkillsHook** appends available skills catalog to system prompt

##### Step 2: Call the LLM

```go
        modelCall := a.buildModelChain(toolSchemas)    // build onion-ring function chain
        eventCh <- StreamEvent{Event: "on_chat_model_start"}

        response, err := modelCall(ctx, msgs, eventCh)
        if err != nil {
            return nil, fmt.Errorf("LLM call: %w", err)
        }

        eventCh <- StreamEvent{Event: "on_chat_model_end"}
```

`response` contains the LLM's text + any tool calls it wants to make.

Multiple return values — `response, err :=` captures both. Go's alternative to exceptions:
```python
# Python                          # Go
try:                               response, err := modelCall(ctx, msgs)
    response = model_call(...)     if err != nil {
except Exception as e:                 return nil, err
    raise                          }
```

```go
        state.Messages = append(state.Messages, AI(response.Content, response.ToolCalls...))

        if len(response.ToolCalls) == 0 {
            break       // LLM didn't call any tools -> conversation done
        }
```

**This is the exit condition.** No tool calls = loop ends.

##### Step 3: Execute tools IN PARALLEL

```go
        var wg sync.WaitGroup
        results := make([]ToolResult, len(response.ToolCalls))

        for i, tc := range response.ToolCalls {
            wg.Add(1)                            // increment counter
            go func(idx int, tc ToolCall) {      // launch goroutine for each tool
                defer wg.Done()                  // decrement counter when done

                eventCh <- StreamEvent{Event: "on_tool_start", Name: tc.Name}

                toolCallFn := a.buildToolCallChain(toolMap)
                wrapped, err := toolCallFn(ctx, tc)
                var result ToolResult
                if err != nil {
                    result = ToolResult{Error: err.Error()}
                } else if wrapped != nil {
                    result = *wrapped        // dereference pointer to get the value
                }

                results[idx] = result        // store in pre-allocated slot (thread-safe)

                eventCh <- StreamEvent{Event: "on_tool_end", Name: tc.Name}
            }(i, tc)       // pass i and tc as arguments to the goroutine
        }
        wg.Wait()           // block until ALL goroutines finish
```

Why `go func(idx int, tc ToolCall)` instead of using loop variables directly? Each tool runs in its own goroutine (parallel). The parameters are passed **by value** — this is important because the loop variables `i` and `tc` change each iteration. Without passing them in, all goroutines would share the same variable and get wrong values.

```python
# Python equivalent (same gotcha exists)
for i, tc in enumerate(tool_calls):
    # WRONG: lambda captures loop variable by reference
    thread = Thread(target=lambda: execute(i, tc))

    # RIGHT: capture by value
    thread = Thread(target=lambda idx=i, t=tc: execute(idx, t))
```

`sync.WaitGroup` — a counter. `Add(1)` increments, `Done()` decrements, `Wait()` blocks until counter is 0.

`*wrapped` — dereference the pointer. `wrapped` is `*ToolResult` (pointer). `*wrapped` gives the actual value.

```go
        for _, result := range results {
            state.Messages = append(state.Messages, ToolMsg(result.ToolCallID, result.Name, result.Output))
        }
    }  // end of main loop — goes back to "call LLM" with the tool results
```

#### Phase C: Cleanup

```go
    a.threadStore.Save(threadID, state)    // persist conversation
    return state, nil                      // nil error = success
}
```

### executeTool — looks up and calls a tool

```go
func (a *Agent) executeTool(ctx context.Context, tc ToolCall, toolMap map[string]Tool) ToolResult {
    tool, ok := toolMap[tc.Name]        // look up tool by name
    if !ok {                            // tool not found
        return ToolResult{Error: "unknown tool: " + tc.Name}
    }

    output, err := tool.Execute(ctx, tc.Args)    // call the tool
    if err != nil {
        return ToolResult{Error: err.Error()}
    }

    return ToolResult{Output: output}
}
```

`tool, ok := toolMap[tc.Name]` — Go map lookup returns two values: the value and a boolean. `ok` is `false` if the key doesn't exist.

### buildModelChain — onion ring for LLM calls

```go
func (a *Agent) buildModelChain(toolSchemas []llm.ToolSchema) ModelCallFunc {
    // The innermost function — actually calls the LLM
    base := func(ctx context.Context, msgs []Message, eventCh chan<- StreamEvent) (*ModelResponse, error) {
        req := llm.Request{
            Model:     a.Config.ModelStr(),
            Messages:  llmMsgs,
            Tools:     toolSchemas,
            MaxTokens: 4096,
        }

        chunkCh := make(chan llm.StreamChunk, 64)
        go func() {
            llmErr = a.LLM.Stream(ctx, req, chunkCh)   // LLM writes chunks to channel
        }()

        for chunk := range chunkCh {           // read chunks as they arrive
            if chunk.Delta != "" {
                content += chunk.Delta          // accumulate text
                eventCh <- StreamEvent{...}     // send to browser (shows typing effect)
            }
            if chunk.ToolCall != nil {
                toolCalls = append(toolCalls, ...)  // collect tool calls
            }
        }

        return &ModelResponse{Content: content, ToolCalls: toolCalls}, nil
    }

    // Wrap with hooks (reverse order so index-0 is outermost)
    fn := base
    for i := len(a.Hooks) - 1; i >= 0; i-- {
        hook := a.Hooks[i]
        prev := fn
        fn = func(...) { return hook.WrapModelCall(ctx, msgs, prev) }
    }
    return fn
}
```

If hooks are `[A, B, C]`, the chain becomes `A(B(C(base)))`.

Call order: `A -> B -> C -> actual LLM call -> C -> B -> A`.

### buildToolCallChain — onion ring for tool calls

```go
func (a *Agent) buildToolCallChain(toolMap map[string]Tool) ToolCallFunc {
    base := func(ctx context.Context, tc ToolCall) (*ToolResult, error) {
        r := a.executeTool(ctx, tc, toolMap)    // actually run the tool
        return &r, nil
    }

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

Same onion ring pattern. For example, `FilesystemHook.WrapToolCall` truncates outputs > 80k chars.

### Helper functions

```go
func convertMessages(msgs []Message) []llm.Message { ... }
```
Converts from `agent.Message` to `llm.Message`. They're separate types because `agent` and `llm` are independent packages.

```go
func buildToolSchemas(toolMap map[string]Tool) []llm.ToolSchema { ... }
```
Converts tools into JSON Schema format. The LLM needs to know what tools are available and what parameters they accept.

---

## 5. Hook System: `agent/hook.go`

### The Interface

```go
type Hook interface {
    Name() string
    Phases() []string   // which phases this hook participates in
    BeforeAgent(ctx context.Context, state *AgentState) error
    ModifyRequest(ctx context.Context, msgs []Message) ([]Message, error)
    WrapModelCall(ctx context.Context, msgs []Message, next ModelCallWrapFunc) (*llm.Response, error)
    WrapToolCall(ctx context.Context, call ToolCall, next ToolCallFunc) (*ToolResult, error)
}
```

`Phases()` returns which phases the hook is active in — the agent loop checks this before calling each method. Four phases, each with a different purpose:

| Phase | When | Purpose |
|-------|------|---------|
| `BeforeAgent` | Once, before loop starts | One-time setup: register tools, load files |
| `ModifyRequest` | Before each LLM call | Inject system prompt sections |
| `WrapModelCall` | Around each LLM call | Summarization, caching |
| `WrapToolCall` | Around each tool call | Truncation, tracing |

### BaseHook — "do nothing" defaults

```go
type BaseHook struct{}

func (BaseHook) Name() string { return "base" }   // default name, overridden by each hook

func (BaseHook) Phases() []string {
    return []string{"before_agent", "modify_request", "wrap_model_call", "wrap_tool_call"}
    // default: all phases active. Each hook overrides to list only its active phases.
}

func (BaseHook) BeforeAgent(ctx context.Context, state *AgentState) error {
    return nil                          // does nothing
}
func (BaseHook) ModifyRequest(ctx context.Context, msgs []Message) ([]Message, error) {
    return msgs, nil                    // returns messages unchanged
}
func (BaseHook) WrapModelCall(ctx context.Context, msgs []Message, next ModelCallWrapFunc) (*llm.Response, error) {
    return next(ctx, msgs)              // just calls the next function in chain
}
func (BaseHook) WrapToolCall(ctx context.Context, call ToolCall, next ToolCallFunc) (*ToolResult, error) {
    return next(ctx, call)              // just calls the next function in chain
}
```

Every hook **embeds** `BaseHook` (Go's version of inheritance) and only overrides the phases it cares about:

```go
type FilesystemHook struct {
    agent.BaseHook          // embedded: inherits all BaseHook methods
    fs      wickfs.FileSystem
    workdir string
}
// FilesystemHook overrides BeforeAgent and WrapToolCall
// ModifyRequest and WrapModelCall are inherited from BaseHook (do nothing)
```

Python equivalent:
```python
class BaseHook:
    def before_agent(self, ctx, state): pass
    def modify_request(self, ctx, msgs): return msgs

class FilesystemHook(BaseHook):   # inherits defaults
    def before_agent(self, ctx, state):   # overrides
        ...
```

---

## 6. What Each Hook Does

### FilesystemHook (`hooks/filesystem.go`)

**Active phases:** `before_agent`, `wrap_tool_call`

**BeforeAgent** — Registers 7 file-operation tools:

```go
func (h *FilesystemHook) BeforeAgent(ctx context.Context, state *agent.AgentState) error {
    agent.RegisterToolOnState(state, &agent.FuncTool{
        ToolName: "ls",
        Fn: func(ctx context.Context, args map[string]any) (string, error) {
            path, _ := args["path"].(string)
            resolved, err := h.resolvePath(path)   // security: prevents path escape
            entries, err := h.fs.Ls(ctx, resolved)  // LocalFS.Ls or RemoteFS.Ls
            data, _ := json.Marshal(entries)         // convert to JSON string
            return string(data), nil
        },
    })
    // Same pattern for: read_file, write_file, edit_file, glob, grep, execute
}
```

`h.fs.Ls()` is the **wickfs abstraction**. `h.fs` could be:
- `LocalFS` — directly reads the host filesystem (fast, no subprocess)
- `RemoteFS` — sends command to wick-daemon inside Docker container (sandboxed)

The tool code doesn't know or care which one — same interface.

**WrapToolCall** — Truncates huge tool outputs (but excludes file tools whose output is already controlled):

```go
func (h *FilesystemHook) WrapToolCall(ctx context.Context, call agent.ToolCall, next agent.ToolCallFunc) (*agent.ToolResult, error) {
    result, err := next(ctx, call)     // execute the tool FIRST (call next in chain)

    // File tools are excluded — their output is already bounded
    excluded := map[string]bool{
        "ls": true, "glob": true, "grep": true,
        "read_file": true, "edit_file": true, "write_file": true,
    }

    const maxChars = 80_000    // ~20k tokens
    if len(result.Output) > maxChars && !excluded[call.Name] {
        head := result.Output[:2000]
        tail := result.Output[len(result.Output)-2000:]
        result.Output = head + "\n\n... [truncated] ...\n\n" + tail
    }
    return result, nil
}
```

Notice: `next(ctx, call)` runs the tool first, THEN the hook processes the result. Hooks can act BEFORE (modify input) or AFTER (modify output). The `excluded` map prevents double-truncation on file tools — only tools like `execute` get truncated.

### MemoryHook (`hooks/memory.go`)

**Active phases:** `before_agent`, `modify_request`

**BeforeAgent** — Reads AGENTS.md files from disk:

```go
func (h *MemoryHook) BeforeAgent(ctx context.Context, state *agent.AgentState) error {
    for _, path := range h.paths {
        result := h.backend.Execute(fmt.Sprintf("cat %s 2>/dev/null", path))
        //                          runs "cat AGENTS.md" on host or in container
        if result.ExitCode == 0 {
            parts = append(parts, result.Output)
        }
    }
    h.memoryContent = strings.Join(parts, "\n\n---\n\n")
    //               stores content for later use in ModifyRequest
}
```

**ModifyRequest** — Injects memory into system prompt (every iteration):

```go
func (h *MemoryHook) ModifyRequest(ctx context.Context, msgs []agent.Message) ([]agent.Message, error) {
    injection := fmt.Sprintf(`
<agent_memory>
%s
</agent_memory>
Guidelines for agent memory:
- This memory persists across conversations
- You can update it by using edit_file on the AGENTS.md file
- Use it to track important context, decisions, and patterns
- Keep entries concise and organized`, h.memoryContent)

    // Append to existing system message
    if len(msgs) > 0 && msgs[0].Role == "system" {
        msgs[0].Content += injection
    }
    return msgs, nil
}
```

Before: `[{role:"system", content:"You are helpful"}, {role:"user", content:"hi"}]`

After: `[{role:"system", content:"You are helpful\n<agent_memory>...notes...</agent_memory>"}, {role:"user", content:"hi"}]`

### SkillsHook (`hooks/skills.go`)

**Active phases:** `before_agent`, `modify_request`

**BeforeAgent** — Scans for SKILL.md files:

```go
func (h *SkillsHook) BeforeAgent(ctx context.Context, state *agent.AgentState) error {
    for _, skillsDir := range h.paths {
        result := h.backend.Execute(fmt.Sprintf("find %s -name SKILL.md -type f", skillsDir))
        // Finds: skills/slides/SKILL.md, skills/research/SKILL.md, etc.

        for _, mdPath := range strings.Split(result.Output, "\n") {
            readResult := h.backend.Execute(fmt.Sprintf("cat %s", mdPath))

            entry := SkillEntry{Path: mdPath}

            // Fallback: use parent directory name (e.g. "slides" from "skills/slides/SKILL.md")
            parts := strings.Split(mdPath, "/")
            if len(parts) >= 2 {
                entry.Name = parts[len(parts)-2]
            }

            // Parse YAML frontmatter — overrides directory name if present:
            // ---
            // name: Slide Deck Creator
            // description: Create presentations
            // ---
            match := frontmatterRE.FindStringSubmatch(readResult.Output)
            if match != nil {
                var front map[string]any
                yaml.Unmarshal([]byte(match[1]), &front)
                if name, ok := front["name"].(string); ok {
                    entry.Name = name    // override fallback with explicit name
                }
                if desc, ok := front["description"].(string); ok {
                    entry.Description = strings.TrimSpace(desc)
                }
            }

            h.skills = append(h.skills, entry)
        }
    }
}
```

**ModifyRequest** — Adds skills catalog to system prompt:

```go
func (h *SkillsHook) ModifyRequest(ctx context.Context, msgs []agent.Message) ([]agent.Message, error) {
    // Builds:
    // Available Skills:
    // - [slides] Create presentations -> Read skills/slides/SKILL.md for full instructions
    // - [research] Research topics -> Read skills/research/SKILL.md for full instructions
    msgs[0].Content += catalog
    return msgs, nil
}
```

The LLM sees this in its system prompt and knows it can call `read_file("skills/slides/SKILL.md")` to learn how to create slides.

### SummarizationHook (`hooks/summarization.go`)

**Active phases:** `wrap_model_call`

**WrapModelCall** — Compresses old messages when conversation exceeds context window:

```go
func (h *SummarizationHook) WrapModelCall(ctx context.Context, msgs []agent.Message, next agent.ModelCallWrapFunc) (*llm.Response, error) {
    totalTokens := estimateTokens(msgs)                    // rough: len(text) / 4
    threshold := int(float64(h.contextWindow) * 0.85)      // 85% of context window

    if totalTokens <= threshold {
        return next(ctx, msgs)     // under limit -> pass through, call LLM normally
    }

    // OVER LIMIT! Compress.
    keepCount := len(msgs) / 10                        // keep recent 10%
    oldMsgs := msgs[:len(msgs)-keepCount]              // to summarize
    recentMsgs := msgs[len(msgs)-keepCount:]           // to keep

    // Ask LLM to summarize old messages
    summaryResp, err := h.llmClient.Call(ctx, llm.Request{
        Messages: []llm.Message{{Role: "user", Content: "Summarize concisely...\n" + oldText}},
        MaxTokens: 2000,
    })

    // Replace: [old1, old2, old3, ..., recent1, recent2]
    // With:    [summary, recent1, recent2]
    compressed := append([]agent.Message{summaryMsg}, recentMsgs...)
    return next(ctx, compressed)    // call LLM with compressed messages
}
```

The **`next` parameter** is the key to the onion ring. `next` is the function to call AFTER this hook. It could be the actual LLM call or another hook.

```
SummarizationHook receives next = TracingHook.WrapModelCall
TracingHook receives next = actual LLM call

Call order:
  SummarizationHook.WrapModelCall(msgs, next=TracingHook)
    -> compresses messages if needed
    -> calls next(compressed_msgs)
      -> TracingHook.WrapModelCall(msgs, next=LLM)
        -> starts timer
        -> calls next(msgs)
          -> actual LLM call
        -> stops timer
      -> returns response
    -> returns response
```

### TodoListHook (`hooks/todolist.go`)

**Active phases:** `before_agent`

**BeforeAgent** — Registers the `write_todos` tool:

```go
func (h *TodoListHook) BeforeAgent(ctx context.Context, state *agent.AgentState) error {
    if state.Todos == nil {
        state.Todos = []agent.Todo{}
    }

    agent.RegisterToolOnState(state, &agent.FuncTool{
        ToolName: "write_todos",
        Fn: func(ctx context.Context, args map[string]any) (string, error) {
            // LLM sends: {"todos": [{"id":"1","title":"Fix bug","status":"done"}]}
            data, _ := json.Marshal(args["todos"])
            json.Unmarshal(data, &todos)
            state.Todos = todos              // update the state
            return fmt.Sprintf("Updated %d todo(s)", len(todos)), nil
        },
    })
}
```

The LLM can call `write_todos` to track task progress. The UI reads `state.Todos` to show a task list.

---

## 7. Complete Timeline for One User Message

```
User sends: "Create a presentation about AI"

=== BeforeAgent (runs ONCE) ====================================================
  FilesystemHook.BeforeAgent:
    -> Registers tools: ls, read_file, write_file, edit_file, glob, grep, execute

  SkillsHook.BeforeAgent:
    -> Scans skills/ directory
    -> Finds: slides/SKILL.md, research/SKILL.md, csv-analyzer/SKILL.md
    -> Parses YAML frontmatter from each

  MemoryHook.BeforeAgent:
    -> Reads AGENTS.md file content
    -> Stores it for later injection

  TodoListHook.BeforeAgent:
    -> Registers write_todos tool

=== Loop iteration 1 ===========================================================
  ModifyRequest:
    MemoryHook:  system prompt += "<agent_memory>...notes...</agent_memory>"
    SkillsHook:  system prompt += "Available Skills: [slides], [research]..."

  WrapModelCall chain:
    SummarizationHook: tokens OK, pass through
    -> LLM receives messages + tool schemas

  LLM responds: "I'll read the slides skill instructions"
                 tool_call: read_file("skills/slides/SKILL.md")

  WrapToolCall chain:
    FilesystemHook.WrapToolCall:
      -> next(ctx, call) -> executes read_file -> returns SKILL.md content
      -> checks length (< 80k) -> passes through

=== Loop iteration 2 ===========================================================
  ModifyRequest: (same injections again)

  LLM receives: previous messages + tool result (SKILL.md content)
  LLM responds: "I'll create the presentation file"
                 tool_call: write_file("presentation.md", "# AI Presentation\n...")

  WrapToolCall chain:
    -> executes write_file -> writes to /workspace/presentation.md

=== Loop iteration 3 ===========================================================
  LLM receives: previous messages + "File written: presentation.md (2048 bytes)"
  LLM responds: "I've created your presentation about AI. You can find it at..."
                 (no tool calls)

  -> len(response.ToolCalls) == 0 -> break -> LOOP ENDS

=== After loop =================================================================
  threadStore.Save() -> saves conversation for next time
  "done" event -> browser -> shows completed response
```

---

## 8. Visual Summary of One Iteration

```
                    +----------------------------------+
                    |         runLoop iteration         |
                    +----------------------------------+
                                    |
                    +---------------v-------------------+
                    |  ModifyRequest hooks               |
                    |  (inject system prompt, skills,    |
                    |   compress old messages)            |
                    +---------------+-------------------+
                                    |
                    +---------------v-------------------+
                    |  buildModelChain (onion ring)       |
                    |  Hook3 -> Hook2 -> Hook1 -> LLM    |
                    |                                     |
                    |  LLM streams chunks -> eventCh      |
                    |  (browser sees text appearing)      |
                    +---------------+-------------------+
                                    |
                        +-----------v-----------+
                        | LLM returned tool     | NO -> break (done)
                        | calls?                |--------------------->
                        +-----------+-----------+
                                    | YES
                    +---------------v-------------------+
                    |  For each tool call (in parallel):  |
                    |                                     |
                    |  goroutine 1: read_file("/x.py")   |
                    |  goroutine 2: calculate("2+3")      |
                    |  goroutine 3: internet_search("go") |
                    |                                     |
                    |  wg.Wait() -- wait for all          |
                    +---------------+-------------------+
                                    |
                    +---------------v-------------------+
                    |  Append tool results to messages    |
                    |  Loop back to top                   |
                    +-----------------------------------+
```

---

## 9. Request Flow End-to-End

```
1. Browser: POST /agents/default/stream  {"messages": [{"role":"user","content":"Add 2+3"}]}

2. handlers.go receives it, finds the "default" agent config

3. Creates Agent struct with LLM client + hooks + tools

4. Calls agent.RunStream() -> starts the loop in a goroutine

5. Loop iteration 1:
   - ModifyRequest hooks inject system prompt
   - Calls Ollama LLM with the message
   - LLM responds: "I'll use the add tool" + tool_call{name:"add", args:{a:2, b:3}}
   - SSE events stream to browser (user sees text appearing)

6. Tool execution:
   - Finds "add" FuncTool
   - Calls Fn(ctx, {a:2, b:3}) -> returns "5"

7. Loop iteration 2:
   - Sends tool result back to LLM
   - LLM responds: "The sum of 2 and 3 is 5"
   - No tool calls -> loop ends

8. "done" event -> browser shows final response
```
