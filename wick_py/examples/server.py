"""Entry point: build the supervisor agents and start the wick server.

The actual agent logic lives in `agents/`:
  - agents/prompts/*.md   — system prompts (one per agent)
  - agents/tools.py       — global @tool definitions
  - agents/config.py      — SharedConfig (backend, skills, debug)
  - agents/subagents.py   — sub-agent factories
  - agents/supervisors.py — supervisor (top-level) agent factories
  - agents/gateway.py     — gateway LLM provider + token refresh
"""

from __future__ import annotations

import os

from agents.config import load_shared_config
from agents.gateway import register_gateway_provider
from agents.supervisors import build_claude_agent, build_ollama_agent


def main() -> None:
    cfg = load_shared_config()
    claude = build_claude_agent(cfg)
    ollama = build_ollama_agent(cfg)

    register_gateway_provider(claude)

    claude.run(
        go_binary=os.environ.get("WICK_SERVER_BINARY"),
        go_port=8000,
        sidecar_port=9100,
        extra_agents=[ollama],
    )


if __name__ == "__main__":
    main()
