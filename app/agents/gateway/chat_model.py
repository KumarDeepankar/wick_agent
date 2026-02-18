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
from langchain_core.messages import BaseMessage, AIMessage, HumanMessage, SystemMessage, ToolMessage
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

    # ── Tool binding ────────────────────────────────────────────────────

    def bind_tools(
        self,
        tools: list[Any],
        **kwargs: Any,
    ) -> GatewayChatModel:
        """Bind tools to the model, returning a new instance (LangChain convention).

        Converts tools via LangChain's convert_to_openai_tool utility.
        """
        from langchain_core.utils.function_calling import convert_to_openai_tool

        openai_tools = [convert_to_openai_tool(t) for t in tools]

        # Create a copy with tools bound
        new = self.model_copy()
        # PrivateAttr fields are not copied by model_copy — set them manually
        new._token_manager = self._token_manager
        new._response_parser = self._response_parser
        new._bound_tools = openai_tools
        new._sync_client = self._sync_client
        new._async_client = self._async_client
        return new

    # ── Request building ────────────────────────────────────────────────

    def _build_request_body(
        self,
        messages: list[BaseMessage],
        stream: bool = False,
    ) -> dict[str, Any]:
        """Convert LangChain messages to request body via the response parser."""
        msg_dicts = _langchain_messages_to_dicts(messages)
        return self._response_parser.format_messages(
            msg_dicts,
            model=self.model_name,
            tools=self._bound_tools or None,
            temperature=self.temperature,
            max_tokens=self.max_tokens,
            stream=stream,
        )

    def _build_headers(self, token: str | None) -> dict[str, str]:
        """Build HTTP headers, including auth token if available."""
        headers: dict[str, str] = {"Content-Type": "application/json"}
        if token:
            headers["Authorization"] = f"Bearer {token}"
        return headers

    def _resolve_token_sync(self) -> str | None:
        """Resolve token: static_token → token_manager → None."""
        if self.static_token:
            return self.static_token
        if self._token_manager:
            return self._token_manager.get_token_sync()
        return None

    async def _resolve_token_async(self) -> str | None:
        """Resolve token: static_token → token_manager → None."""
        if self.static_token:
            return self.static_token
        if self._token_manager:
            return await self._token_manager.get_token()
        return None

    @property
    def _chat_endpoint(self) -> str:
        """Full URL to the chat completions endpoint."""
        url = self.gateway_url.rstrip("/")
        if not url.endswith("/chat/completions"):
            url = f"{url}/chat/completions"
        return url

    # ── Sync generation ─────────────────────────────────────────────────

    def _generate(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: CallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> ChatResult:
        """Synchronous invoke — gets token, POSTs to gateway, parses response."""
        token = self._resolve_token_sync()
        body = self._build_request_body(messages, stream=False)
        if stop:
            body["stop"] = stop
        headers = self._build_headers(token)

        logger.debug("Gateway POST %s body_keys=%s", self._chat_endpoint, list(body.keys()))

        resp = self._sync_client.post(
            self._chat_endpoint,
            json=body,
            headers=headers,
        )
        resp.raise_for_status()
        raw = resp.json()

        return self._response_parser.parse_response(raw)

    # ── Async generation ────────────────────────────────────────────────

    async def _agenerate(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: AsyncCallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> ChatResult:
        """Async invoke — gets token, POSTs to gateway, parses response."""
        token = await self._resolve_token_async()
        body = self._build_request_body(messages, stream=False)
        if stop:
            body["stop"] = stop
        headers = self._build_headers(token)

        logger.debug("Gateway async POST %s", self._chat_endpoint)

        resp = await self._async_client.post(
            self._chat_endpoint,
            json=body,
            headers=headers,
        )
        resp.raise_for_status()
        raw = resp.json()

        return self._response_parser.parse_response(raw)

    # ── Sync streaming ──────────────────────────────────────────────────

    def _stream(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: CallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> Iterator[ChatGenerationChunk]:
        """Sync streaming — SSE line parsing, delegates to parse_stream_chunk."""
        token = self._resolve_token_sync()
        body = self._build_request_body(messages, stream=True)
        if stop:
            body["stop"] = stop
        headers = self._build_headers(token)

        with self._sync_client.stream(
            "POST",
            self._chat_endpoint,
            json=body,
            headers=headers,
        ) as resp:
            resp.raise_for_status()
            for line in resp.iter_lines():
                chunk = self._parse_sse_line(line)
                if chunk is None:
                    continue
                if run_manager:
                    run_manager.on_llm_new_token(
                        chunk.text, chunk=chunk,
                    )
                yield chunk

    # ── Async streaming ─────────────────────────────────────────────────

    async def _astream(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: AsyncCallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> AsyncIterator[ChatGenerationChunk]:
        """Async streaming — powers astream_events(v2)."""
        token = await self._resolve_token_async()
        body = self._build_request_body(messages, stream=True)
        if stop:
            body["stop"] = stop
        headers = self._build_headers(token)

        async with self._async_client.stream(
            "POST",
            self._chat_endpoint,
            json=body,
            headers=headers,
        ) as resp:
            resp.raise_for_status()
            async for line in resp.aiter_lines():
                chunk = self._parse_sse_line(line)
                if chunk is None:
                    continue
                if run_manager:
                    await run_manager.on_llm_new_token(
                        chunk.text, chunk=chunk,
                    )
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
    """Convert LangChain BaseMessage objects to simple dicts.

    Handles: SystemMessage, HumanMessage, AIMessage (with tool_calls),
    ToolMessage.
    """
    result: list[dict[str, Any]] = []
    for msg in messages:
        d: dict[str, Any] = {"role": _role_for_message(msg)}

        if isinstance(msg, ToolMessage):
            d["content"] = msg.content
            d["tool_call_id"] = msg.tool_call_id
        elif isinstance(msg, AIMessage) and msg.tool_calls:
            d["content"] = msg.content or ""
            d["tool_calls"] = [
                {
                    "id": tc.get("id", ""),
                    "type": "function",
                    "function": {
                        "name": tc["name"],
                        "arguments": (
                            json.dumps(tc["args"])
                            if isinstance(tc["args"], dict)
                            else str(tc["args"])
                        ),
                    },
                }
                for tc in msg.tool_calls
            ]
        else:
            d["content"] = msg.content

        result.append(d)
    return result


def _role_for_message(msg: BaseMessage) -> str:
    """Map LangChain message type to role string."""
    if isinstance(msg, SystemMessage):
        return "system"
    if isinstance(msg, HumanMessage):
        return "human"
    if isinstance(msg, AIMessage):
        return "assistant"
    if isinstance(msg, ToolMessage):
        return "tool"
    return msg.type
