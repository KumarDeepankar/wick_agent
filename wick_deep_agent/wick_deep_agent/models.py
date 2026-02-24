"""Pure-Python LLM model abstraction with built-in OpenAI and Anthropic support.

Provides a ``Model`` ABC that lets users define custom LLM clients, plus
built-in implementations for OpenAI-compatible and Anthropic APIs — all
working independently of the Go server.

Import via the submodule::

    from wick_deep_agent.models import OpenAIModel, AnthropicModel, resolve
"""

from __future__ import annotations

import abc
import json
import os
from dataclasses import dataclass, field
from typing import Any, Iterator, Optional, Union

import requests

from .client import MessageInput, _normalize_messages

# ---------------------------------------------------------------------------
# Data types
# ---------------------------------------------------------------------------

__all__ = [
    "AnthropicModel",
    "Model",
    "ModelError",
    "ModelResponse",
    "OpenAIModel",
    "StreamChunk",
    "ToolCallResult",
    "ToolSchema",
    "resolve",
]


@dataclass
class ToolCallResult:
    """A single tool call returned by the model."""

    id: str
    name: str
    args: dict[str, Any]


@dataclass
class ModelResponse:
    """Non-streaming response from a model."""

    content: str
    tool_calls: list[ToolCallResult] = field(default_factory=list)


@dataclass
class StreamChunk:
    """A single chunk from a streaming response.

    Every ``stream()`` call must end with ``StreamChunk(done=True)``.
    """

    delta: str = ""
    tool_call: Optional[ToolCallResult] = None
    done: bool = False


@dataclass
class ToolSchema:
    """Tool definition passed to the model."""

    name: str
    description: str
    parameters: dict[str, Any]  # JSON Schema


class ModelError(Exception):
    """Error from an LLM API call."""

    def __init__(self, status_code: int, body: str) -> None:
        self.status_code = status_code
        self.body = body
        super().__init__(f"HTTP {status_code}: {body}")


# ---------------------------------------------------------------------------
# Model ABC — template method pattern
# ---------------------------------------------------------------------------


@dataclass
class Model(abc.ABC):
    """Abstract base model with template-method invoke/stream pair.

    Subclasses implement **at least one** of ``invoke()`` or ``stream()``
    and get the other for free.  If neither is overridden, both raise
    ``RuntimeError`` at call time.
    """

    model: str

    # Recursion guard — prevents infinite delegation between defaults.
    _guard: bool = field(default=False, init=False, repr=False, compare=False)

    def invoke(
        self,
        messages: MessageInput,
        *,
        system_prompt: str = "",
        tools: Optional[list[ToolSchema]] = None,
        max_tokens: int = 0,
        temperature: Optional[float] = None,
    ) -> ModelResponse:
        """Default: drain ``stream()``, accumulate into ``ModelResponse``."""
        if self._guard:
            raise RuntimeError(
                "Model subclass must implement invoke() or stream()"
            )
        self._guard = True
        try:
            content_parts: list[str] = []
            tool_calls: list[ToolCallResult] = []
            for chunk in self.stream(
                messages,
                system_prompt=system_prompt,
                tools=tools,
                max_tokens=max_tokens,
                temperature=temperature,
            ):
                if chunk.done:
                    break
                if chunk.delta:
                    content_parts.append(chunk.delta)
                if chunk.tool_call:
                    tool_calls.append(chunk.tool_call)
            return ModelResponse(
                content="".join(content_parts), tool_calls=tool_calls
            )
        finally:
            self._guard = False

    def stream(
        self,
        messages: MessageInput,
        *,
        system_prompt: str = "",
        tools: Optional[list[ToolSchema]] = None,
        max_tokens: int = 0,
        temperature: Optional[float] = None,
    ) -> Iterator[StreamChunk]:
        """Default: call ``invoke()``, emit result as chunks + done sentinel."""
        if self._guard:
            raise RuntimeError(
                "Model subclass must implement invoke() or stream()"
            )
        self._guard = True
        try:
            resp = self.invoke(
                messages,
                system_prompt=system_prompt,
                tools=tools,
                max_tokens=max_tokens,
                temperature=temperature,
            )
        finally:
            self._guard = False
        if resp.content:
            yield StreamChunk(delta=resp.content)
        for tc in resp.tool_calls:
            yield StreamChunk(tool_call=tc)
        yield StreamChunk(done=True)


# ---------------------------------------------------------------------------
# OpenAI-compatible implementation
# ---------------------------------------------------------------------------


def _tools_to_openai(tools: list[ToolSchema]) -> list[dict[str, Any]]:
    return [
        {
            "type": "function",
            "function": {
                "name": t.name,
                "description": t.description,
                "parameters": t.parameters,
            },
        }
        for t in tools
    ]


def _messages_to_openai(
    msgs: list[dict[str, Any]], system_prompt: str
) -> list[dict[str, Any]]:
    """Convert normalised message dicts to OpenAI chat format."""
    out: list[dict[str, Any]] = []
    if system_prompt:
        out.append({"role": "system", "content": system_prompt})
    for m in msgs:
        role = m.get("role", "user")
        if role == "tool":
            out.append(
                {
                    "role": "tool",
                    "tool_call_id": m.get("tool_call_id", ""),
                    "content": m.get("content", ""),
                }
            )
        elif role == "assistant" and m.get("tool_calls"):
            entry: dict[str, Any] = {
                "role": "assistant",
                "content": m.get("content", ""),
            }
            entry["tool_calls"] = [
                {
                    "id": tc.get("id", ""),
                    "type": "function",
                    "function": {
                        "name": tc.get("name", ""),
                        "arguments": json.dumps(tc.get("args", {})),
                    },
                }
                for tc in m["tool_calls"]
            ]
            out.append(entry)
        else:
            out.append({"role": role, "content": m.get("content", "")})
    return out


def _parse_openai_tool_calls(
    raw_calls: list[dict[str, Any]],
) -> list[ToolCallResult]:
    results: list[ToolCallResult] = []
    for tc in raw_calls:
        func = tc.get("function", {})
        args_str = func.get("arguments", "{}")
        try:
            args = json.loads(args_str)
        except json.JSONDecodeError:
            args = {"_raw": args_str}
        results.append(
            ToolCallResult(
                id=tc.get("id", ""),
                name=func.get("name", ""),
                args=args,
            )
        )
    return results


@dataclass
class OpenAIModel(Model):
    """OpenAI-compatible model (works with OpenAI, Ollama, vLLM, LiteLLM, etc.)."""

    base_url: str = "https://api.openai.com/v1"
    api_key: str = ""
    timeout: int = 300

    def _headers(self) -> dict[str, str]:
        headers: dict[str, str] = {"Content-Type": "application/json"}
        key = self.api_key or os.environ.get("OPENAI_API_KEY", "")
        if key and key.lower() != "ollama":
            headers["Authorization"] = f"Bearer {key}"
        return headers

    def _build_body(
        self,
        msgs: list[dict[str, Any]],
        system_prompt: str,
        tools: Optional[list[ToolSchema]],
        max_tokens: int,
        temperature: Optional[float],
        stream: bool,
    ) -> dict[str, Any]:
        body: dict[str, Any] = {
            "model": self.model,
            "messages": _messages_to_openai(msgs, system_prompt),
            "stream": stream,
        }
        if tools:
            body["tools"] = _tools_to_openai(tools)
        if max_tokens > 0:
            body["max_tokens"] = max_tokens
        if temperature is not None:
            body["temperature"] = temperature
        return body

    def invoke(
        self,
        messages: MessageInput,
        *,
        system_prompt: str = "",
        tools: Optional[list[ToolSchema]] = None,
        max_tokens: int = 0,
        temperature: Optional[float] = None,
    ) -> ModelResponse:
        msgs = _normalize_messages(messages)
        body = self._build_body(
            msgs, system_prompt, tools, max_tokens, temperature, stream=False
        )
        url = f"{self.base_url.rstrip('/')}/chat/completions"
        resp = requests.post(
            url,
            json=body,
            headers=self._headers(),
            timeout=self.timeout,
        )
        if resp.status_code != 200:
            raise ModelError(resp.status_code, resp.text)
        data = resp.json()
        choice = data["choices"][0]
        msg = choice["message"]
        tool_calls = _parse_openai_tool_calls(msg.get("tool_calls") or [])
        return ModelResponse(
            content=msg.get("content") or "", tool_calls=tool_calls
        )

    def stream(
        self,
        messages: MessageInput,
        *,
        system_prompt: str = "",
        tools: Optional[list[ToolSchema]] = None,
        max_tokens: int = 0,
        temperature: Optional[float] = None,
    ) -> Iterator[StreamChunk]:
        msgs = _normalize_messages(messages)
        body = self._build_body(
            msgs, system_prompt, tools, max_tokens, temperature, stream=True
        )
        url = f"{self.base_url.rstrip('/')}/chat/completions"
        resp = requests.post(
            url,
            json=body,
            headers=self._headers(),
            stream=True,
            timeout=self.timeout,
        )
        if resp.status_code != 200:
            raise ModelError(resp.status_code, resp.text)

        # Accumulate tool call arguments by index.
        pending_tool_calls: dict[int, dict[str, Any]] = {}

        for raw_line in resp.iter_lines(decode_unicode=True):
            if not raw_line or not raw_line.startswith("data:"):
                continue
            payload = raw_line[5:].strip()
            if payload == "[DONE]":
                break
            try:
                data = json.loads(payload)
            except json.JSONDecodeError:
                continue
            delta = data.get("choices", [{}])[0].get("delta", {})
            content = delta.get("content")
            if content:
                yield StreamChunk(delta=content)
            # Accumulate streamed tool calls.
            for tc_delta in delta.get("tool_calls") or []:
                idx = tc_delta.get("index", 0)
                if idx not in pending_tool_calls:
                    pending_tool_calls[idx] = {
                        "id": tc_delta.get("id", ""),
                        "name": tc_delta.get("function", {}).get("name", ""),
                        "arguments": "",
                    }
                entry = pending_tool_calls[idx]
                if tc_delta.get("id"):
                    entry["id"] = tc_delta["id"]
                fn = tc_delta.get("function", {})
                if fn.get("name"):
                    entry["name"] = fn["name"]
                if fn.get("arguments"):
                    entry["arguments"] += fn["arguments"]

        # Emit accumulated tool calls.
        for _idx in sorted(pending_tool_calls):
            entry = pending_tool_calls[_idx]
            try:
                args = json.loads(entry["arguments"])
            except json.JSONDecodeError:
                args = {"_raw": entry["arguments"]}
            yield StreamChunk(
                tool_call=ToolCallResult(
                    id=entry["id"], name=entry["name"], args=args
                )
            )
        yield StreamChunk(done=True)


# ---------------------------------------------------------------------------
# Anthropic implementation
# ---------------------------------------------------------------------------


def _tools_to_anthropic(tools: list[ToolSchema]) -> list[dict[str, Any]]:
    return [
        {
            "name": t.name,
            "description": t.description,
            "input_schema": t.parameters,
        }
        for t in tools
    ]


def _messages_to_anthropic(
    msgs: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    """Convert normalised message dicts to Anthropic Messages API format.

    - Skips system messages (handled as top-level ``system`` field).
    - Converts tool results to ``role: user`` with ``tool_result`` content blocks.
    - Converts assistant tool_calls to ``tool_use`` content blocks.
    """
    out: list[dict[str, Any]] = []
    for m in msgs:
        role = m.get("role", "user")
        if role == "system":
            continue
        if role == "tool":
            out.append(
                {
                    "role": "user",
                    "content": [
                        {
                            "type": "tool_result",
                            "tool_use_id": m.get("tool_call_id", ""),
                            "content": m.get("content", ""),
                        }
                    ],
                }
            )
        elif role == "assistant" and m.get("tool_calls"):
            content: list[dict[str, Any]] = []
            if m.get("content"):
                content.append({"type": "text", "text": m["content"]})
            for tc in m["tool_calls"]:
                content.append(
                    {
                        "type": "tool_use",
                        "id": tc.get("id", ""),
                        "name": tc.get("name", ""),
                        "input": tc.get("args", {}),
                    }
                )
            out.append({"role": "assistant", "content": content})
        else:
            out.append({"role": role, "content": m.get("content", "")})
    return out


@dataclass
class AnthropicModel(Model):
    """Anthropic Messages API (/v1/messages)."""

    api_key: str = ""
    base_url: str = "https://api.anthropic.com/v1"
    max_tokens_default: int = 4096
    timeout: int = 300

    def _headers(self) -> dict[str, str]:
        key = self.api_key or os.environ.get("ANTHROPIC_API_KEY", "")
        return {
            "Content-Type": "application/json",
            "x-api-key": key,
            "anthropic-version": "2023-06-01",
        }

    def _build_body(
        self,
        msgs: list[dict[str, Any]],
        system_prompt: str,
        tools: Optional[list[ToolSchema]],
        max_tokens: int,
        temperature: Optional[float],
        stream: bool,
    ) -> dict[str, Any]:
        body: dict[str, Any] = {
            "model": self.model,
            "messages": _messages_to_anthropic(msgs),
            "max_tokens": max_tokens if max_tokens > 0 else self.max_tokens_default,
        }
        if system_prompt:
            body["system"] = system_prompt
        if tools:
            body["tools"] = _tools_to_anthropic(tools)
        if temperature is not None:
            body["temperature"] = temperature
        if stream:
            body["stream"] = True
        return body

    def invoke(
        self,
        messages: MessageInput,
        *,
        system_prompt: str = "",
        tools: Optional[list[ToolSchema]] = None,
        max_tokens: int = 0,
        temperature: Optional[float] = None,
    ) -> ModelResponse:
        msgs = _normalize_messages(messages)
        body = self._build_body(
            msgs, system_prompt, tools, max_tokens, temperature, stream=False
        )
        url = f"{self.base_url.rstrip('/')}/messages"
        resp = requests.post(
            url,
            json=body,
            headers=self._headers(),
            timeout=self.timeout,
        )
        if resp.status_code != 200:
            raise ModelError(resp.status_code, resp.text)
        data = resp.json()
        content_parts: list[str] = []
        tool_calls: list[ToolCallResult] = []
        for block in data.get("content", []):
            if block["type"] == "text":
                content_parts.append(block["text"])
            elif block["type"] == "tool_use":
                tool_calls.append(
                    ToolCallResult(
                        id=block["id"],
                        name=block["name"],
                        args=block.get("input", {}),
                    )
                )
        return ModelResponse(
            content="".join(content_parts), tool_calls=tool_calls
        )

    def stream(
        self,
        messages: MessageInput,
        *,
        system_prompt: str = "",
        tools: Optional[list[ToolSchema]] = None,
        max_tokens: int = 0,
        temperature: Optional[float] = None,
    ) -> Iterator[StreamChunk]:
        msgs = _normalize_messages(messages)
        body = self._build_body(
            msgs, system_prompt, tools, max_tokens, temperature, stream=True
        )
        url = f"{self.base_url.rstrip('/')}/messages"
        resp = requests.post(
            url,
            json=body,
            headers=self._headers(),
            stream=True,
            timeout=self.timeout,
        )
        if resp.status_code != 200:
            raise ModelError(resp.status_code, resp.text)

        # Streaming state machine for Anthropic SSE events.
        current_tool: Optional[dict[str, Any]] = None

        for raw_line in resp.iter_lines(decode_unicode=True):
            if not raw_line or not raw_line.startswith("data:"):
                continue
            payload = raw_line[5:].strip()
            try:
                event = json.loads(payload)
            except json.JSONDecodeError:
                continue
            etype = event.get("type", "")

            if etype == "content_block_start":
                block = event.get("content_block", {})
                if block.get("type") == "tool_use":
                    current_tool = {
                        "id": block.get("id", ""),
                        "name": block.get("name", ""),
                        "input_json": "",
                    }

            elif etype == "content_block_delta":
                delta = event.get("delta", {})
                if delta.get("type") == "text_delta":
                    text = delta.get("text", "")
                    if text:
                        yield StreamChunk(delta=text)
                elif delta.get("type") == "input_json_delta":
                    if current_tool is not None:
                        current_tool["input_json"] += delta.get(
                            "partial_json", ""
                        )

            elif etype == "content_block_stop":
                if current_tool is not None:
                    try:
                        args = json.loads(current_tool["input_json"])
                    except json.JSONDecodeError:
                        args = (
                            {"_raw": current_tool["input_json"]}
                            if current_tool["input_json"]
                            else {}
                        )
                    yield StreamChunk(
                        tool_call=ToolCallResult(
                            id=current_tool["id"],
                            name=current_tool["name"],
                            args=args,
                        )
                    )
                    current_tool = None

            elif etype == "message_stop":
                break

        yield StreamChunk(done=True)


# ---------------------------------------------------------------------------
# Factory
# ---------------------------------------------------------------------------


def resolve(spec: str, **kwargs: Any) -> Model:
    """Parse ``'provider:model'`` string into a concrete Model instance.

    Supported providers: ``openai``, ``anthropic``, ``ollama``.
    API keys are read from *kwargs* first, then environment variables.

    Examples::

        resolve("openai:gpt-4o")
        resolve("anthropic:claude-sonnet-4-20250514")
        resolve("ollama:llama3.1:8b")
    """
    parts = spec.split(":", 1)
    if len(parts) < 2:
        raise ValueError(
            f"invalid model spec {spec!r}: expected 'provider:model' "
            f"(e.g. 'openai:gpt-4o', 'ollama:llama3.1:8b')"
        )
    provider = parts[0].lower()
    model_name = parts[1]

    if provider == "openai":
        return OpenAIModel(
            model=model_name,
            api_key=kwargs.get("api_key", "")
            or os.environ.get("OPENAI_API_KEY", ""),
            base_url=kwargs.get("base_url", "https://api.openai.com/v1"),
            timeout=kwargs.get("timeout", 300),
        )
    elif provider == "anthropic":
        return AnthropicModel(
            model=model_name,
            api_key=kwargs.get("api_key", "")
            or os.environ.get("ANTHROPIC_API_KEY", ""),
            base_url=kwargs.get(
                "base_url", "https://api.anthropic.com/v1"
            ),
            timeout=kwargs.get("timeout", 300),
        )
    elif provider == "ollama":
        base_url = kwargs.get("base_url") or os.environ.get(
            "OLLAMA_BASE_URL", "http://localhost:11434"
        )
        return OpenAIModel(
            model=model_name,
            base_url=f"{base_url.rstrip('/')}/v1",
            api_key="ollama",
            timeout=kwargs.get("timeout", 300),
        )
    else:
        raise ValueError(
            f"unknown provider {provider!r}: expected 'openai', 'anthropic', or 'ollama'"
        )
