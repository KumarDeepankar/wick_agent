# wick_server

Go library (`package wickserver`) for building AI agent applications. Import it directly into your Go application — single binary, single process, zero IPC.

## Usage

```go
import (
    wickserver "wick_server"
    "wick_server/agent"
)

s := wickserver.New(wickserver.WithPort(8000))

s.RegisterAgent("default", &agent.AgentConfig{
    Name:  "My Agent",
    Model: "ollama:llama3.1:8b",
    SystemPrompt: "You are a helpful assistant.",
    Backend: &agent.BackendCfg{Type: "local", Workdir: "./workspace"},
})

s.RegisterTool(&agent.FuncTool{
    ToolName: "add",
    ToolDesc: "Add two numbers",
    ToolParams: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "a": map[string]any{"type": "number"},
            "b": map[string]any{"type": "number"},
        },
        "required": []string{"a", "b"},
    },
    Fn: func(ctx context.Context, args map[string]any) (string, error) {
        a, _ := args["a"].(float64)
        b, _ := args["b"].(float64)
        return fmt.Sprintf("%g", a+b), nil
    },
})

s.Start() // blocks until shutdown signal
```

See [`wick_go/`](../wick_go/) for a full application example.

## Standalone Binary

For `agents.yaml`-based deployments without writing Go code:

```bash
cd server
go build -o wick_server ./cmd/wick_server/
./wick_server --config agents.yaml --port 8000
```

## Packages

```
server/                              # package wickserver
├── app.go                           # Server struct, New(), RegisterAgent/Tool, Start/Shutdown
├── server.go                        # HTTP helpers (writeJSON, CORS middleware)
├── config.go                        # AppConfig (env/flag parsing)
├── config_loader.go                 # agents.yaml parser + agent init
├── auth.go                          # Bearer token auth middleware
│
├── agent/                           # Core runtime
│   ├── messages.go                  # Message, ToolCall, ToolResult, Messages chain, validation, builders
│   ├── state.go                     # AgentState, Todo
│   ├── config.go                    # AgentConfig, BackendCfg, SkillsCfg, MemoryCfg, AgentInfo
│   ├── events.go                    # StreamEvent
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
│   └── tool_store.go                # ToolStore (native + HTTP tools)
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
- Supports both local and remote Docker daemons (`tcp://host:2376`)

### Container Control

```bash
curl -X POST /agents/{id}/container -H 'Content-Type: application/json' -d '{"action":"stop"}'
curl -X POST /agents/{id}/container -H 'Content-Type: application/json' -d '{"action":"restart"}'
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
| `/agents/{id}/files/list` | GET | List directory contents (`?path=`) |
| `/agents/{id}/files/read` | GET | Read file contents (`?path=`) |
| `/agents/files/download` | GET | Download file (`?path=&agent_id=`) |
| `/agents/files/upload` | PUT | Upload file (JSON body) |
| `/agents/tools/available` | GET | List registered tools |
| `/agents/tools/register` | POST | Register external HTTP tool |
| `/agents/tools/deregister/{name}` | DELETE | Remove external tool |
| `/agents/skills/available` | GET | List available skills |
| `/agents/hooks/available` | GET | List available hooks with phases |

## Build Commands

```bash
# Library + all sub-packages
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

## License

Apache License 2.0 — see [LICENSE](LICENSE).
