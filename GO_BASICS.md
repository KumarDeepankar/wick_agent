# Go Basics — Language Fundamentals

A quick reference for Go's core concepts, aimed at developers coming from Python or JavaScript.

---

## Assignment & Pointers

Go gives you **explicit control** over whether variables share data or get independent copies. This is the single biggest difference from Python (where mutable objects like lists and dicts are always shared references — immutable types like `int` and `str` behave like values, but Python doesn't let you choose).

### The Three Assignment Forms

```go
type Config struct {
    Name  string
    Tags  []string          // slice — hidden pointer inside!
    Model map[string]any    // map — hidden pointer inside!
}

original := Config{Name: "agent", Tags: []string{"a", "b"}, Model: map[string]any{"k": "v"}}

a := &original    // pointer — a points TO original (fully shared)
b := original     // value copy — Name is independent, but Tags/Model still share data!
c := *(&original) // dereference copy — same as b (copies struct, but slices/maps still shared)
```

**Warning:** `b := original` copies the struct's fields, but slice/map fields are just headers containing pointers. The headers get copied, but they still point to the **same underlying data**. So `b.Name = "x"` is safe, but `b.Tags[0] = "x"` changes `original` too. See [Hidden Pointer Exception](#the-hidden-pointer-exception) below.

### Comparison Table

| | `a := &Xyz` | `b := Xyz` | `c := *ptr` |
|---|---|---|---|
| **What is it** | Reference (points to Xyz) | Direct copy | Dereferenced copy |
| **Type of variable** | Pointer (`*T`) | Value (`T`) | Value (`T`) |
| **Data shared?** | Always. They share everything. | Only if it contains "hidden pointers" | Only if the pointed-to value has "hidden pointers" |
| **Modify simple fields** | Changes original | Does NOT change original | Does NOT change original |
| **Modify slice/map contents** | Changes original | **ALSO changes original** (exception!) | **ALSO changes original** (exception!) |
| **Exception case** | None. Always behaves as a reference. | Slices, maps, channels: modifying contents changes original | Slices, maps, channels inside the value: modifying contents changes original |

### The "Hidden Pointer" Exception

In Go, **slices** are internally small descriptors (called "headers" — 3 words: pointer + length + capacity) that hold a pointer to data stored elsewhere. **Maps** and **channels** are even simpler — the variable itself IS a pointer to a runtime structure. When you copy a struct that contains any of these, you copy the header or pointer — but it still refers to the **same underlying data**.

```go
original := Config{
    Name: "agent",
    Tags: []string{"a", "b"},       // Tags header points to → [a, b] in memory
}

copy := original                     // copies Name (independent) + Tags header (shared!)

copy.Name = "changed"                // independent — original.Name is still "agent"
copy.Tags[0] = "CHANGED"            // SHARED — original.Tags[0] is now "CHANGED" too!
```

Why? Because `copy.Tags` and `original.Tags` are separate headers pointing to the **same array**:

```
original.Tags ──header──►  ┌───┬───┐
                            │ a │ b │  ← same memory!
copy.Tags     ──header──►  └───┴───┘
```

### Safe vs Unsafe Modifications

| Field Type | Copy-safe? | Why |
|---|---|---|
| `int`, `float64`, `bool` | Safe | Fully copied, no hidden pointers |
| `string` | Safe | Strings are immutable in Go |
| `[3]int` (fixed array) | Safe | Arrays are value types, fully copied |
| `[]int` (slice) | **Unsafe** | Header copied, underlying array shared |
| `map[string]any` | **Unsafe** | Header copied, underlying hash table shared |
| `chan int` | **Unsafe** | Channel variable is a pointer; copy gives another pointer to same channel |
| `*Foo` (pointer field) | **Unsafe** | The pointer is copied, same target |

### How to Make a True Deep Copy

If you need a fully independent copy, you must manually copy slices and maps:

```go
original := Config{
    Name: "agent",
    Tags: []string{"a", "b"},
    Model: map[string]any{"k": "v"},
}

// Deep copy
deepCopy := original                              // copies simple fields
deepCopy.Tags = make([]string, len(original.Tags)) // new slice
copy(deepCopy.Tags, original.Tags)                 // copy contents
deepCopy.Model = make(map[string]any)              // new map
for k, v := range original.Model {
    deepCopy.Model[k] = v                          // copy entries
}

deepCopy.Tags[0] = "CHANGED"    // original.Tags[0] is still "a"
deepCopy.Model["k"] = "new"     // original.Model["k"] is still "v"
```

### Visual Summary

```
a := &original        a := original           a := *ptr
                       (or a := *ptr)
┌───┐    ┌────────┐   ┌────────┐ ┌────────┐  Same as middle column.
│ a │───►│original│   │ a      │ │original│  *ptr just means "get
└───┘    │ Name   │   │ Name ✓ │ │ Name   │  the value the pointer
 pointer │ Tags ──►│   │ Tags ──►│ │ Tags ──►│  points to, then copy".
         │ Model──►│   │ Model──►│ │ Model──►│
         └────────┘   └────────┘ └────────┘
                       ✓ = independent copy
                       ► = shared (hidden pointer)
```

### Method & Function Parameters

The same rules apply when passing values to functions — but now it matters because the function might **modify your data**:

```go
type Agent struct {
    Name  string
    Tools []string
}

func updateByPointer(a *Agent)  { a.Name = "changed" }   // receives address
func updateByValue(a Agent)     { a.Name = "changed" }    // receives copy

original := Agent{Name: "bot", Tools: []string{"ls", "grep"}}

updateByValue(original)         // original.Name is still "bot" — function got a copy
updateByPointer(&original)      // original.Name is now "changed" — function had the address
```

| | `fn(&Xyz)` or `fn(ptr)` | `fn(Xyz)` or `fn(*ptr)` |
|---|---|---|
| **What's passed** | Address (8 bytes) | Full copy of the data |
| **Method header** | `func do(arg *MyType)` | `func do(arg MyType)` |
| **Modification** | Affects original — method has the address | Safe/local — method has its own copy |
| **Efficiency** | High — always 8 bytes regardless of struct size | Variable — copies entire struct, slow if large |
| **Exception** | None. Always modifies original. | Slices/maps: modifying **elements** still affects original |

**The hidden pointer exception in methods:**

```go
func addTool(a Agent) {         // receives by VALUE (copy)
    a.Name = "new"              // safe — only changes the copy
    a.Tools[0] = "REPLACED"     // DANGER — changes original's slice data!
    a.Tools = append(a.Tools, "new_tool")
    // ^ tricky: if cap > len, writes into shared backing array (caller can't see
    //   the new element but the array slot is silently mutated).
    //   if cap == len, allocates new array (original truly unaffected).
}

original := Agent{Name: "bot", Tools: []string{"ls", "grep"}}
addTool(original)
// original.Name is still "bot"       ← copy was independent
// original.Tools[0] is "REPLACED"    ← slice header shared the underlying array
```

**Key rule:** Use `*T` (pointer receiver/parameter) when you need to modify the caller's data or the struct is large. Use `T` (value) when you want guaranteed isolation and the data is small.

```go
// This codebase uses pointer receivers everywhere:
func (a *Agent) Run(ctx context.Context, ...) (*AgentState, error)
func (h *FilesystemHook) BeforeAgent(ctx context.Context, ...) error
func (f *FuncTool) Execute(ctx context.Context, ...) (string, error)
//     ^^^^^^^^^ pointer receiver — can modify the struct, efficient for large structs
```

---

## Value Types vs Reference Types

Go types fall into two categories:

| Category | Types | Assignment behavior |
|---|---|---|
| **Value types** | `int`, `float64`, `bool`, `string`, `struct`, `[N]T` (arrays) | Full copy. Independent. |
| **Reference types** | `slice`, `map`, `chan`, `*T` (pointers), `func`, `interface` | Copy the header/pointer. Shared underlying data. |

```go
// Value type — independent
x := 42
y := x
y = 99          // x is still 42

// Reference type — shared
a := []int{1, 2, 3}
b := a
b[0] = 99       // a[0] is now 99 too!

// Reference type — but reassignment breaks the link
b = []int{7, 8, 9}   // b now points to new data
b[0] = 0              // a is unaffected — they point to different arrays now
```

---

## Nil — Go's "None"

```go
var p *Config       // pointer — nil (points to nothing)
var s []string      // slice — nil (no underlying array)
var m map[string]int // map — nil (no underlying hash table)
var err error       // interface — nil

// Check for nil
if p == nil { ... }  // like Python: if p is None

// GOTCHA: nil map reads succeed, but writes panic!
var m map[string]int
_ = m["key"]         // returns zero value (0) — no panic
m["key"] = 1          // PANIC: assignment to entry in nil map

// Fix: always initialize maps before writing
m = make(map[string]int)
m["key"] = 1          // works fine
```

| Type | Zero value | Safe to read? | Safe to write? |
|---|---|---|---|
| `*T` | `nil` | No (panic) | No (panic) |
| `[]T` | `nil` | Yes (`len` = 0) | **Partial** — `append` works, but `s[0]=x` panics |
| `map[K]V` | `nil` | Yes (returns zero) | **No (panic!)** |
| `chan T` | `nil` | Blocks forever | Blocks forever |
| `error` | `nil` | Yes (means "no error") | N/A |

---

## Error Handling

Go has no exceptions. Functions return an `error` as the last return value:

```go
// Go                                    // Python
result, err := doSomething()            // try:
if err != nil {                         //     result = do_something()
    return fmt.Errorf("failed: %w", err)// except Exception as e:
}                                       //     raise RuntimeError(f"failed: {e}")
// use result                           // use result
```

**`%w`** in `fmt.Errorf` wraps the original error — callers can unwrap it later with `errors.Is()` or `errors.As()`.

Common patterns:

```go
// 1. Return early on error (most common)
data, err := os.ReadFile("config.json")
if err != nil {
    return nil, err
}

// 2. Inline error check (short-variable-declaration inside if)
if err := doSomething(); err != nil {
    return err
}

// 3. Ignore error (only when you truly don't care)
data, _ := json.Marshal(value)    // _ discards the error
```

---

## Goroutines & Channels

**Goroutine** = lightweight thread. Initial stack is 2KB (grows dynamically as needed), vs ~8MB for OS threads.

**Channel** = typed pipe for sending data between goroutines.

```go
// Launch a goroutine
go func() {
    fmt.Println("runs concurrently")
}()

// Channel basics — two forms:
unbuffered := make(chan string)       // unbuffered — send blocks until someone receives
buffered   := make(chan string, 10)   // buffered — queue of size 10, send blocks only when full

buffered <- "hello"                   // send (blocks if buffer full)
msg := <-buffered                     // receive (blocks if buffer empty)
close(buffered)                       // close channel (signals "no more data")

// Read until channel is closed
for msg := range buffered {
    fmt.Println(msg)                  // loops until channel is closed
}
```

**`select`** — listen on multiple channels at once:

```go
select {
case msg := <-dataCh:
    process(msg)
case <-ctx.Done():          // cancellation signal
    return ctx.Err()
case <-time.After(5 * time.Second):
    return errors.New("timeout")
}
```

**`sync.WaitGroup`** — wait for multiple goroutines to finish:

```go
var wg sync.WaitGroup

for i := 0; i < 5; i++ {
    wg.Add(1)                       // +1 to counter
    go func(n int) {
        defer wg.Done()             // -1 when goroutine finishes
        fmt.Println("worker", n)
    }(i)                            // pass i by value (important!)
}

wg.Wait()                           // blocks until counter is 0
```

---

## Defer

`defer` schedules a function call to run when the **enclosing function returns**. Like Python's `finally`, but more flexible:

```go
func readFile(path string) (string, error) {
    f, err := os.Open(path)
    if err != nil {
        return "", err
    }
    defer f.Close()         // will run when readFile returns, no matter what

    data, err := io.ReadAll(f)
    if err != nil {
        return "", err      // f.Close() still runs
    }
    return string(data), nil // f.Close() still runs
}
```

Multiple defers run in **LIFO order** (last in, first out):

```go
defer fmt.Println("first")
defer fmt.Println("second")
defer fmt.Println("third")
// Output: third, second, first
```

**Gotchas:**

```go
// GOTCHA 1: deferred closures capture variables by REFERENCE, not value
x := 1
defer func() { fmt.Println(x) }()   // prints 99, not 1!
x = 99

// Fix: pass as argument (evaluated at defer time)
defer func(v int) { fmt.Println(v) }(x)   // prints 1

// GOTCHA 2: deferred functions can modify named return values
func addOne() (result int) {
    defer func() { result++ }()
    return 41   // actually returns 42!
}
```

---

## Struct Embedding (Go's "Inheritance")

Go doesn't have classes or inheritance. Instead, you **embed** one struct inside another:

```go
type BaseHook struct{}
func (BaseHook) Name() string { return "base" }
func (BaseHook) BeforeAgent() error { return nil }

type FilesystemHook struct {
    BaseHook            // embedded — "inherits" all BaseHook methods
    fs wickfs.FileSystem
}

// FilesystemHook automatically has Name() and BeforeAgent()
// You can override by defining your own:
func (h *FilesystemHook) Name() string { return "filesystem" }
func (h *FilesystemHook) BeforeAgent() error {
    // custom implementation
}
// h.Name() now calls FilesystemHook.Name() — BaseHook.Name() is "shadowed"
// But the original is still accessible explicitly: h.BaseHook.Name()
// If FilesystemHook didn't define BeforeAgent(), h.BeforeAgent() would
// automatically call BaseHook.BeforeAgent() (this is called "promotion")
```

Python equivalent:
```python
class BaseHook:
    def name(self): return "base"
    def before_agent(self): pass

class FilesystemHook(BaseHook):   # inheritance
    def name(self): return "filesystem"
    def before_agent(self):
        # custom implementation
```

---

## Interfaces — Implicit Satisfaction

The most important Go concept for this codebase. No `implements` keyword:

```go
// Define what a Tool must look like
type Tool interface {
    Name() string
    Execute(ctx context.Context, args map[string]any) (string, error)
}

// FuncTool satisfies Tool automatically — it has the right methods
type FuncTool struct {
    ToolName string
    Fn       func(context.Context, map[string]any) (string, error)
}
func (f *FuncTool) Name() string { return f.ToolName }
func (f *FuncTool) Execute(ctx context.Context, args map[string]any) (string, error) {
    return f.Fn(ctx, args)
}

// HTTPTool also satisfies Tool — completely different struct, same interface
type HTTPTool struct {
    URL string
}
func (h *HTTPTool) Name() string { return h.URL }
func (h *HTTPTool) Execute(ctx context.Context, args map[string]any) (string, error) {
    // HTTP POST to h.URL
}

// Both can be used anywhere a Tool is expected:
var tools []Tool = []Tool{
    &FuncTool{ToolName: "add", Fn: addFunc},
    &HTTPTool{URL: "http://example.com/tool"},
}
```

---

## The `make` vs `new` vs Literal

Three ways to create things:

```go
// Literal — most common for structs
cfg := Config{Name: "agent"}        // value
cfg := &Config{Name: "agent"}       // pointer to value

// make — for slices, maps, channels (reference types that need initialization)
s := make([]string, 0, 10)          // slice: length 0, capacity 10
m := make(map[string]int)           // map: empty, ready to use
ch := make(chan int, 5)              // channel: buffered, size 5

// new — rarely used, returns a pointer to zeroed value
p := new(Config)                     // same as: p := &Config{}
```

**When to use which:**
- Structs → literal (`Config{...}` or `&Config{...}`)
- Slices → `make` or literal (`[]string{"a", "b"}`)
- Maps → `make` (or literal `map[string]int{"a": 1}`)
- Channels → `make`

---

## Multiple Return Values

Go functions can return multiple values. The last return is conventionally an `error`:

```go
// Two returns: result + error
func divide(a, b float64) (float64, error) {
    if b == 0 {
        return 0, errors.New("division by zero")
    }
    return a / b, nil    // nil error = success
}

result, err := divide(10, 3)

// Map lookup: value + exists
val, ok := myMap["key"]    // ok is false if key doesn't exist

// Type assertion: value + ok (safe form)
name, ok := value.(string)  // ok is false if value isn't a string

// WARNING: without ok, wrong type = runtime PANIC
name := value.(string)      // panics if value is not a string!
```

---

## Common Patterns in This Codebase

### Functional Options

```go
type Option func(*Server)

func WithPort(port int) Option {
    return func(s *Server) { s.port = port }
}

s := New(WithPort(8000), WithHost("0.0.0.0"))
```

Python equivalent: `Server(port=8000, host="0.0.0.0")` — but more extensible for libraries.

### Builder Pattern with Method Chaining

```go
// Not used in this codebase, but common in Go:
req := NewRequest().
    WithMethod("POST").
    WithURL("/api").
    WithBody(data)
```

### Comma-OK Idiom

```go
val, ok := myMap["key"]        // map lookup
tool, ok := toolMap[name]      // same pattern
result, ok := iface.(string)   // type assertion
```

Always check `ok` before using the value — prevents panics.
