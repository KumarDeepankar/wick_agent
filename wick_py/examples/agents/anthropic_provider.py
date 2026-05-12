"""Native Anthropic provider for the Smart Planner agent.

Streams the Anthropic Messages API directly from Python. Used only by the
`smart-planner` supervisor; the other supervisors keep their own providers.

Why a Python-side provider (instead of the Go-side `provider="anthropic"`):

  * gives us prompt caching out of the box on system prompt + tool schemas
    (large, stable across turns),
  * keeps the Anthropic adapter co-located with the rest of the planner
    code so the model + auth knobs are obvious in `server.py`,
  * mirrors the existing `gateway.py` structure so contributors see one
    consistent pattern for "register provider X on agent Y".

Wire it from `server.py`:

    from agents.anthropic_provider import register_anthropic_provider
    register_anthropic_provider(planner)
"""

from __future__ import annotations

import json
import logging
import os
from typing import Any, Iterator

import httpx

from wick import Agent, LLMMessage, LLMRequest, StreamChunk, ToolCallResult

logger = logging.getLogger("wick.anthropic")

ANTHROPIC_BASE_URL = os.environ.get("ANTHROPIC_BASE_URL", "https://api.anthropic.com")
ANTHROPIC_VERSION = "2023-06-01"
DEFAULT_MODEL = os.environ.get("ANTHROPIC_MODEL", "claude-sonnet-4-6")
DEFAULT_MAX_TOKENS = 4096


# ── Public API ────────────────────────────────────────────────────────────


def register_anthropic_provider(
    agent: Agent,
    model: str = DEFAULT_MODEL,
    *,
    api_key: str | None = None,
    max_tokens: int = DEFAULT_MAX_TOKENS,
) -> None:
    """Attach a native Anthropic LLM provider to the given agent.

    Args:
        agent:       wick Agent to register the provider on.
        model:       Anthropic model id (e.g. "claude-sonnet-4-6").
        api_key:     Override for ANTHROPIC_API_KEY env var.
        max_tokens:  Default cap when the request doesn't specify one.

    The function is idempotent per agent — calling it twice replaces the
    handler. The API key is resolved at call time, not at import time, so
    `register_anthropic_provider(agent)` works even if `ANTHROPIC_API_KEY`
    is set later by `start.py`.
    """
    resolved_key = api_key or os.environ.get("ANTHROPIC_API_KEY")
    if not resolved_key:
        raise RuntimeError(
            "ANTHROPIC_API_KEY is not set. Export it on the host (start.py "
            "auto-passes it into the container) or pass api_key=... explicitly."
        )

    @agent.llm_provider(model)
    async def _anthropic_llm(request: LLMRequest):
        async for chunk in _stream(request, resolved_key, model, max_tokens):
            yield chunk


# ── Streaming ─────────────────────────────────────────────────────────────


async def _stream(
    request: LLMRequest,
    api_key: str,
    default_model: str,
    default_max_tokens: int,
):
    """Stream a Messages-API response, converting events to wick StreamChunks."""
    # The Go server uses `request.model` as an internal routing tag (often
    # prefixed with "proxy:" when the provider is served by the Python
    # sidecar). It is NOT necessarily a real Anthropic model id, so we
    # always send the model that was registered via
    # register_anthropic_provider(...). gateway.py uses the same pattern.
    payload: dict[str, Any] = {
        "model": default_model,
        "max_tokens": request.max_tokens or default_max_tokens,
        "messages": _build_messages(request.messages),
        "stream": True,
    }
    if request.temperature is not None:
        payload["temperature"] = request.temperature

    system_blocks = _build_system(request.system_prompt)
    if system_blocks:
        payload["system"] = system_blocks

    tools = _build_tools(request.tools)
    if tools:
        payload["tools"] = tools

    headers = {
        "x-api-key": api_key,
        "anthropic-version": ANTHROPIC_VERSION,
        "content-type": "application/json",
        # Prompt caching is GA but the header keeps us compatible across SDKs.
        "anthropic-beta": "prompt-caching-2024-07-31",
    }

    timeout = httpx.Timeout(connect=10.0, read=120.0, write=10.0, pool=5.0)

    # Tool-use blocks arrive as a stream of partial JSON inside content_block_delta
    # events. Accumulate them keyed by content-block index, then flush at stop.
    pending_tools: dict[int, dict[str, Any]] = {}

    async with httpx.AsyncClient(timeout=timeout) as client:
        async with client.stream(
            "POST",
            f"{ANTHROPIC_BASE_URL}/v1/messages",
            headers=headers,
            json=payload,
        ) as resp:
            if resp.status_code != 200:
                body = (await resp.aread()).decode("utf-8", errors="replace")
                raise RuntimeError(f"Anthropic {resp.status_code}: {body[:500]}")

            current_event: str | None = None
            async for line in resp.aiter_lines():
                if not line:
                    continue
                if line.startswith("event: "):
                    current_event = line[len("event: "):].strip()
                    continue
                if not line.startswith("data: "):
                    continue
                data = line[len("data: "):]
                if data.strip() == "[DONE]":
                    break
                try:
                    payload_obj = json.loads(data)
                except json.JSONDecodeError:
                    logger.warning("Skipping malformed SSE chunk: %s", data[:120])
                    continue

                async for chunk in _handle_event(current_event, payload_obj, pending_tools):
                    yield chunk

    yield StreamChunk(done=True)


async def _handle_event(
    event: str | None,
    data: dict[str, Any],
    pending_tools: dict[int, dict[str, Any]],
) -> Iterator[StreamChunk]:
    """Translate a single Anthropic SSE event into 0+ wick StreamChunks."""
    etype = data.get("type") or event

    if etype == "content_block_start":
        idx = data.get("index", 0)
        block = data.get("content_block") or {}
        if block.get("type") == "tool_use":
            pending_tools[idx] = {
                "id": block.get("id") or f"tool_{idx}",
                "name": block.get("name") or "",
                "input_buf": "",
            }
        return

    if etype == "content_block_delta":
        idx = data.get("index", 0)
        delta = data.get("delta") or {}
        dtype = delta.get("type")
        if dtype == "text_delta":
            text = delta.get("text") or ""
            if text:
                yield StreamChunk(delta=text)
        elif dtype == "input_json_delta":
            partial = delta.get("partial_json") or ""
            if idx in pending_tools and partial:
                pending_tools[idx]["input_buf"] += partial
        return

    if etype == "content_block_stop":
        idx = data.get("index", 0)
        if idx in pending_tools:
            entry = pending_tools.pop(idx)
            try:
                args = json.loads(entry["input_buf"]) if entry["input_buf"] else {}
            except json.JSONDecodeError:
                logger.error(
                    "Malformed tool-use input for %s: %s",
                    entry["name"], entry["input_buf"][:200],
                )
                args = {}
            yield StreamChunk(tool_call=ToolCallResult(
                id=entry["id"],
                name=entry["name"],
                args=args,
            ))
        return

    if etype in ("message_start", "message_delta", "message_stop", "ping"):
        return

    if etype == "error":
        err = data.get("error") or {}
        raise RuntimeError(f"Anthropic stream error: {err.get('type')}: {err.get('message')}")


# ── Request building ──────────────────────────────────────────────────────


def _build_system(prompt: str | None) -> list[dict[str, Any]]:
    """Anthropic system field as a list of blocks so we can attach a cache marker.

    Caching the system prompt cuts repeated-turn cost since the planner's
    system prompt is large and stable across the conversation.
    """
    if not prompt:
        return []
    return [{
        "type": "text",
        "text": prompt,
        "cache_control": {"type": "ephemeral"},
    }]


def _build_tools(tools) -> list[dict[str, Any]]:
    """Convert wick ToolSchema list → Anthropic tool spec list, with caching
    on the last entry so the whole tool block participates in the cache."""
    if not tools:
        return []
    out: list[dict[str, Any]] = []
    for t in tools:
        out.append({
            "name": t.name,
            "description": t.description,
            "input_schema": t.parameters or {"type": "object", "properties": {}},
        })
    if out:
        out[-1]["cache_control"] = {"type": "ephemeral"}
    return out


def _build_messages(messages: list[LLMMessage]) -> list[dict[str, Any]]:
    """Convert wick messages → Anthropic messages format.

    Key transformations:
      * assistant messages with `tool_calls` become assistant content with
        `tool_use` blocks (text first if any),
      * wick `tool` messages (one per tool result) are coalesced into a
        single user message with one `tool_result` block per result —
        Anthropic requires consecutive tool_results in one user turn.
    """
    out: list[dict[str, Any]] = []
    pending_tool_results: list[dict[str, Any]] = []

    def flush_tool_results() -> None:
        if pending_tool_results:
            out.append({"role": "user", "content": list(pending_tool_results)})
            pending_tool_results.clear()

    for msg in messages:
        if msg.role == "tool":
            pending_tool_results.append({
                "type": "tool_result",
                "tool_use_id": msg.tool_call_id or "",
                "content": msg.content or "",
            })
            continue

        flush_tool_results()

        if msg.role == "user":
            out.append({"role": "user", "content": msg.content or ""})
        elif msg.role == "assistant":
            blocks: list[dict[str, Any]] = []
            if msg.content:
                blocks.append({"type": "text", "text": msg.content})
            for tc in msg.tool_calls or []:
                args = tc.args
                if isinstance(args, str):
                    try:
                        args = json.loads(args)
                    except json.JSONDecodeError:
                        args = {}
                blocks.append({
                    "type": "tool_use",
                    "id": tc.id,
                    "name": tc.name,
                    "input": args or {},
                })
            if not blocks:
                blocks.append({"type": "text", "text": ""})
            out.append({"role": "assistant", "content": blocks})
        elif msg.role == "system":
            # System messages mid-stream are rare; Anthropic keeps system
            # at the top level. Promote to user as a safety fallback.
            out.append({"role": "user", "content": msg.content or ""})

    flush_tool_results()
    return out
