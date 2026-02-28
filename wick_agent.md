# Wick Agent — Architecture Reference

## Overview

Wick Agent is an AI agent framework built as a **Go library** (`wick_server`) with a **React UI** (`wick_go/ui/`). The application layer (`wick_go/main.go`) imports the library directly — single binary, single process, zero IPC overhead. Agent definitions, custom tools, and configuration are all native Go code.

```
┌─────────────────────────────────────────────────────────┐
│  wick_go/main.go  (Application — pure Go)               │
│  Registers agents, tools, models via Go API              │
│                                                          │
│  s := wickserver.New(wickserver.WithPort(8000))          │
│  s.RegisterAgent("default", &agent.AgentConfig{...})    │
│  s.RegisterTool(&agent.FuncTool{ToolName: "add", ...})  │
│  s.Start()  // single process, blocks                   │
│                                                          │
│  ┌────────────────────────────────────────────────────┐  │
│  │  wick_server library (imported as Go package)      │  │
│  │  ┌────────┐ ┌────────┐ ┌─────────┐ ┌──────────┐  │  │
│  │  │ agent/ │ │  llm/  │ │ hooks/  │ │ backend/ │  │  │
│  │  │ loop   │ │ openai │ │ fs,todo │ │ local    │  │  │
│  │  │ tools  │ │ anthro │ │ skills  │ │ docker   │  │  │
│  │  │ hooks  │ │ resolver│ │ memory  │ │ wickfs   │  │  │
│  │  └────────┘ └────────┘ └─────────┘ └──────────┘  │  │
│  └────────────────────────────────────────────────────┘  │
└──────────────────┬──────────────────────────────────────┘
                   │ SSE streaming
                   ▼
┌─────────────────────────────────────────────────────────┐
│  wick_go/ui/  (React + Vite + TypeScript)                │
│  Chat, Canvas (code/slides/docs/data), Terminal          │
└─────────────────────────────────────────────────────────┘
```

---

## Repository Layout

```
wick_agent/
├── wick_deep_agent/                # Go library
│   ├── server/                     # Go library (package wickserver)
│   │   ├── app.go                  # Server struct, New(), RegisterAgent/Tool, Start/Shutdown
│   │   ├── server.go               # HTTP helpers (writeJSON, CORS middleware)
│   │   ├── config.go               # AppConfig (env/flag parsing)
│   │   ├── config_loader.go        # agents.yaml parser + agent init
│   │   ├── auth.go                 # Bearer token auth middleware
│   │   ├── go.mod                  # Module: wick_server (go 1.24)
│   │   ├── agent/                  # Core runtime
│   │   │   ├── types.go            # Message, ToolCall, AgentState, AgentConfig
│   │   │   ├── hook.go             # Hook interface (4 phases)
│   │   │   ├── tool.go             # Tool interface + FuncTool + registry
│   │   │   ├── loop.go             # Agent loop (LLM ↔ tool iteration)
│   │   │   ├── registry.go         # Template registry + per-user cloning
│   │   │   ├── thread.go           # In-memory thread store with TTL eviction
│   │   │   └── http_tool.go        # HTTP callback tool wrapper
│   │   ├── llm/                    # LLM clients
│   │   │   ├── client.go           # Client interface (Call, Stream)
│   │   │   ├── openai.go           # OpenAI / Ollama / vLLM
│   │   │   ├── anthropic.go        # Anthropic Messages API
│   │   │   └── resolver.go         # Model spec → Client factory
│   │   ├── backend/                # Execution environments
│   │   │   ├── backend.go          # Backend + ContainerManager interfaces
│   │   │   ├── local.go            # LocalBackend (host sh -c + LocalFS)
│   │   │   ├── docker.go           # DockerBackend (daemon/docker exec + RemoteFS)
│   │   │   └── daemon.go           # DaemonClient + DaemonExecutor (TCP/Unix)
│   │   ├── wickfs/                 # Filesystem abstraction
│   │   │   ├── wickfs.go           # FileSystem interface
│   │   │   ├── local.go            # LocalFS (direct os.* stdlib)
│   │   │   └── remote.go           # RemoteFS (wickfs CLI via Executor)
│   │   ├── hooks/                  # Agent middleware
│   │   │   ├── filesystem.go       # 7 file tools
│   │   │   ├── memory.go           # AGENTS.md injection
│   │   │   ├── skills.go           # SKILL.md loading
│   │   │   ├── todolist.go         # Task tracking
│   │   │   └── summarization.go    # Context window management
│   │   ├── handlers/               # HTTP endpoints
│   │   │   ├── handlers.go         # All /agents/* routes
│   │   │   ├── builtin_tools.go    # NewBuiltinTools (search, calc, datetime)
│   │   │   └── tool_store.go       # ToolStore (HTTP + native tools)
│   │   ├── sse/writer.go           # SSE event writer
│   │   ├── tracing/                # Request tracing
│   │   └── cmd/
│   │       ├── wick_server/main.go # Standalone binary entry point
│   │       ├── wickfs/             # Filesystem CLI (for Docker injection)
│   │       └── wickdaemon/main.go  # In-container daemon (TCP :9090 + Unix socket)
│   └── LICENSE
│
├── wick_go/                        # Application layer (pure Go)
│   ├── main.go                     # Agent definitions, tools, startup
│   ├── go.mod                      # Module with replace → ../wick_deep_agent/server
│   ├── Dockerfile                  # Multi-stage: Go + React + debian-slim
│   ├── Makefile
│   ├── skills/                     # Skill instruction libraries
│   │   ├── slides/SKILL.md
│   │   ├── csv-analyzer/           # Includes analyze.py helper script
│   │   ├── code-review/SKILL.md
│   │   ├── data-analysis/SKILL.md
│   │   ├── research/SKILL.md
│   │   └── summarization/SKILL.md
│   └── ui/                         # React frontend
│       ├── src/
│       │   ├── App.tsx
│       │   ├── hooks/useAgentStream.ts
│       │   └── components/
│       └── package.json
│
├── DEV_WORKFLOW.md
├── GO_CODE_GUIDE.md                # Go beginner's guide to the codebase
└── wick_agent.md                   # This file
```

---

## Go Library API (`package wickserver`)

```go
import (
    wickserver "wick_server"
    "wick_server/agent"
)

// Create server with options
s := wickserver.New(
    wickserver.WithPort(8000),
    wickserver.WithHost("0.0.0.0"),
    wickserver.WithGateway("http://gateway:4000"),  // optional auth
    wickserver.WithConfigFile("agents.yaml"),        // optional file-based config
    wickserver.WithStaticPath("./static"),            // optional UI assets
)

// Register agents
s.RegisterAgent("default", &agent.AgentConfig{
    Name:         "My Agent",
    Model:        "ollama:llama3.1:8b",
    SystemPrompt: "You are helpful.",
    Tools:        []string{"calculate"},
    Backend:      &agent.BackendCfg{Type: "local", Workdir: "./workspace"},
})

// Register native tools (in-process, zero overhead)
s.RegisterTool(&agent.FuncTool{
    ToolName: "add",
    ToolDesc: "Add two numbers",
    ToolParams: map[string]any{...},
    Fn: func(ctx context.Context, args map[string]any) (string, error) {
        return "result", nil
    },
})

// Start (blocks until shutdown signal)
s.Start()
```

### Standalone Binary Mode

The library can also run as a standalone binary for `agents.yaml`-based deployments:

```bash
cd wick_deep_agent/server
go build -o wick_server ./cmd/wick_server/
./wick_server --config agents.yaml --port 8000
```

---

## Agent Loop (`agent/loop.go`)

```
For each turn (max 25):
  1. Run ModifyRequest hooks (inject system sections, memory, skills)
  2. Build model call chain (onion-ring hook wrapping)
  3. Call LLM with streaming → SSE events to client
  4. If response has no tool_calls → break (done)
  5. Execute tool calls in parallel (sync.WaitGroup)
     - Each call wrapped by WrapToolCall hooks
  6. Append tool result messages to conversation
  7. Next iteration
```

---

## Hook System (`agent/hook.go`)

Four middleware phases, executed in onion-ring order:

| Phase | When | Purpose |
|-------|------|---------|
| `BeforeAgent` | Once per agent build | Register tools, load skills/memory |
| `ModifyRequest` | Before each LLM call | Inject system sections, trim context |
| `WrapModelCall` | Around each LLM call | Summarization, prompt caching |
| `WrapToolCall` | Around each tool call | Tracing, result eviction |

| Hook | Phase(s) | What it does |
|------|----------|--------------|
| `FilesystemHook` | before_agent, wrap_tool_call | Registers 7 file tools, evicts large results |
| `MemoryHook` | modify_request | Injects AGENTS.md contents into system prompt |
| `SkillsHook` | before_agent, modify_request | Loads SKILL.md files, adds skill catalog |
| `TodolistHook` | before_agent, modify_request | Task tracking tools + state |
| `SummarizationHook` | wrap_model_call | Token budget, summarizes old messages |
| `TracingHook` | all 4 phases | Request tracing spans |

---

## Backend Abstraction (`backend/`)

```go
type Backend interface {
    Execute(command string) ExecuteResponse
    FS() wickfs.FileSystem
    Workdir() string
    ContainerStatus() string
}
```

| | LocalBackend | DockerBackend |
|-|-------------|---------------|
| **Execute** | `sh -c` on host | wick-daemon (TCP) → fallback to `docker exec` |
| **FS()** | `wickfs.LocalFS` (direct stdlib) | `wickfs.RemoteFS` via daemon → fallback to docker exec |
| **Use case** | Development, trusted | Sandboxed, untrusted |

### wick-daemon (In-Container Daemon)

DockerBackend uses an in-container daemon (`cmd/wickdaemon/`) for fast command execution instead of spawning a `docker exec` per command (~2ms vs ~60ms):

```
┌─────────────────────┐         TCP :9090          ┌───────────────────────┐
│  wick_go (host)     │ ◄─────────────────────────► │  wick-daemon          │
│  DaemonClient       │    NDJSON protocol          │  (inside container)   │
│  DaemonExecutor     │                             │  sh -c per command    │
└─────────────────────┘                             └───────────────────────┘
```

- **Protocol**: NDJSON over TCP — `{"id":"r1","cmd":"ls","workdir":"/workspace","timeout":120}`
- **Dual listen**: TCP `:9090` + Unix socket `/tmp/wick-daemon.sock`
- **Injection**: Binary is `docker cp`'d into container and started via `docker exec -d`
- **Fallback**: If daemon is unavailable, DockerBackend falls back to `docker exec` transparently
- **DaemonClient** (`backend/daemon.go`): Persistent TCP connection with mutex serialization
- **DaemonExecutor**: Implements `wickfs.Executor` interface — drop-in replacement for DockerExecutor

Container launch sequence:
1. `EnsureContainer()` — creates and starts container
2. `EnsureDaemon()` — gets container IP → probes for daemon → if not found: injects binary and starts it
3. `setDaemonClient()` — stores client, creates daemon-backed `wickfs.RemoteFS`

### Container Control

Containers can be stopped and restarted via the API or UI:

| Endpoint | Method | Body | Description |
|----------|--------|------|-------------|
| `/agents/{id}/container` | POST | `{"action":"stop"}` | Stop and remove container |
| `/agents/{id}/container` | POST | `{"action":"restart"}` | Stop then re-launch container |

The UI shows stop/restart buttons alongside the terminal button when a Docker backend is active.

### Docker Mode Mapping (UI)

Both "Local Docker" and "Remote Docker" create Docker containers — the difference is which daemon:

| UI Button | API Payload | Docker Daemon |
|-----------|-------------|---------------|
| Local Docker | `{ mode: "remote", sandbox_url: null }` | Local Docker daemon (`/var/run/docker.sock`) |
| Remote Docker | `{ mode: "remote", sandbox_url: "tcp://..." }` | Remote Docker daemon (TCP URL) |

---

## LLM Clients (`llm/`)

| Spec | Client | Notes |
|------|--------|-------|
| `"ollama:llama3.1:8b"` | OpenAIClient | `base_url=http://localhost:11434/v1` |
| `{"provider":"openai","model":"gpt-4o"}` | OpenAIClient | Standard OpenAI API |
| `{"provider":"anthropic","model":"claude-sonnet-4-20250514"}` | AnthropicClient | Anthropic Messages API |

---

## HTTP Endpoints (`handlers/`)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check + agent count |
| `/agents/` | GET/POST | List or create agents |
| `/agents/{id}` | GET/DELETE | Get or delete agent |
| `/agents/{id}/invoke` | POST | Synchronous agent call |
| `/agents/{id}/stream` | POST | SSE streaming agent call |
| `/agents/{id}/backend` | PATCH | Switch local/remote backend |
| `/agents/{id}/container` | POST | Stop or restart Docker container |
| `/agents/{id}/hooks` | PATCH | Toggle hooks |
| `/agents/{id}/terminal` | WS | WebSocket interactive shell |
| `/agents/{id}/files/*` | GET/PUT | File operations |
| `/agents/tools/*` | GET/POST/DELETE | External tool registration |
| `/agents/skills/available` | GET | List available skills |
| `/agents/events` | GET (SSE) | Real-time config change events |

---

## Thread Store with TTL Eviction

`agent/thread.go` stores conversation state in-memory with automatic eviction:

- Threads not accessed for **1 hour** are automatically removed
- Eviction runs every 5 minutes in a background goroutine
- `LoadOrCreate`, `Save`, `Get` all refresh the access timestamp
- Prevents unbounded memory growth at scale

---

## Data Flow

```
User types message in ChatPanel
    ↓
POST /agents/{id}/stream (SSE)
    ↓
Go handler → resolves agent instance (registry + per-user clone)
    ↓
Agent loop starts:
    ├── ModifyRequest hooks inject system sections
    ├── WrapModelCall hooks manage context window
    ├── LLM.Stream() → SSE events back to React
    ├── For each tool_call:
    │   ├── Built-in (calculate, search) → direct Go code
    │   ├── Filesystem (read/write/...) → wickfs.FileSystem
    │   ├── Native tools (FuncTool) → in-process Go function call
    │   └── External (HTTP callback) → HTTP POST
    └── Loop continues until no more tool_calls or max iterations
    ↓
done event → React → artifact detection → Canvas panel
```

---

## Deployment

### Development

```bash
cd wick_go && go run .                          # server on :8000
cd wick_go/ui && npm run dev                    # UI on :3000 (proxies to :8000)
```

### Production (Docker)

```bash
docker build -f wick_go/Dockerfile -t wick-agent .
docker run -p 8000:8000 wick-agent
```

Multi-stage Dockerfile: Go build (wick_go + wickfs + wick-daemon binaries) → React build → `debian:bookworm-slim` runtime with `python3-minimal` (for skill scripts) and `docker-ce-cli` (for sandbox). File descriptor limit raised to 65536. The `wickfs` and `wick-daemon` binaries are placed in `/usr/local/bin/` for injection into sandbox containers.

### ECS Scaling

| ECS Task Size | Concurrent Users |
|---------------|-----------------|
| 1 vCPU / 2 GB | ~150 (FD-limited) |
| 2 vCPU / 4 GB | ~500 |
| 4 vCPU / 8 GB | ~1000-2000 |

For 10k+ users: multiple instances behind ALB with sticky sessions. Thread state eviction (1h TTL) prevents OOM.

The wick-daemon reduces per-command overhead from ~60ms (docker exec) to ~2ms (TCP), making high-frequency tool calls (file ops, shell commands) significantly faster. Remote Docker support separates the control plane (wick_go host) from the data plane (remote Docker daemon), enabling independent scaling.

---

## Key Design Decisions

1. **Go library, not subprocess** — `wick_server` is `package wickserver`, imported directly by `wick_go/main.go`. One binary, one process. No IPC, no HTTP callbacks for tools.

2. **Native tools** — Custom tools are `agent.FuncTool` instances — Go functions called in-process. Zero serialization overhead.

3. **wickfs abstraction** — `LocalFS` uses direct stdlib calls (zero process spawning). `RemoteFS` delegates to the `wickfs` CLI via wick-daemon (fast path) or Docker exec (fallback). Same interface for both.

4. **Onion-ring hooks** — Hooks wrap each other in reverse order. Filesystem tools, memory, skills, summarization, and tracing are all independent hooks.

5. **Per-user agent scoping** — Registry templates are cloned per `agentID:username`. Backends are also scoped per user.

6. **Streaming-first** — All LLM calls stream. SSE events flow from agent loop to React UI.

7. **TTL-based eviction** — Thread store evicts idle conversations after 1 hour, preventing unbounded memory growth.

8. **Standalone binary** — `cmd/wick_server/` provides a standalone binary for `agents.yaml`-based deployments without writing Go code.

9. **In-container daemon** — `wick-daemon` runs inside Docker containers, accepting commands via NDJSON over TCP. Eliminates `docker exec` spawning overhead (~60ms → ~2ms). Falls back transparently if daemon is unavailable.

10. **Container lifecycle management** — Containers can be stopped/restarted via REST API. Daemon connections are cleaned up on stop. Mode switches clean up orphaned containers.
