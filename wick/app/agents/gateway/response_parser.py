"""Pluggable response parser for gateway LLM endpoints.

Gateways often return non-OpenAI response formats (extra envelopes,
different field names, custom tool call structures). This module provides
an ABC and a flexible default implementation that handles common patterns.

To customize: subclass GatewayResponseParser and override the methods,
then reference your class in agents.yaml via response_parser config.
"""

from __future__ import annotations

import json
import logging
import uuid
from abc import ABC, abstractmethod
from typing import Any

from langchain_core.messages import AIMessage, AIMessageChunk
from langchain_core.outputs import ChatGeneration, ChatGenerationChunk, ChatResult

logger = logging.getLogger(__name__)

# Common field names gateways use for content
_CONTENT_FIELDS = ("output", "content", "response", "text", "message")
# Common field names for tool call arguments
_TOOL_ARGS_FIELDS = ("arguments", "input", "args", "parameters")


class GatewayResponseParser(ABC):
    """Abstract base class for gateway response parsing.

    Subclass this and override the three methods to handle your
    gateway's specific request/response format.
    """

    @abstractmethod
    def parse_response(self, raw: dict[str, Any]) -> ChatResult:
        """Parse a full (non-streaming) gateway response into a ChatResult."""

    @abstractmethod
    def parse_stream_chunk(self, raw: dict[str, Any]) -> ChatGenerationChunk | None:
        """Parse a single streaming chunk. Return None to skip the chunk."""

    @abstractmethod
    def format_messages(
        self,
        messages: list[dict[str, Any]],
        *,
        model: str,
        tools: list[dict[str, Any]] | None = None,
        temperature: float | None = None,
        max_tokens: int | None = None,
        stream: bool = False,
        **kwargs: Any,
    ) -> dict[str, Any]:
        """Format messages and tools into the gateway's expected request body.

        This hook controls both request format and can add any
        gateway-specific fields.
        """


class DefaultGatewayResponseParser(GatewayResponseParser):
    """Flexible parser that handles common gateway response patterns.

    Handles:
        - Direct content: {"content": "..."}
        - Envelope wrapping: {"data": {"output": "..."}}
        - OpenAI-like: {"choices": [{"message": {"content": "..."}}]}
        - Tool calls with various field names
        - Streaming with content_delta, tool_call_delta, done chunks
    """

    def parse_response(self, raw: dict[str, Any]) -> ChatResult:
        """Parse a full gateway response into a ChatResult."""
        content = ""
        tool_calls: list[dict[str, Any]] = []

        # Try OpenAI-compatible format first
        choices = raw.get("choices")
        if choices and isinstance(choices, list):
            msg = choices[0].get("message", {})
            content = msg.get("content", "") or ""
            raw_tool_calls = msg.get("tool_calls")
            if raw_tool_calls:
                tool_calls = self._parse_tool_calls(raw_tool_calls)
        else:
            # Unwrap envelope if present
            inner = self._unwrap_envelope(raw)
            content = self._extract_content(inner)
            raw_tool_calls = inner.get("tool_calls")
            if raw_tool_calls:
                tool_calls = self._parse_tool_calls(raw_tool_calls)

        message = AIMessage(content=content, tool_calls=tool_calls)

        # Extract usage metadata if available
        usage = raw.get("usage", {})
        generation_info: dict[str, Any] = {}
        if usage:
            generation_info["token_usage"] = usage

        generation = ChatGeneration(message=message, generation_info=generation_info)

        return ChatResult(
            generations=[generation],
            llm_output={"model_name": raw.get("model", "")},
        )

    def parse_stream_chunk(self, raw: dict[str, Any]) -> ChatGenerationChunk | None:
        """Parse a single streaming chunk from the gateway."""
        # Handle SSE "done" signal
        if raw.get("done") or raw.get("type") == "done":
            return None

        content = ""
        tool_call_chunks: list[dict[str, Any]] = []

        # OpenAI-compatible streaming
        choices = raw.get("choices")
        if choices and isinstance(choices, list):
            delta = choices[0].get("delta", {})
            content = delta.get("content", "") or ""

            raw_tc = delta.get("tool_calls")
            if raw_tc:
                for tc in raw_tc:
                    tool_call_chunks.append({
                        "name": tc.get("function", {}).get("name", ""),
                        "args": tc.get("function", {}).get("arguments", ""),
                        "id": tc.get("id", ""),
                        "index": tc.get("index", 0),
                    })

            finish_reason = choices[0].get("finish_reason")
            if finish_reason == "stop" and not content and not tool_call_chunks:
                return None
        else:
            # Custom gateway streaming format
            chunk_type = raw.get("type", "")

            if chunk_type == "content_delta":
                content = raw.get("delta", raw.get("text", "")) or ""
            elif chunk_type == "tool_call_delta":
                tool_call_chunks.append({
                    "name": raw.get("name", ""),
                    "args": raw.get("arguments", raw.get("args", "")),
                    "id": raw.get("id", ""),
                    "index": raw.get("index", 0),
                })
            else:
                # Try to extract content from generic chunk
                content = self._extract_content(raw)
                if not content:
                    return None

        message = AIMessageChunk(
            content=content,
            tool_call_chunks=tool_call_chunks if tool_call_chunks else [],
        )
        return ChatGenerationChunk(message=message)

    def format_messages(
        self,
        messages: list[dict[str, Any]],
        *,
        model: str,
        tools: list[dict[str, Any]] | None = None,
        temperature: float | None = None,
        max_tokens: int | None = None,
        stream: bool = False,
        **kwargs: Any,
    ) -> dict[str, Any]:
        """Format as OpenAI-compatible request body (most gateways expect this)."""
        body: dict[str, Any] = {
            "model": model,
            "messages": messages,
            "stream": stream,
        }
        if tools:
            body["tools"] = tools
        if temperature is not None:
            body["temperature"] = temperature
        if max_tokens is not None:
            body["max_tokens"] = max_tokens
        return body

    # ── Helpers ──────────────────────────────────────────────────────────

    def _unwrap_envelope(self, raw: dict[str, Any]) -> dict[str, Any]:
        """Unwrap common envelope patterns like {"data": {...}}."""
        # {"data": {"output": "..."}} → inner dict
        if "data" in raw and isinstance(raw["data"], dict):
            return raw["data"]
        # {"result": {"content": "..."}} → inner dict
        if "result" in raw and isinstance(raw["result"], dict):
            return raw["result"]
        return raw

    def _extract_content(self, data: dict[str, Any]) -> str:
        """Extract text content trying multiple field names."""
        for field in _CONTENT_FIELDS:
            val = data.get(field)
            if val and isinstance(val, str):
                return val
        return ""

    def _parse_tool_calls(
        self, raw_calls: list[dict[str, Any]],
    ) -> list[dict[str, Any]]:
        """Parse tool calls with flexible field names."""
        result: list[dict[str, Any]] = []
        for tc in raw_calls:
            # OpenAI format: {"function": {"name": ..., "arguments": ...}}
            func = tc.get("function", {})
            name = func.get("name") or tc.get("name", "")
            call_id = tc.get("id") or str(uuid.uuid4())

            # Try multiple field names for arguments
            args_raw = func.get("arguments") if func else None
            if args_raw is None:
                for field in _TOOL_ARGS_FIELDS:
                    args_raw = tc.get(field)
                    if args_raw is not None:
                        break

            # Parse arguments to dict
            if isinstance(args_raw, str):
                try:
                    args = json.loads(args_raw)
                except (json.JSONDecodeError, TypeError):
                    args = {"raw": args_raw}
            elif isinstance(args_raw, dict):
                args = args_raw
            else:
                args = {}

            result.append({"name": name, "args": args, "id": call_id})
        return result
