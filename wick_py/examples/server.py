"""Entry point: define the supervisors and run the wick server.

Edit settings in the "Settings" section. Edit supervisor definitions in the
"Supervisors" section. Everything else lives in `agents/`:

    agents/prompts/*.md  — system prompts (one per agent)
    agents/tools.py      — global @tool definitions
    agents/subagents.py  — sub-agent factories
    agents/gateway.py    — gateway LLM provider + token refresh
"""

from __future__ import annotations

import os
from pathlib import Path

from wick import Agent, SkillsConfig

from agents import prompts
from agents import tools as _tools  # noqa: F401 — registers @tool on import
from agents.gateway import register_gateway_provider
from agents.subagents import (
    build_batch_processor,
    build_math_agent,
    build_report_agent,
    build_summarizer,
)


# ── Settings ────────────────────────────────────────────────────────────
# Edit these. Every supervisor below reads from these constants.

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
DEFAULT_SKILLS_DIR = REPO_ROOT / "wick_py" / "skills"
SKILLS_DIR = os.environ.get("WICK_SKILLS_DIR") or str(DEFAULT_SKILLS_DIR)

BACKEND = {"type": "local", "workdir": "/workspace"}
SKILLS = SkillsConfig(paths=[SKILLS_DIR], exclude=["slides", "report-generator"])
DEBUG = True
SYSTEM_PROMPT = prompts.load("main")

OLLAMA_HOST = os.environ.get("OLLAMA_HOST", "http://localhost:11434")


# ── Supervisors ─────────────────────────────────────────────────────────
# Each supervisor is a top-level Agent that receives user messages directly
# and orchestrates sub-agents via delegate_to_agent / start_async_task.
# To add a supervisor: copy a block below, rename the id, edit the values.

claude = Agent(
    "gateway-claude",
    name="Claude",
    system_prompt=SYSTEM_PROMPT,
    builtin_tools=["calculate", "current_datetime"],
    subagents=[
        build_report_agent(),
        build_batch_processor(),
        build_summarizer(),
    ],
    backend=BACKEND,
    skills=SKILLS,
    debug=DEBUG,
)

ollama = Agent(
    "default",
    name="Ollama Local",
    model={
        "provider": "ollama",
        "model": "llama3.1:8b",
        "base_url": f"{OLLAMA_HOST}/v1",
    },
    system_prompt=SYSTEM_PROMPT,
    subagents=[
        build_math_agent(),
        build_report_agent(),
        build_batch_processor(),
        build_summarizer(),
    ],
    backend=BACKEND,
    skills=SKILLS,
    debug=DEBUG,
)


# ── Register LLM providers ──────────────────────────────────────────────
# The gateway provider is attached here (not inside Agent(...)) because it
# needs network access and a background token-refresh thread.

register_gateway_provider(claude)


# ── Run ─────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    claude.run(
        go_binary=os.environ.get("WICK_SERVER_BINARY"),
        go_port=8000,
        sidecar_port=9100,
        extra_agents=[ollama],
    )
