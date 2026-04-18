"""Gateway LLM provider for Claude.

Wires an Anthropic-compatible LLM served via a custom gateway (OpenAI chat
completions format) onto a wick Agent. Owns the auth token refresh loop.

Call `register_gateway_provider(agent)` once during startup.
"""

from __future__ import annotations

import json
import logging
import os
import threading

import httpx

from wick import Agent, LLMRequest, StreamChunk, ToolCallResult

from gateway_auth import fetch_token

logger = logging.getLogger("wick.gateway")

GATEWAY_URL = os.environ.get("GATEWAY_URL", "https://xyz-abc")
GATEWAY_MODEL = os.environ.get("GATEWAY_MODEL", "anthropic.claude-4-5-sonnet-v1:0")
TOKEN_REFRESH_INTERVAL = 20 * 60  # seconds

_gateway_token = ""
_token_lock = threading.Lock()
_refresh_started = False


def _refresh_token() -> None:
    global _gateway_token
    try:
        new_token = fetch_token()
        with _token_lock:
            _gateway_token = new_token
        logger.info("Gateway token refreshed")
    except Exception as e:
        logger.error("Token refresh failed: %s", e)


def _token_refresh_loop() -> None:
    while True:
        _refresh_token()
        threading.Event().wait(TOKEN_REFRESH_INTERVAL)


def _get_token() -> str:
    with _token_lock:
        return _gateway_token


def _ensure_refresh_thread_started() -> None:
    global _refresh_started
    if _refresh_started:
        return
    _refresh_token()
    threading.Thread(target=_token_refresh_loop, daemon=True).start()
    _refresh_started = True


def register_gateway_provider(agent: Agent, model_id: str = "claude-sonnet") -> None:
    """Register the gateway LLM provider on the given agent.

    Starts the background token-refresh thread the first time it's called.
    Subsequent calls reuse the running refresher.
    """
    _ensure_refresh_thread_started()

    @agent.llm_provider(model_id)
    async def _gateway_llm(request: LLMRequest):
        async for chunk in _stream_gateway(request):
            yield chunk


async def _stream_gateway(request: LLMRequest):
    """Stream a response from the gateway (OpenAI chat completions format)."""
    messages = _build_messages(request)
    tools = _build_tools(request)

    payload: dict = {
        "model": GATEWAY_MODEL,
        "max_tokens": request.max_tokens or 4096,
        "messages": messages,
        "stream": True,
    }
    if tools:
        payload["tools"] = tools

    headers = {
        "Content-Type": "application/json",
        "Authorization": f"Bearer {_get_token()}",
    }

    timeout = httpx.Timeout(connect=10.0, read=60.0, write=10.0, pool=5.0)
    pending_tool_calls: dict[int, dict] = {}

    async with httpx.AsyncClient(timeout=timeout) as client:
        async with client.stream(
            "POST",
            f"{GATEWAY_URL}/chat/completions",
            headers=headers,
            json=payload,
        ) as resp:
            resp.raise_for_status()

            async for line in resp.aiter_lines():
                if not line.startswith("data: "):
                    continue
                data = line[6:]
                if data.strip() == "[DONE]":
                    break

                try:
                    chunk = json.loads(data)
                except json.JSONDecodeError:
                    logger.warning("Skipping malformed SSE chunk: %s", data[:120])
                    continue

                choices = chunk.get("choices") or []
                if not choices:
                    continue
                delta = choices[0].get("delta") or {}

                text = delta.get("content")
                if text:
                    yield StreamChunk(delta=text)

                for tc in delta.get("tool_calls") or []:
                    _accumulate_tool_call(pending_tool_calls, tc)

    for idx in sorted(pending_tool_calls):
        entry = pending_tool_calls[idx]
        try:
            args = json.loads(entry["arguments"]) if entry["arguments"] else {}
        except json.JSONDecodeError:
            logger.error(
                "Malformed tool call args for %s: %s",
                entry["name"], entry["arguments"][:200],
            )
            args = {}
        yield StreamChunk(tool_call=ToolCallResult(
            id=entry["id"] or f"call_{idx}",
            name=entry["name"],
            args=args,
        ))

    yield StreamChunk(done=True)


def _build_messages(request: LLMRequest) -> list[dict]:
    """Convert wick messages → OpenAI chat completions format."""
    messages: list[dict] = []
    if request.system_prompt:
        messages.append({"role": "system", "content": request.system_prompt})

    for msg in request.messages:
        if msg.role == "user":
            messages.append({"role": "user", "content": msg.content})
        elif msg.role == "assistant":
            m: dict = {"role": "assistant", "content": msg.content or ""}
            if getattr(msg, "tool_calls", None):
                m["tool_calls"] = [
                    {
                        "id": tc.id,
                        "type": "function",
                        "function": {
                            "name": tc.name,
                            "arguments": (
                                json.dumps(tc.args)
                                if isinstance(tc.args, dict)
                                else tc.args
                            ),
                        },
                    }
                    for tc in msg.tool_calls
                ]
            messages.append(m)
        elif msg.role == "tool":
            messages.append({
                "role": "tool",
                "tool_call_id": msg.tool_call_id,
                "content": msg.content or "",
            })
    return messages


def _build_tools(request: LLMRequest) -> list[dict]:
    """Convert wick tool schemas → OpenAI function-calling format."""
    if not request.tools:
        return []
    return [
        {
            "type": "function",
            "function": {
                "name": t.name,
                "description": t.description,
                "parameters": t.parameters,
            },
        }
        for t in request.tools
    ]


def _accumulate_tool_call(pending: dict[int, dict], tc: dict) -> None:
    """Merge a tool-call SSE fragment into the pending accumulator."""
    idx = tc.get("index", 0)
    entry = pending.setdefault(idx, {"id": "", "name": "", "arguments": ""})
    if tc.get("id"):
        entry["id"] = tc["id"]
    func = tc.get("function") or {}
    if func.get("name"):
        entry["name"] = func["name"]
    if func.get("arguments"):
        entry["arguments"] += func["arguments"]
