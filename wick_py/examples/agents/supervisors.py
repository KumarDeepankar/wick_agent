"""Supervisor (top-level) agent factories.

Supervisors are the user-facing agents that receive chat messages directly
and orchestrate sub-agents via delegate_to_agent / start_async_task. Each
build function is symmetric: it takes a SharedConfig and returns an Agent.
"""

from __future__ import annotations

import os

from wick import Agent

from . import tools as _tools  # noqa: F401 — register @tool functions on import
from .config import MAIN_SYSTEM_PROMPT, SharedConfig
from .subagents import (
    build_batch_processor,
    build_math_agent,
    build_report_agent,
    build_summarizer,
)


def build_claude_agent(cfg: SharedConfig) -> Agent:
    """The Claude-via-gateway supervisor agent.

    The gateway LLM provider is attached separately in `gateway.register_gateway_provider`.
    """
    return Agent(
        "gateway-claude",
        name="Claude",
        system_prompt=MAIN_SYSTEM_PROMPT,
        builtin_tools=["calculate", "current_datetime"],
        subagents=[
            build_report_agent(),
            build_batch_processor(),
            build_summarizer(),
        ],
        backend=cfg.backend,
        skills=cfg.skills,
        debug=cfg.debug,
    )


def build_ollama_agent(cfg: SharedConfig) -> Agent:
    """The Ollama (local, Go-native LLM) supervisor agent."""
    ollama_host = os.environ.get("OLLAMA_HOST", "http://localhost:11434")
    return Agent(
        "default",
        name="Ollama Local",
        model={
            "provider": "ollama",
            "model": "llama3.1:8b",
            "base_url": f"{ollama_host}/v1",
        },
        system_prompt=MAIN_SYSTEM_PROMPT,
        subagents=[
            build_math_agent(),
            build_report_agent(),
            build_batch_processor(),
            build_summarizer(),
        ],
        backend=cfg.backend,
        skills=cfg.skills,
        debug=cfg.debug,
    )
