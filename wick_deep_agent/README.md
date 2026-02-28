# wick-deep-agent

AI agent framework with a **Go library** (`package wickserver`) at its core and a **Python SDK** for scripting, lifecycle management, and CLI access.

## Two Ways to Use

### 1. Go Library (Recommended)

Import `wick_server` directly into a Go application — single binary, single process, zero IPC:

```go
import (
    wickserver "wick_server"
    "wick_server/agent"
)

s := wickserver.New(wickserver.WithPort(8000))
s.RegisterAgent("default", &agent.AgentConfig{
    Name:  "My Agent",
    Model: "ollama:llama3.1:8b",
})
s.RegisterTool(&agent.FuncTool{
    ToolName: "add",
    Fn: func(ctx context.Context, args map[string]any) (string, error) {
        a, _ := args["a"].(float64)
        b, _ := args["b"].(float64)
        return fmt.Sprintf("%g", a+b), nil
    },
})
s.Start() // blocks
```

See [`wick_go/`](../wick_go/) for a full example application.

### 2. Python SDK

Build and manage the standalone Go binary from Python:

```python
from wick_deep_agent import WickClient, WickServer
from wick_deep_agent.messages import HumanMessage

# Build the server binary
WickServer.build()

# Start with inline agent config
server = WickServer(
    port=8000,
    agents={
        "default": {
            "name": "My Agent",
            "model": "ollama:llama3.1:8b",
            "system_prompt": "You are a helpful assistant.",
        },
    },
)
server.start()
server.wait_ready()

# Talk to the agent
client = WickClient("http://localhost:8000")
result = client.invoke(HumanMessage("Hello!"), agent_id="default")
print(result)

server.stop()
```

### CLI

```bash
wick-agent build                         # Compile Go binary
wick-agent start --port 8000             # Start server
wick-agent status                        # Check if running
wick-agent logs -n 100                   # Tail logs
wick-agent stop                          # Stop server
wick-agent systemd --binary ./wick_go    # Generate systemd unit
```

## Installation (Python SDK)

```bash
pip install -e .

# With dev tools
pip install -e ".[dev]"
```

## Go Library Packages

```
server/                              # package wickserver
├── app.go                           # Server struct, New(), RegisterAgent/Tool, Start/Shutdown
├── server.go                        # HTTP helpers (writeJSON, CORS middleware)
├── config.go                        # AppConfig (env/flag parsing)
├── config_loader.go                 # agents.yaml parser + agent init
├── auth.go                          # Bearer token auth middleware
│
├── agent/                           # Core runtime
│   ├── types.go                     # Message, ToolCall, AgentState, AgentConfig
│   ├── hook.go                      # Hook interface (4 phases)
│   ├── tool.go                      # Tool interface + FuncTool + registry
│   ├── loop.go                      # Agent loop (LLM ↔ tool iteration)
│   ├── registry.go                  # Template registry + per-user cloning
│   └── thread.go                    # In-memory thread store with TTL eviction
│
├── llm/                             # LLM clients
│   ├── openai.go                    # OpenAI / Ollama / vLLM
│   ├── anthropic.go                 # Anthropic Messages API
│   └── resolver.go                  # Model spec → Client factory
│
├── backend/                         # Execution environments
│   ├── backend.go                   # Backend + ContainerManager interfaces
│   ├── local.go                     # LocalBackend (host sh -c + LocalFS)
│   ├── docker.go                    # DockerBackend (daemon/docker exec + RemoteFS)
│   └── daemon.go                    # DaemonClient + DaemonExecutor (TCP/Unix)
│
├── wickfs/                          # Filesystem abstraction
│   ├── wickfs.go                    # FileSystem interface
│   ├── local.go                     # LocalFS (direct os.* stdlib)
│   └── remote.go                    # RemoteFS (wickfs CLI via Executor)
│
├── hooks/                           # Agent middleware
│   ├── filesystem.go                # 7 file tools (ls, read, write, edit, glob, grep, exec)
│   ├── memory.go                    # AGENTS.md injection
│   ├── skills.go                    # SKILL.md loading
│   ├── todolist.go                  # Task tracking
│   └── summarization.go            # Context window management
│
├── handlers/                        # HTTP endpoints
│   ├── handlers.go                  # All /agents/* routes
│   ├── builtin_tools.go             # NewBuiltinTools (search, calc, datetime)
│   └── tool_store.go                # ToolStore (HTTP + native tools)
│
├── sse/writer.go                    # SSE event writer
├── tracing/                         # Request tracing
│
└── cmd/
    ├── wick_server/main.go          # Standalone binary entry point
    ├── wickfs/                      # Filesystem CLI (injected into containers)
    └── wickdaemon/main.go           # In-container daemon (TCP + Unix socket)
```

## Backends

| | LocalBackend | DockerBackend |
|-|-------------|---------------|
| **Execute** | `sh -c` on host | wick-daemon (TCP ~2ms) → fallback to `docker exec` (~60ms) |
| **FS()** | `wickfs.LocalFS` (direct stdlib) | `wickfs.RemoteFS` via daemon → fallback to docker exec |
| **Use case** | Development, trusted | Sandboxed, untrusted |

### wick-daemon

Docker containers run an in-container daemon (`cmd/wickdaemon/`) that accepts commands over NDJSON/TCP instead of spawning `docker exec` per command:

```
Host (DaemonClient) ──TCP:9090──► Container (wick-daemon) ──sh -c──► command
```

- Injected automatically via `docker cp` + `docker exec -d` on container launch
- Falls back to `docker exec` transparently if daemon is unavailable
- Supports both local Docker daemon and remote Docker (`tcp://host:2376`)

### Container Control

```bash
# Stop container
curl -X POST /agents/{id}/container -d '{"action":"stop"}'

# Restart container
curl -X POST /agents/{id}/container -d '{"action":"restart"}'
```

## HTTP Endpoints

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

## Python SDK Imports

```python
# Core API
from wick_deep_agent import WickClient, WickServer, tool, model

# Message types
from wick_deep_agent.messages import (
    HumanMessage, SystemMessage, AIMessage, ToolMessage, Messages,
)
```

## Build Commands

```bash
# Go library + all sub-packages
cd server && go build ./...

# Standalone binary
cd server && go build -o wick_server ./cmd/wick_server/

# wickfs CLI (for container injection)
cd server && go build -o wickfs ./cmd/wickfs/

# wick-daemon (for container injection)
cd server && go build -o wick-daemon ./cmd/wickdaemon/

# Tests
cd server && go test ./...
```

## Project Layout

```
wick_deep_agent/
├── server/                  # Go library (package wickserver)
│   ├── app.go               # Server struct, public API
│   ├── agent/               # Core runtime (loop, hooks, tools, threads)
│   ├── llm/                 # LLM clients (OpenAI, Anthropic, Ollama)
│   ├── backend/             # Execution backends (local, docker, daemon)
│   ├── wickfs/              # Filesystem abstraction (local, remote)
│   ├── hooks/               # Agent middleware (fs, memory, skills, todos)
│   ├── handlers/            # HTTP endpoints
│   └── cmd/                 # Binaries (wick_server, wickfs, wickdaemon)
├── wick_deep_agent/         # Python SDK
│   ├── __init__.py          # WickClient, WickServer, tool, model
│   ├── client.py            # Typed HTTP client
│   ├── launcher.py          # Server lifecycle manager
│   ├── cli.py               # wick-agent CLI
│   ├── messages.py          # Message types
│   ├── tool.py              # @tool decorator
│   └── model.py             # @model decorator
├── pyproject.toml
└── README.md
```

## License

Apache License 2.0 — see [LICENSE](LICENSE).
