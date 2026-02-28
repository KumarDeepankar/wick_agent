# wick_go

A pure Go AI agent application powered by the `wick_server` library (`wick_deep_agent/server/`).

Single binary, single process, zero IPC. Agent definitions, tools, and skills are configured directly in `main.go` using the `wickserver` Go API.

## Quick Start

```bash
# Build and run
cd wick_go
go build -o wick_go . && ./wick_go

# Or just run directly
go run .
```

The server starts on `http://localhost:8000`.

## Architecture

```
main.go (Go)
───────────
  s := wickserver.New(wickserver.WithPort(8000))
  s.RegisterAgent("default", &agent.AgentConfig{...})
  s.RegisterTool(&agent.FuncTool{ToolName: "add", Fn: ...})
  s.Start()  // blocks — single process, single port

       ┌─────────────────────────────────────────────────┐
       │  wick_server library (imported, not a subprocess)│
       │  Agent loop, LLM clients, hooks, backends       │
       │  HTTP server on :8000                            │
       └──────────────────┬──────────────────────────────┘
                          │ SSE streaming
                          ▼
       ┌─────────────────────────────────────────────────┐
       │  ui/ (React + Vite + TypeScript)                 │
       │  Chat, Canvas, Terminal, Settings                │
       └─────────────────────────────────────────────────┘
```

Single process, single port. Tools are native Go functions (`agent.FuncTool`) — zero serialization overhead.

## Execution Modes (Backends)

| Type | Description | Use Case |
|------|-------------|----------|
| `state` | No execution backend. Chat-only. | Default when no backend configured. |
| `local` | Commands run on the host via `sh -c`. | Development, trusted environments. |
| `docker` | Commands run in Docker container via wick-daemon (fast) or docker exec (fallback). | Production, untrusted input, sandboxed execution. |

### Docker Backends: Local vs Remote

The UI offers two Docker modes — both create containers, but target different Docker daemons:

| UI Button | Docker Daemon | When to Use |
|-----------|---------------|-------------|
| **Local Docker** | Local daemon (`/var/run/docker.sock`) | Development, single-machine deploys |
| **Remote Docker** | Remote daemon (`tcp://host:2376`) | Separating control plane from compute |

### wick-daemon (In-Container Daemon)

Docker backends use an in-container daemon for fast command execution. Instead of spawning `docker exec` per command (~60ms overhead), the daemon listens on TCP `:9090` inside the container and accepts NDJSON commands (~2ms):

```
Host (DaemonClient) ──TCP:9090──► Container (wick-daemon) ──sh -c──► command
```

The daemon is automatically injected (`docker cp`) and started when a container launches. If the daemon is unavailable, DockerBackend falls back to `docker exec` transparently.

### Container Control

Stop or restart containers via the Settings panel (stop/restart buttons) or the API:

```bash
# Stop container
curl -X POST /agents/{id}/container -d '{"action":"stop"}'

# Restart container
curl -X POST /agents/{id}/container -d '{"action":"restart"}'
```

### Configuring in main.go

```go
// Local backend
s.RegisterAgent("default", &agent.AgentConfig{
    Backend: &agent.BackendCfg{Type: "local", Workdir: "./workspace"},
    ...
})

// Docker backend
s.RegisterAgent("sandboxed", &agent.AgentConfig{
    Backend: &agent.BackendCfg{
        Type:          "docker",
        Workdir:       "/workspace",
        Image:         "python:3.11-slim",
        ContainerName: "wick-sandbox",
        Timeout:       60,
        MaxOutputBytes: 200000,
    },
    ...
})

// No backend (chat-only) — omit Backend field
```

### Backend Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `Type` | string | — | `"local"`, `"docker"`, or omit for `"state"` |
| `Workdir` | string | `/workspace` | Working directory inside the backend |
| `Timeout` | float64 | `120` | Command execution timeout (seconds) |
| `MaxOutputBytes` | int | `100000` | Max stdout/stderr captured per command |
| `Image` | string | `python:3.11-slim` | Docker image (docker mode only) |
| `ContainerName` | string | `wick-sandbox-{user}` | Container name (docker mode only) |
| `DockerHost` | string | *(local docker)* | Remote Docker daemon URL |

## Agent Configuration

```go
s.RegisterAgent("my-agent", &agent.AgentConfig{
    Name:         "Display Name",
    Model:        "ollama:llama3.1:8b",       // or map[string]any for explicit config
    SystemPrompt: "You are a helpful assistant.",

    // Tools — names of built-in tools the agent can use
    Tools: []string{"internet_search", "calculate", "current_datetime"},

    // Backend — where shell/file tools execute
    Backend: &agent.BackendCfg{Type: "local", Workdir: "./workspace"},

    // Skills — directories containing SKILL.md files
    Skills: &agent.SkillsCfg{Paths: []string{"./skills"}},

    // Memory — directories containing AGENTS.md files
    Memory: &agent.MemoryCfg{Paths: []string{"./AGENTS.md"}},

    // Subagents — delegated specialists
    Subagents: []agent.SubAgentCfg{
        {
            Name:         "researcher",
            Description:  "Research a topic using web search.",
            SystemPrompt: "You are a research assistant.",
            Tools:        []string{"internet_search"},
        },
    },

    // Context window size for summarization hook
    ContextWindow: 128000,

    // Per-agent secrets
    BuiltinConfig: map[string]string{
        "tavily_api_key": "tvly-...",
    },

    Debug: true,
})
```

## Model Specs

The Go LLM resolver handles model specs natively:

| Format | Provider | Credentials |
|--------|----------|-------------|
| `"ollama:llama3.1:8b"` | Ollama (local) | None needed |
| `"openai:gpt-4"` | OpenAI | `OPENAI_API_KEY` env var |
| `"anthropic:claude-3"` | Anthropic | `ANTHROPIC_API_KEY` env var |

### Dict Format (explicit)

```go
Model: map[string]any{
    "provider": "anthropic",
    "model":    "claude-sonnet-4-20250514",
    "api_key":  "sk-...",  // optional, falls back to env var
},
```

## Custom Tools

Define tools as native Go functions — no HTTP callbacks, no serialization:

```go
s.RegisterTool(&agent.FuncTool{
    ToolName: "add",
    ToolDesc: "Add two numbers together.",
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
```

Tools registered via `RegisterTool()` are available to all agents.

## Hooks

Hooks are auto-configured based on agent config:

| Hook | Auto-enabled When | Provides |
|------|-------------------|----------|
| `tracing` | Always | Timed spans for LLM and tool calls |
| `todolist` | Always | `write_todos` tool for task tracking |
| `filesystem` | Backend is configured | `ls`, `read_file`, `write_file`, `edit_file`, `glob`, `grep`, `execute` |
| `skills` | `Skills.Paths` set + backend | Injects skill catalog into system prompt |
| `memory` | `Memory.Paths` set + backend | Loads AGENTS.md memory files into system prompt |
| `summarization` | Always | Compresses conversation near `ContextWindow` limit |

## Skills

Skills are markdown instruction files in `skills/` directories:

```
skills/
  csv-analyzer/
    SKILL.md
    analyze.py        # supporting script (agent invokes via exec_command)
  research/
    SKILL.md
  slides/
    SKILL.md
```

Skills are discovered at runtime. The UI shows them as clickable cards.

## Environment Variables

| Variable | Description |
|----------|-------------|
| `OPENAI_API_KEY` | OpenAI API key |
| `ANTHROPIC_API_KEY` | Anthropic API key |
| `TAVILY_API_KEY` | Web search (injected into `BuiltinConfig`) |
| `OLLAMA_BASE_URL` | Ollama server URL (default: `http://localhost:11434`) |
| `WICK_GATEWAY_URL` | Auth gateway for multi-user mode |
| `PORT` | Server port (default: `8000`) |
| `HOST` | Server bind address (default: `0.0.0.0`) |

## Project Structure

```
wick_go/
  main.go             # Agent definitions, tools, startup (pure Go)
  go.mod              # Module with replace directive → wick_server
  Dockerfile          # Multi-stage: Go build + React build + debian-slim runtime
  Makefile            # Build targets
  skills/             # Skill folders (SKILL.md files)
    code-review/
    csv-analyzer/
    data-analysis/
    research/
    slides/
    summarization/
  ui/                 # React frontend (Vite + TypeScript)
  static/             # Built UI assets (served by Go)
```

## Docker

```bash
# Build (from repo root)
docker build -f wick_go/Dockerfile -t wick-agent .

# Run
docker run -p 8000:8000 wick-agent

# With Docker sandbox support
docker run -p 8000:8000 -v /var/run/docker.sock:/var/run/docker.sock wick-agent
```

The Dockerfile builds three Go binaries (`wick_go`, `wickfs`, `wick-daemon`), bundles React UI assets, and runs on `debian:bookworm-slim` with `python3-minimal` (for skill scripts) and `docker-ce-cli` (for sandbox support). The `wickfs` and `wick-daemon` binaries are placed in `/usr/local/bin/` for injection into sandbox containers. File descriptor limit is raised to 65536 for high concurrency.

## Scaling

A single instance handles ~500 concurrent users on 2 vCPU / 4 GB (with `ulimit -n 65536`). Thread state is evicted after 1 hour of inactivity to prevent memory growth.

The wick-daemon reduces per-command overhead from ~60ms (docker exec) to ~2ms (TCP), enabling high-frequency tool calls at scale. Remote Docker support separates the control plane from compute — the wick_go host can be a small instance while containers run on beefy Docker hosts.

For higher scale, put multiple instances behind an ALB with sticky sessions.

## Development

```bash
# Build Go binary
cd wick_go && go build -o wick_go .

# Run with hot reload (UI dev server)
cd wick_go/ui && npm run dev    # :3000, proxies API to :8000

# Build UI for production
cd wick_go/ui && npm run build -- --outDir ../static
```
