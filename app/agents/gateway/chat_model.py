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
        if self.temperature is not None:
            body["temperature"] = self.temperature

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

        # 2. URL — Anthropic Messages API format
        url = self.gateway_url.rstrip("/")
        if not url.endswith("/v1/messages"):
            url = f"{url}/v1/messages"

        # 3. Headers — match Anthropic API format
        headers: dict[str, str] = {
            "content-type": "application/json",
            "anthropic-version": "2023-06-01",
        }
        if token:
            headers["x-api-key"] = token

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

        url = self.gateway_url.rstrip("/")
        if not url.endswith("/v1/messages"):
            url = f"{url}/v1/messages"

        headers: dict[str, str] = {
            "content-type": "application/json",
            "anthropic-version": "2023-06-01",
        }
        if token:
            headers["x-api-key"] = token

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

        Handles (matching claude_client.py):
            1. Gateway wrapped: {"result": [{"content": [{"type":"text","text":"..."}]}]}
            2. Anthropic native: {"content": [{"type": "text", "text": "..."}]}
            3. OpenAI compat:    {"choices": [{"message": {"content": "..."}}]}
        """
        content = ""

        # 1. Gateway wrapped format: {"status":"success", "result": [{...}]}
        if "result" in raw and isinstance(raw["result"], list) and len(raw["result"]) > 0:
            logger.debug("Detected gateway wrapped response format")
            content_blocks = raw["result"]
            # Could be direct content blocks or nested response
            if isinstance(content_blocks[0], dict) and content_blocks[0].get("type") == "text":
                content = content_blocks[0].get("text", "")
            elif isinstance(content_blocks[0], dict) and "content" in content_blocks[0]:
                # {"result": [{"content": [{"type":"text","text":"..."}]}]}
                inner_blocks = content_blocks[0].get("content", [])
                for block in inner_blocks:
                    if block.get("type") == "text":
                        content = block.get("text", "")
                        break

        # 2. Standard Anthropic format: {"content": [{"type":"text","text":"..."}]}
        elif "content" in raw and isinstance(raw["content"], list):
            for block in raw["content"]:
                if block.get("type") == "text":
                    content = block.get("text", "")
                    break

        # 3. OpenAI-compatible fallback
        elif "choices" in raw and isinstance(raw["choices"], list):
            msg = raw["choices"][0].get("message", {})
            content = msg.get("content", "") or ""

        if not content:
            logger.warning("Could not extract content from response. Keys: %s", list(raw.keys()))

        message = AIMessage(content=content)
        generation = ChatGeneration(message=message)
        return ChatResult(generations=[generation])

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
        body = self._build_request_body(messages, stream=False)
        if stop:
            body["stop"] = stop
        url, headers, body = await self._gateway_request_async(body)

        resp = await self._async_client.post(url, json=body, headers=headers)

        if resp.status_code != 200:
            logger.error(
                "GATEWAY ERROR  status=%d  url=%s  response=%s",
                resp.status_code, url, resp.text[:1000],
            )
            resp.raise_for_status()

        raw = resp.json()
        logger.debug("GATEWAY RESPONSE: %s", json.dumps(raw, default=str)[:1000])
        return self._parse_gateway_response(raw)

    # ── Sync streaming ──────────────────────────────────────────────────

    def _stream(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: CallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> Iterator[ChatGenerationChunk]:
        body = self._build_request_body(messages, stream=True)
        if stop:
            body["stop"] = stop
        url, headers, body = self._gateway_request(body)

        with self._sync_client.stream("POST", url, json=body, headers=headers) as resp:
            resp.raise_for_status()
            for line in resp.iter_lines():
                chunk = self._parse_sse_line(line)
                if chunk is None:
                    continue
                if run_manager:
                    run_manager.on_llm_new_token(chunk.text, chunk=chunk)
                yield chunk

    # ── Async streaming ─────────────────────────────────────────────────

    async def _astream(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: AsyncCallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> AsyncIterator[ChatGenerationChunk]:
        body = self._build_request_body(messages, stream=True)
        if stop:
            body["stop"] = stop
        url, headers, body = await self._gateway_request_async(body)

        async with self._async_client.stream("POST", url, json=body, headers=headers) as resp:
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
        return "user"
    if isinstance(msg, AIMessage):
        return "assistant"
    if isinstance(msg, ToolMessage):
        return "tool"
    return msg.type
