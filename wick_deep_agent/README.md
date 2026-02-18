# wick-deep-agent

Python client and server manager for the wick_go agent server.

Provides a typed HTTP client (`WickClient`), a server lifecycle manager (`WickServer`),
and a `wick-agent` CLI for building, starting, stopping, and inspecting the Go server.

## Installation

```bash
pip install -e .
```

With development tools:

```bash
pip install -e ".[dev]"
```

## Quick Start

### Python

```python
from wick_deep_agent import WickClient, WickServer
from wick_deep_agent.messages import HumanMessage, SystemMessage, Messages

# Build the server binary
WickServer.build()

# Start with inline agent config — no YAML file needed
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

# Clean up
server.stop()
```

### CLI

```bash
wick-agent build
wick-agent start --port 8000
wick-agent status
wick-agent logs -n 100
wick-agent stop
wick-agent systemd --binary ./server/wick_go
```

## Import Structure

```python
# Core API
from wick_deep_agent import WickClient, WickServer

# Message types
from wick_deep_agent.messages import (
    HumanMessage,
    SystemMessage,
    AIMessage,
    ToolMessage,
    Messages,
)
```

## Project Layout

```
wick_deep_agent/
├── wick_deep_agent/     # Python package
│   ├── __init__.py      # WickClient, WickServer
│   ├── client.py        # WickClient — typed HTTP client
│   ├── launcher.py      # WickServer — server lifecycle manager
│   ├── cli.py           # wick-agent CLI entry point
│   └── messages.py      # HumanMessage, SystemMessage, Messages, ...
├── server/              # Go server source (wick_go)
├── pyproject.toml
├── LICENSE
└── README.md
```

## License

Apache License 2.0 — see [LICENSE](LICENSE).
