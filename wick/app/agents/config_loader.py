"""YAML-based agent configuration persistence.

Reads agent definitions from `agents.yaml` on startup and writes
back whenever agents are created, updated, or deleted via the API.

File layout:
  defaults:   Global defaults applied to all agents
  agents:     Dict of agent_id → full agent config (all knobs)
"""

from __future__ import annotations

import copy
import logging
from pathlib import Path
from typing import Any

import yaml

from app.agents.deep_agent import register_template, list_tools
from app.agents.mcp_client import load_mcp_tools

logger = logging.getLogger(__name__)

CONFIG_PATH = Path(__file__).resolve().parent.parent.parent / "agents.yaml"


# ═══════════════════════════════════════════════════════════════════════════
# Read
# ═══════════════════════════════════════════════════════════════════════════


def _read_yaml() -> dict[str, Any]:
    """Read and parse agents.yaml, returning the full dict."""
    if not CONFIG_PATH.exists():
        logger.warning("agents.yaml not found at %s — starting with empty config", CONFIG_PATH)
        return {"defaults": {}, "agents": {}}
    with open(CONFIG_PATH) as f:
        data = yaml.safe_load(f) or {}
    return data


def _apply_defaults(
    agent_cfg: dict[str, Any],
    defaults: dict[str, Any],
) -> dict[str, Any]:
    """Merge defaults into an agent config (agent values take precedence)."""
    merged = copy.deepcopy(defaults)

    for key, value in agent_cfg.items():
        if key in merged and isinstance(merged[key], dict) and isinstance(value, dict):
            # Deep-merge one level for nested configs (backend, cache, etc.)
            merged[key] = {**merged[key], **value}
        else:
            merged[key] = value

    return merged


def load_agents_from_yaml() -> int:
    """Load all agents from agents.yaml, creating them in the registry.

    Returns the number of agents loaded.
    """
    data = _read_yaml()
    defaults = data.get("defaults", {})
    agents_cfg = data.get("agents", {})

    # ── Discover and register MCP tools before creating agents ───────────
    mcp_servers_cfg = data.get("mcp_servers", {})
    if mcp_servers_cfg:
        mcp_count = load_mcp_tools(mcp_servers_cfg)
        logger.info("Registered %d MCP tool(s) from %d server(s)", mcp_count, len(mcp_servers_cfg))

    loaded = 0

    for agent_id, raw_cfg in agents_cfg.items():
        if raw_cfg is None:
            raw_cfg = {}

        cfg = _apply_defaults(raw_cfg, defaults)

        # Drop tools that aren't registered (e.g. MCP server was unreachable)
        requested_tools = cfg.get("tools") or []
        available = set(list_tools())
        valid_tools = [t for t in requested_tools if t in available]
        skipped = set(requested_tools) - available
        if skipped:
            logger.warning(
                "Agent '%s': skipping unavailable tools: %s",
                agent_id, sorted(skipped),
            )

        try:
            register_template(
                agent_id=agent_id,
                name=cfg.get("name"),
                model=cfg.get("model"),
                system_prompt=cfg.get("system_prompt"),
                tool_names=valid_tools or None,
                middleware_names=cfg.get("middleware"),
                subagents=cfg.get("subagents"),
                backend_cfg=cfg.get("backend"),
                interrupt_on_cfg=cfg.get("interrupt_on"),
                skills_cfg=cfg.get("skills") if isinstance(cfg.get("skills"), dict) else None,
                memory_cfg=cfg.get("memory") if isinstance(cfg.get("memory"), dict) else None,
                response_format_cfg=cfg.get("response_format"),
                cache_cfg=cfg.get("cache"),
                debug=cfg.get("debug", False),
            )
            loaded += 1
            logger.info("Registered agent template '%s' from agents.yaml", agent_id)
        except Exception:
            logger.exception("Failed to register template '%s' from agents.yaml", agent_id)

    return loaded


# ═══════════════════════════════════════════════════════════════════════════
# Write
# ═══════════════════════════════════════════════════════════════════════════


def _write_yaml(data: dict[str, Any]) -> None:
    """Write the full config dict back to agents.yaml."""
    with open(CONFIG_PATH, "w") as f:
        yaml.dump(
            data,
            f,
            default_flow_style=False,
            sort_keys=False,
            allow_unicode=True,
            width=120,
        )
    logger.info("agents.yaml updated at %s", CONFIG_PATH)


def save_agent_to_yaml(
    agent_id: str,
    *,
    name: str | None = None,
    model: str | None = None,
    system_prompt: str | None = None,
    tools: list[str] | None = None,
    middleware: list[str] | None = None,
    subagents: list[dict[str, Any]] | None = None,
    backend_cfg: dict[str, Any] | None = None,
    interrupt_on_cfg: dict[str, Any] | None = None,
    skills_cfg: dict[str, Any] | None = None,
    memory_cfg: dict[str, Any] | None = None,
    response_format_cfg: dict[str, Any] | None = None,
    cache_cfg: dict[str, Any] | None = None,
    debug: bool = False,
) -> None:
    """Persist a single agent's config into agents.yaml."""
    data = _read_yaml()
    if "agents" not in data:
        data["agents"] = {}

    # Build the agent entry — only include non-default/non-empty values
    entry: dict[str, Any] = {}
    if name is not None:
        entry["name"] = name
    if model is not None:
        entry["model"] = model
    if system_prompt is not None:
        entry["system_prompt"] = system_prompt
    if tools is not None:
        entry["tools"] = tools
    if middleware is not None:
        entry["middleware"] = middleware
    if subagents:
        # Strip resolved callables, keep only serializable keys
        clean_subagents = []
        for sa in subagents:
            clean = {
                "name": sa["name"],
                "description": sa["description"],
                "system_prompt": sa["system_prompt"],
            }
            if sa.get("tools"):
                clean["tools"] = sa["tools"]
            if sa.get("model"):
                clean["model"] = sa["model"]
            if sa.get("middleware"):
                clean["middleware"] = sa["middleware"]
            clean_subagents.append(clean)
        entry["subagents"] = clean_subagents
    if backend_cfg:
        entry["backend"] = backend_cfg
    if interrupt_on_cfg:
        entry["interrupt_on"] = interrupt_on_cfg
    if skills_cfg:
        entry["skills"] = skills_cfg
    if memory_cfg:
        entry["memory"] = memory_cfg
    if response_format_cfg:
        entry["response_format"] = response_format_cfg
    if cache_cfg:
        entry["cache"] = cache_cfg
    if debug:
        entry["debug"] = debug

    data["agents"][agent_id] = entry
    _write_yaml(data)


def remove_agent_from_yaml(agent_id: str) -> None:
    """Remove an agent entry from agents.yaml."""
    data = _read_yaml()
    agents = data.get("agents", {})
    if agent_id in agents:
        del agents[agent_id]
        data["agents"] = agents
        _write_yaml(data)
