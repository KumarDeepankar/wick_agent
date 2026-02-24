# wick_go

A Python-driven AI agent app powered by the `wick_deep_agent` Go execution engine.

The Go server is a **dumb execution engine** — it starts with zero agents and zero config. All agent definitions, model credentials, tools, and skills are pushed from Python at startup via REST API.

## Quick Start

```bash
# Dev mode: build Go binary + start server
python app.py --build

# Production: build everything (Go + UI) and run
make prod
```

## Architecture

```
app.py (Python)                          Go Server (wick_deep_agent)
───────────────                          ──────────────────────────
Defines agents, models,        start()   Starts with 0 agents
tools, skills, defaults   ──────────►    Listens on :8000

register_agents()          POST /agents/ Stores agent configs
  - resolves model specs   ──────────►   Resolves LLM clients
  - injects API keys                     Creates backends
  - sends full config                    Wires hooks + tools

register_tools()           POST /tools/  Registers HTTP callback
  - @tool decorated fns    ──────────►   tools (add, weather, etc.)

UI (React)                 GET/POST      Renders chat, settings,
  - chat interface         ◄──────────►  skills, file browser
  - settings panel
  - skill cards
```

## Execution Modes (Backends)

There are **3 backend types** that control where agent tools (execute, write_file, ls, etc.) run:

| Type | Description | Use Case |
|------|-------------|----------|
| `state` | No execution backend. Agent can only chat — no file/shell tools. | Default when no backend is configured. Chat-only agents. |
| `local` | Commands run directly on the host via `sh -c`. | Development, trusted environments. |
| `docker` | Commands run inside an isolated Docker container. | Production, untrusted input, sandboxed execution. |

### Configuring in app.py

Set the backend in `DEFAULTS` (applies to all agents) or per-agent:

```python
# All agents use Docker
DEFAULTS = {
    "backend": {"type": "docker", "workdir": "/workspace", "image": "python:3.11-slim"},
}

# All agents use local shell
DEFAULTS = {
    "backend": {"type": "local", "workdir": "/workspace"},
}

# No backend (chat-only)
DEFAULTS = {
    # omit "backend" entirely
}

# Per-agent override
AGENTS = {
    "default": {
        "backend": {"type": "local", "workdir": "/tmp/sandbox"},
        ...
    },
    "sandboxed": {
        "backend": {
            "type": "docker",
            "workdir": "/workspace",
            "image": "node:20-slim",
            "container_name": "wick-node-sandbox",
            "timeout": 60,
            "max_output_bytes": 200000,
        },
        ...
    },
}
```

### Backend Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `type` | string | — | `"local"`, `"docker"`, or omit for `"state"` |
| `workdir` | string | `/workspace` | Working directory inside the backend |
| `timeout` | float | `120` | Command execution timeout (seconds) |
| `max_output_bytes` | int | `100000` | Max stdout/stderr captured per command |
| `image` | string | `python:3.11-slim` | Docker image (docker mode only) |
| `container_name` | string | `wick-sandbox-{user}` | Container name (docker mode only) |
| `docker_host` | string | *(local docker)* | Remote Docker daemon URL, e.g. `tcp://host:2375` |

### Runtime Override via UI

The Settings panel has a **Local / Remote** toggle that calls `PATCH /agents/{id}/backend`. This overrides the backend for the current session only — restarting the server restores the `app.py` config.

## Agent Configuration

Each agent in the `AGENTS` dict supports these fields:

```python
AGENTS = {
    "my-agent": {
        # Required
        "name": "Display Name",
        "model": "ollama:llama3.1:8b",       # see Model Specs below
        "system_prompt": "You are helpful.",

        # Tools — names of tools the agent can use
        "tools": ["calculate", "current_datetime", "internet_search"],

        # Backend — where shell/file tools execute (see above)
        "backend": {"type": "local", "workdir": "/workspace"},

        # Skills — directories containing SKILL.md files
        "skills": {"paths": ["/absolute/path/to/skills"]},

        # Memory — directories containing AGENTS.md files
        "memory": {"paths": ["/absolute/path/to/memory"]},

        # Subagents — delegated specialists
        "subagents": [
            {
                "name": "researcher",
                "description": "Research a topic using web search.",
                "system_prompt": "You are a research assistant.",
                "tools": ["internet_search"],
                "model": "ollama:llama3.1:8b",  # optional, inherits parent
            }
        ],

        # Middleware — processing pipeline names
        "middleware": [],

        # Context window size for summarization hook (default: 128000)
        "context_window": 128000,

        # Per-agent secrets (not from server env)
        "builtin_config": {
            "tavily_api_key": "tvly-...",
        },

        # Debug logging
        "debug": True,
    },
}
```

## Model Specs

Models are resolved by the Python launcher into self-contained dicts before being sent to Go. The Go server never reads API keys from environment variables.

### String Shortcuts (resolved by Python)

| Format | Provider | Credentials Source |
|--------|----------|--------------------|
| `"ollama:llama3.1:8b"` | Ollama (local) | None needed |
| `"openai:gpt-4"` | OpenAI | `OPENAI_API_KEY` env var |
| `"anthropic:claude-3"` | Anthropic | `ANTHROPIC_API_KEY` env var |
| `"gateway:my-model"` | OpenAI-compatible proxy | `GATEWAY_BASE_URL` + `GATEWAY_API_KEY` env vars |
| `"llama3.1:8b"` | Ollama (no prefix) | None needed |

### Dict Format (explicit, pass-through)

```python
"model": {
    "provider": "openai",
    "model": "gpt-4",
    "api_key": "sk-...",
    "base_url": "https://api.openai.com/v1",  # optional
}
```

### Custom Models (`@model` decorator)

For non-standard APIs (e.g. AWS Bedrock with SigV4 auth):

```python
from wick_deep_agent import model

@model(name="bedrock-claude")
class BedrockClaude:
    def call(self, request):
        # Custom request/response handling
        return {"content": "...", "tool_calls": []}

    def stream(self, request):  # optional
        yield {"delta": "chunk"}
        yield {"done": True}

AGENTS = {"default": {"model": BedrockClaude, ...}}
```

Custom models run as a Python sidecar — the Go server proxies LLM calls back to Python via HTTP callback.

## Hooks

Hooks are middleware that wrap the agent loop. They are auto-configured based on agent config but can be toggled at runtime via the UI Settings panel.

| Hook | Auto-enabled When | Provides |
|------|-------------------|----------|
| `tracing` | Always | Timed spans for LLM and tool calls |
| `todolist` | Always | `write_todos` tool for task tracking |
| `filesystem` | Backend is configured | `ls`, `read_file`, `write_file`, `edit_file`, `glob`, `grep`, `execute` |
| `skills` | `skills.paths` set + backend | Injects skill catalog into system prompt |
| `memory` | `memory.paths` set + backend | Loads AGENTS.md memory files into system prompt |
| `summarization` | Always | Compresses conversation when nearing `context_window` limit |

## Tools

### Built-in Tools

| Tool | Description | Requires |
|------|-------------|----------|
| `calculate` | Evaluate math expressions | Nothing |
| `current_datetime` | Get current date/time | Nothing |
| `internet_search` | Web search via Tavily | `builtin_config.tavily_api_key` |

### Filesystem Tools (from `filesystem` hook)

`ls`, `read_file`, `write_file`, `edit_file`, `glob`, `grep`, `execute` — available when a backend is configured.

### External Tools (`@tool` decorator)

Define custom tools in `app.py` that run in the Python process:

```python
from wick_deep_agent import tool

@tool(description="Add two numbers together.")
def add(a: int, b: int) -> str:
    return str(a + b)

server = WickServer(tools=[add], ...)
```

These are registered via HTTP callback — the Go server calls back to Python when the LLM invokes them.

## Skills

Skills are markdown instruction files that teach the agent specific workflows.

### Adding a Skill

Create a folder under `skills/` with a `SKILL.md`:

```
skills/
  my-skill/
    SKILL.md
    helper.py        # optional supporting files
```

`SKILL.md` format:

```markdown
---
name: my-skill
description: One-line description of when to use this skill.
icon: wrench
sample-prompts:
- Example prompt that triggers this skill
- Another example
---

## Instructions

Detailed instructions for the LLM go here.
The agent reads this before executing the skill.
```

Skills are discovered at runtime — no rebuild or restart needed. The UI shows them as clickable cards on the welcome screen.

### Available Icons

`code`, `table`, `bar-chart`, `search`, `slides`, `document`, `wrench`

## Environment Variables

All credentials are resolved by the Python launcher, not the Go server.

| Variable | Used By | Description |
|----------|---------|-------------|
| `OPENAI_API_KEY` | `_resolve_model_spec()` | OpenAI API key |
| `ANTHROPIC_API_KEY` | `_resolve_model_spec()` | Anthropic API key |
| `TAVILY_API_KEY` | `register_agents()` | Injected into `builtin_config` |
| `OLLAMA_BASE_URL` | `_resolve_model_spec()` | Ollama server URL (default: `http://localhost:11434`) |
| `GATEWAY_BASE_URL` | `_resolve_model_spec()` | OpenAI-compatible proxy URL |
| `GATEWAY_API_KEY` | `_resolve_model_spec()` | Proxy API key |
| `WICK_GATEWAY_URL` | Go server | Auth gateway for multi-user mode |
| `PORT` | Go server | Server port (default: `8000`) |
| `HOST` | Go server | Server bind address (default: `0.0.0.0`) |

## Project Structure

```
wick_go/
  app.py              # Agent definitions, tools, startup
  models.py           # Custom model definitions (@model)
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

## Development

```bash
# Build Go binary only
make build-server

# Build UI only
make build-ui

# Dev mode (Go server + Vite hot reload on :3000)
make dev

# Full production build
make build
```
