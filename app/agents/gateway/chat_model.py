"""LangChain BaseChatModel for gateway endpoints with OAuth2 auth.

This model handles:
    - OAuth2 token management (automatic refresh)
    - Custom response parsing via pluggable GatewayResponseParser
    - Sync and async generation / streaming
    - Tool binding compatible with LangGraph agents
"""

from __future__ import annotations

import json
import logging
from typing import Any, Iterator, AsyncIterator, Optional

import httpx
from langchain_core.callbacks import (
    AsyncCallbackManagerForLLMRun,
    CallbackManagerForLLMRun,
)
from langchain_core.language_models import BaseChatModel
from langchain_core.messages import BaseMessage, AIMessage, AIMessageChunk, HumanMessage, SystemMessage, ToolMessage
from langchain_core.outputs import ChatGeneration, ChatGenerationChunk, ChatResult
from pydantic import Field, PrivateAttr

from app.agents.gateway.response_parser import (
    DefaultGatewayResponseParser,
    GatewayResponseParser,
)
from app.agents.gateway.token_manager import GatewayTokenManager

logger = logging.getLogger(__name__)


class GatewayChatModel(BaseChatModel):
    """LangChain chat model for gateway endpoints with OAuth2 + custom parsing.

    Use this instead of ChatOpenAI when your gateway requires OAuth2
    client_credentials authentication or returns non-OpenAI response formats.
    """

    # ── Public config fields ────────────────────────────────────────────
    model_name: str = Field(default="", description="Model identifier sent to the gateway")
    gateway_url: str = Field(default="", description="Base URL of the gateway endpoint")
    temperature: Optional[float] = Field(default=None)
    max_tokens: Optional[int] = Field(default=None)
    static_token: Optional[str] = Field(
        default=None,
        description="Pre-fetched Bearer token. Bypasses OAuth2 token_manager when set.",
    )

    # ── Private attributes ──────────────────────────────────────────────
    _token_manager: GatewayTokenManager | None = PrivateAttr(default=None)
    _response_parser: GatewayResponseParser = PrivateAttr(
        default_factory=DefaultGatewayResponseParser,
    )
    _bound_tools: list[dict[str, Any]] = PrivateAttr(default_factory=list)
    _tool_choice: Any = PrivateAttr(default=None)
    _sync_client: httpx.Client = PrivateAttr(default=None)  # type: ignore[assignment]
    _async_client: httpx.AsyncClient = PrivateAttr(default=None)  # type: ignore[assignment]

    def __init__(
        self,
        *,
        token_manager: GatewayTokenManager | None = None,
        response_parser: GatewayResponseParser | None = None,
        **kwargs: Any,
    ) -> None:
        super().__init__(**kwargs)
        self._token_manager = token_manager
        self._response_parser = response_parser or DefaultGatewayResponseParser()
        self._bound_tools = []
        self._tool_choice = None
        self._sync_client = httpx.Client(timeout=120.0)
        self._async_client = httpx.AsyncClient(timeout=120.0)

    # ── LangChain required properties ───────────────────────────────────

    @property
    def _llm_type(self) -> str:
        return "gateway-chat-model"

    @property
    def _identifying_params(self) -> dict[str, Any]:
        return {
            "model_name": self.model_name,
            "gateway_url": self.gateway_url,
        }

    @property
    def profile(self) -> dict[str, Any] | None:
        """Model profile for summarization middleware context window limits.

        Returns Anthropic Claude 3.5 Sonnet defaults. Adjust max_input_tokens
        and max_output_tokens if your gateway model differs.
        """
        return {
            "max_input_tokens": 200000,
            "max_output_tokens": self.max_tokens or 8192,
            "supports_tool_use": True,
            "structured_output": True,
        }

    # ── Tool binding ────────────────────────────────────────────────────

    def bind_tools(
        self,
        tools: list[Any],
        *,
        tool_choice: Any | None = None,
        **kwargs: Any,
    ) -> GatewayChatModel:
        """Bind tools to the model, returning a new instance (LangChain convention).

        Converts tools to Anthropic format:
            {"name": "...", "description": "...", "input_schema": {...}}

        Supports tool_choice:
            - "any"          → force tool use (Anthropic: {"type": "any"})
            - "auto"         → model decides (Anthropic: {"type": "auto"})
            - "tool_name"    → force specific tool (Anthropic: {"type": "tool", "name": "..."})
            - dict           → passed through as-is
        """
        from langchain_core.utils.function_calling import convert_to_openai_tool

        anthropic_tools = []
        for i, t in enumerate(tools):
            # Log raw tool info before conversion
            raw_name = getattr(t, "name", None) or getattr(t, "__name__", None)
            logger.error(">>> TOOL[%d] raw: type=%s name=%s", i, type(t).__name__, raw_name)

            oai = convert_to_openai_tool(t)
            logger.error(">>> TOOL[%d] openai_converted: %s", i, json.dumps(oai, default=str)[:500])

            func = oai.get("function", oai)
            name = func.get("name", "") or getattr(t, "name", "") or getattr(t, "__name__", "")
            if not name:
                logger.error(">>> TOOL[%d] SKIPPED — no name found", i)
                continue
            anthropic_tools.append({
                "name": name,
                "description": func.get("description", "") or getattr(t, "description", ""),
                "input_schema": func.get("parameters", {"type": "object", "properties": {}}),
            })
        logger.error(">>> BOUND %d tools: %s", len(anthropic_tools), [t["name"] for t in anthropic_tools])

        # Normalize tool_choice to Anthropic format
        anthropic_tool_choice = None
        if tool_choice is not None:
            if isinstance(tool_choice, dict):
                anthropic_tool_choice = tool_choice
            elif tool_choice in ("any", "required"):
                anthropic_tool_choice = {"type": "any"}
            elif tool_choice == "auto":
                anthropic_tool_choice = {"type": "auto"}
            elif tool_choice == "none":
                anthropic_tool_choice = None
            elif isinstance(tool_choice, str):
                # Specific tool name
                anthropic_tool_choice = {"type": "tool", "name": tool_choice}

        # Apply model_settings kwargs (temperature, max_tokens overrides)
        updates: dict[str, Any] = {}
        if "temperature" in kwargs:
            updates["temperature"] = kwargs.pop("temperature")
        if "max_tokens" in kwargs:
            updates["max_tokens"] = kwargs.pop("max_tokens")

        new = self.model_copy(update=updates) if updates else self.model_copy()
        # PrivateAttr fields are not copied by model_copy — set them manually
        new._token_manager = self._token_manager
        new._response_parser = self._response_parser
        new._bound_tools = anthropic_tools
        new._tool_choice = anthropic_tool_choice
        new._sync_client = self._sync_client
        new._async_client = self._async_client
        return new

    # ── Request building ────────────────────────────────────────────────

    def _build_request_body(
        self,
        messages: list[BaseMessage],
        stream: bool = False,
    ) -> dict[str, Any]:
        """Build the JSON payload in Anthropic Messages API format.

        Matches the working format from claude_client.py:
          {
            "model": "anthropic.claude-3-5-sonnet",
            "max_tokens": 4096,
            "messages": [{"role": "user", "content": "Hello"}],
            "system": "You are a helpful assistant."    ← top-level, not in messages
          }
        """
        msg_dicts = _langchain_messages_to_dicts(messages)

        # Anthropic: system prompt is a top-level field, not a message
        system_text = None
        non_system_msgs = []
        for m in msg_dicts:
            if m["role"] == "system":
                system_text = m["content"]
            else:
                non_system_msgs.append(m)

        body: dict[str, Any] = {
            "model": self.model_name,
            "max_tokens": self.max_tokens or 4096,
            "messages": non_system_msgs,
        }
        if system_text:
            body["system"] = system_text
        if stream:
            body["stream"] = stream
        if self._bound_tools:
            body["tools"] = self._bound_tools
        if self._tool_choice:
            body["tool_choice"] = self._tool_choice
        if self.temperature is not None:
            body["temperature"] = self.temperature

        logger.error(
            ">>> REQUEST BODY tools_count=%d  tool_names=%s  bound_tools_id=%s",
            len(body.get("tools", [])),
            [t.get("name", "???") for t in body.get("tools", [])],
            id(self._bound_tools),
        )

        return body

    def _gateway_request(self, body: dict[str, Any]) -> tuple[str, dict[str, str], dict[str, Any]]:
        """Build URL + headers + body for a gateway POST call.

        ── EDIT THIS METHOD to customize your gateway request. ──

        Called on EVERY request (sync and async). Returns:
            (url, headers, body)

        Token is fetched fresh each call. Modify _fetch_bearer_token()
        to plug in your own auth logic.
        """
        # 1. Token (refreshed every call)
        token = self._fetch_bearer_token()
        if not token and self.static_token:
            token = self.static_token
        if not token and self._token_manager:
            token = self._token_manager.get_token_sync()

        # 2. URL — use exactly as configured in agents.yaml
        url = self.gateway_url

        # 3. Headers — Bearer token for gateway proxy
        headers: dict[str, str] = {
            "content-type": "application/json",
            "anthropic-version": "2023-06-01",
        }
        if token:
            headers["Authorization"] = f"Bearer {token}"

        logger.info(
            "GATEWAY REQUEST  url=%s  headers=%s  body_keys=%s  model=%s  msg_count=%d",
            url,
            {k: (v[:20] + "...") if k == "Authorization" else v for k, v in headers.items()},
            list(body.keys()),
            body.get("model", "?"),
            len(body.get("messages", [])),
        )
        logger.debug("GATEWAY REQUEST BODY: %s", json.dumps(body, default=str)[:2000])

        return url, headers, body

    async def _gateway_request_async(self, body: dict[str, Any]) -> tuple[str, dict[str, str], dict[str, Any]]:
        """Async version of _gateway_request (for async token managers)."""
        token = self._fetch_bearer_token()
        if not token and self.static_token:
            token = self.static_token
        if not token and self._token_manager:
            token = await self._token_manager.get_token()

        url = self.gateway_url

        headers: dict[str, str] = {
            "content-type": "application/json",
            "anthropic-version": "2023-06-01",
        }
        if token:
            headers["Authorization"] = f"Bearer {token}"

        logger.info(
            "GATEWAY REQUEST (async)  url=%s  headers=%s  body_keys=%s  model=%s  msg_count=%d",
            url,
            {k: (v[:20] + "...") if k == "Authorization" else v for k, v in headers.items()},
            list(body.keys()),
            body.get("model", "?"),
            len(body.get("messages", [])),
        )
        logger.debug("GATEWAY REQUEST BODY: %s", json.dumps(body, default=str)[:2000])

        return url, headers, body

    def _fetch_bearer_token(self) -> str | None:
        """Your custom token logic — called on EVERY request.

        Return a bearer token string, or None to fall through
        to static_token / token_manager.

        Example:
            def _fetch_bearer_token(self) -> str | None:
                resp = httpx.post("https://your-auth-server/token", data={
                    "grant_type": "client_credentials",
                    "client_id": "my-id",
                    "client_secret": "my-secret",
                })
                return resp.json()["access_token"]
        """
        return None

    def _parse_gateway_response(self, raw: dict[str, Any]) -> ChatResult:
        """Parse the gateway's JSON response into a LangChain ChatResult.

        ── EDIT THIS METHOD to match your gateway's response format. ──

        Handles text AND tool_use blocks from Anthropic format:
            {"content": [
                {"type": "text", "text": "I'll analyze..."},
                {"type": "tool_use", "id": "toolu_123", "name": "read_file", "input": {"path": "x.csv"}}
            ]}

        Tool calls are parsed into LangChain's tool_calls format so
        LangGraph continues the agent loop (tool execution → next turn).
        """
        content_blocks = self._extract_content_blocks(raw)

        # Parse text and tool_use blocks
        text_parts: list[str] = []
        tool_calls: list[dict[str, Any]] = []

        for block in content_blocks:
            if block.get("type") == "text":
                text_parts.append(block.get("text", ""))
            elif block.get("type") == "tool_use":
                tool_calls.append({
                    "name": block.get("name", ""),
                    "args": block.get("input", {}),
                    "id": block.get("id", ""),
                })

        content = "\n".join(text_parts)

        if not content and not tool_calls:
            logger.warning("Could not extract content from response. Keys: %s", list(raw.keys()))

        if tool_calls:
            logger.info("Parsed %d tool call(s): %s", len(tool_calls), [tc["name"] for tc in tool_calls])

        message = AIMessage(content=content, tool_calls=tool_calls)
        generation = ChatGeneration(message=message)
        return ChatResult(generations=[generation])

    def _extract_content_blocks(self, raw: dict[str, Any]) -> list[dict[str, Any]]:
        """Extract the content blocks array from various response formats."""
        # 1. Gateway wrapped: {"result": [{"content": [...]}]}
        if "result" in raw and isinstance(raw["result"], list) and len(raw["result"]) > 0:
            logger.debug("Detected gateway wrapped response format")
            first = raw["result"][0]
            if isinstance(first, dict) and "content" in first:
                return first.get("content", [])
            # Direct content blocks in result
            if isinstance(first, dict) and first.get("type") in ("text", "tool_use"):
                return raw["result"]

        # 2. Standard Anthropic: {"content": [...]}
        if "content" in raw and isinstance(raw["content"], list):
            return raw["content"]

        # 3. OpenAI-compatible fallback
        if "choices" in raw and isinstance(raw["choices"], list):
            msg = raw["choices"][0].get("message", {})
            # Convert to block format for uniform handling
            blocks: list[dict[str, Any]] = []
            if msg.get("content"):
                blocks.append({"type": "text", "text": msg["content"]})
            for tc in (msg.get("tool_calls") or []):
                func = tc.get("function", {})
                blocks.append({
                    "type": "tool_use",
                    "id": tc.get("id", ""),
                    "name": func.get("name", ""),
                    "input": json.loads(func.get("arguments", "{}")),
                })
            return blocks

        return []

    # ── Sync generation ─────────────────────────────────────────────────

    def _generate(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: CallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> ChatResult:
        body = self._build_request_body(messages, stream=False)
        if stop:
            body["stop"] = stop
        url, headers, body = self._gateway_request(body)

        resp = self._sync_client.post(url, json=body, headers=headers)

        if resp.status_code != 200:
            logger.error(
                "GATEWAY ERROR  status=%d  url=%s  response=%s",
                resp.status_code, url, resp.text[:1000],
            )
            resp.raise_for_status()

        raw = resp.json()
        logger.debug("GATEWAY RESPONSE: %s", json.dumps(raw, default=str)[:1000])
        return self._parse_gateway_response(raw)

    # ── Async generation ────────────────────────────────────────────────

    async def _agenerate(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: AsyncCallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> ChatResult:
        logger.error(
            ">>> _agenerate CALLED with %d messages, kwargs=%s, _bound_tools=%d",
            len(messages), list(kwargs.keys()), len(self._bound_tools),
        )
        body = self._build_request_body(messages, stream=False)
        if stop:
            body["stop"] = stop
        url, headers, body = await self._gateway_request_async(body)

        logger.error(
            ">>> FULL REQUEST  url=%s\n>>> HEADERS=%s\n>>> BODY=%s",
            url, json.dumps(headers), json.dumps(body, default=str)[:3000],
        )

        resp = await self._async_client.post(url, json=body, headers=headers)

        logger.error(">>> RESPONSE status=%d  body=%s", resp.status_code, resp.text[:2000])

        if resp.status_code != 200:
            resp.raise_for_status()

        raw = resp.json()
        return self._parse_gateway_response(raw)

    # ── Sync streaming (non-streaming POST, yields result as single chunk) ──

    def _stream(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: CallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> Iterator[ChatGenerationChunk]:
        result = self._generate(messages, stop=stop, run_manager=run_manager, **kwargs)
        msg = result.generations[0].message
        # Forward tool_calls so LangGraph agent loop continues
        tool_call_chunks = [
            {
                "name": tc["name"],
                "args": json.dumps(tc["args"]) if isinstance(tc["args"], dict) else str(tc["args"]),
                "id": tc.get("id", ""),
                "index": i,
            }
            for i, tc in enumerate(getattr(msg, "tool_calls", []) or [])
        ]
        chunk = ChatGenerationChunk(
            message=AIMessageChunk(
                content=msg.content,
                tool_call_chunks=tool_call_chunks,
            ),
        )
        if run_manager:
            run_manager.on_llm_new_token(chunk.text, chunk=chunk)
        yield chunk

    # ── Async streaming (non-streaming POST, yields result as single chunk) ──

    async def _astream(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: AsyncCallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> AsyncIterator[ChatGenerationChunk]:
        result = await self._agenerate(messages, stop=stop, run_manager=run_manager, **kwargs)
        msg = result.generations[0].message
        # Forward tool_calls so LangGraph agent loop continues
        tool_call_chunks = [
            {
                "name": tc["name"],
                "args": json.dumps(tc["args"]) if isinstance(tc["args"], dict) else str(tc["args"]),
                "id": tc.get("id", ""),
                "index": i,
            }
            for i, tc in enumerate(getattr(msg, "tool_calls", []) or [])
        ]
        chunk = ChatGenerationChunk(
            message=AIMessageChunk(
                content=msg.content,
                tool_call_chunks=tool_call_chunks,
            ),
        )
        if run_manager:
            await run_manager.on_llm_new_token(chunk.text, chunk=chunk)
        yield chunk

    # ── SSE parsing helper ──────────────────────────────────────────────

    def _parse_sse_line(self, line: str) -> ChatGenerationChunk | None:
        """Parse a single SSE line into a ChatGenerationChunk or None."""
        line = line.strip()
        if not line:
            return None

        # Standard SSE: "data: {...}"
        if line.startswith("data:"):
            data_str = line[5:].strip()
            if data_str == "[DONE]":
                return None
            try:
                raw = json.loads(data_str)
            except (json.JSONDecodeError, TypeError):
                logger.debug("Skipping unparseable SSE data: %s", data_str[:100])
                return None
        else:
            # Try parsing the whole line as JSON (some gateways skip SSE prefix)
            try:
                raw = json.loads(line)
            except (json.JSONDecodeError, TypeError):
                return None

        return self._response_parser.parse_stream_chunk(raw)


# ═════════════════════════════════════════════════════════════════════════
# Message conversion
# ═════════════════════════════════════════════════════════════════════════


def _langchain_messages_to_dicts(
    messages: list[BaseMessage],
) -> list[dict[str, Any]]:
    """Convert LangChain BaseMessage objects to Anthropic Messages API format.

    Anthropic format differences from OpenAI:
        - AIMessage with tool_calls → content is a list of blocks:
            [{"type":"text","text":"..."}, {"type":"tool_use","id":"...","name":"...","input":{}}]
        - ToolMessage → role "user" with content block:
            {"role":"user","content":[{"type":"tool_result","tool_use_id":"...","content":"..."}]}
    """
    result: list[dict[str, Any]] = []
    for msg in messages:
        if isinstance(msg, SystemMessage):
            # System messages handled separately in _build_request_body
            result.append({"role": "system", "content": msg.content})

        elif isinstance(msg, ToolMessage):
            # Anthropic: tool results are sent as role "user" with tool_result block
            result.append({
                "role": "user",
                "content": [
                    {
                        "type": "tool_result",
                        "tool_use_id": msg.tool_call_id,
                        "content": msg.content if isinstance(msg.content, str) else json.dumps(msg.content),
                    }
                ],
            })

        elif isinstance(msg, AIMessage) and msg.tool_calls:
            # Anthropic: assistant message with tool_use blocks
            content_blocks: list[dict[str, Any]] = []
            if msg.content:
                content_blocks.append({"type": "text", "text": msg.content})
            for tc in msg.tool_calls:
                content_blocks.append({
                    "type": "tool_use",
                    "id": tc.get("id", ""),
                    "name": tc["name"],
                    "input": tc["args"] if isinstance(tc["args"], dict) else {},
                })
            result.append({"role": "assistant", "content": content_blocks})

        elif isinstance(msg, AIMessage):
            result.append({"role": "assistant", "content": msg.content})

        elif isinstance(msg, HumanMessage):
            result.append({"role": "user", "content": msg.content})

        else:
            result.append({"role": msg.type, "content": msg.content})

    return result
    return msg.type
