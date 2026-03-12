"""FastAPI sidecar serving tool callbacks and LLM proxy endpoints.

Go's HTTPTool calls:       POST /tools/{tool_name}
Go's HTTPProxyClient calls: POST /llm/{model_name}/call
                            POST /llm/{model_name}/stream

This module builds a FastAPI app that routes these to Python functions
registered by the user via @agent.tool and @agent.llm_provider decorators.
"""

from __future__ import annotations

import asyncio
import inspect
import json
import logging
import traceback
from collections.abc import AsyncIterator, Callable
from typing import Any

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse, StreamingResponse

from ._types import (
    LLMRequest,
    LLMResponse,
    StreamChunk,
    ToolCallbackRequest,
    ToolCallbackResponse,
    ToolCallResult,
)

logger = logging.getLogger("wick.sidecar")


def build_app(
    tools: dict[str, Callable],
    llm_providers: dict[str, Callable],
) -> FastAPI:
    """Build the FastAPI sidecar application.

    Args:
        tools: mapping of tool name to Python callable.
        llm_providers: mapping of model name to LLM handler.
            - Sync handler: (LLMRequest) -> LLMResponse
            - Async generator: (LLMRequest) -> AsyncIterator[StreamChunk]
    """
    app = FastAPI(title="wick-sidecar", docs_url=None, redoc_url=None)

    # ── Tool endpoint ───────────────────────────────────────────────────
    # Contract: agent/http_tool.go HTTPTool.Execute
    #   POST {callbackURL}/tools/{toolName}
    #   Body: {"name": str, "args": dict}
    #   Response: {"result": str} or {"error": str}

    @app.post("/tools/{tool_name}")
    async def handle_tool(tool_name: str, request: ToolCallbackRequest) -> ToolCallbackResponse:
        fn = tools.get(tool_name)
        if fn is None:
            return ToolCallbackResponse(error=f"unknown tool: {tool_name}")

        try:
            if inspect.iscoroutinefunction(fn):
                result = await fn(**request.args)
            else:
                result = await asyncio.to_thread(fn, **request.args)
            return ToolCallbackResponse(result=str(result))
        except Exception as e:
            logger.error("tool %s failed: %s\n%s", tool_name, e, traceback.format_exc())
            return ToolCallbackResponse(error=str(e))

    # ── LLM sync endpoint ──────────────────────────────────────────────
    # Contract: llm/http_proxy.go HTTPProxyClient.Call
    #   POST {callbackURL}/llm/{modelName}/call
    #   Body: llm.Request JSON
    #   Response: llm.Response JSON {"content": str, "tool_calls": [...]}

    @app.post("/llm/{model_name}/call")
    async def handle_llm_call(model_name: str, request: Request) -> JSONResponse:
        body = await request.json()
        llm_request = LLMRequest(**body)

        provider = llm_providers.get(model_name)
        if provider is None:
            return JSONResponse(
                status_code=400,
                content={"error": f"unknown LLM provider: {model_name}"},
            )

        try:
            result = provider(llm_request)

            # If the provider is an async generator, collect all chunks
            if inspect.isasyncgen(result):
                content = ""
                tool_calls: list[ToolCallResult] = []
                async for chunk in result:
                    if chunk.delta:
                        content += chunk.delta
                    if chunk.tool_call:
                        tool_calls.append(chunk.tool_call)
                resp = LLMResponse(
                    content=content,
                    tool_calls=tool_calls if tool_calls else None,
                )
                return JSONResponse(content=resp.model_dump())

            # If it's a coroutine, await it
            if inspect.isawaitable(result):
                result = await result

            # If it returns LLMResponse directly
            if isinstance(result, LLMResponse):
                return JSONResponse(content=result.model_dump())

            return JSONResponse(content=result)
        except Exception as e:
            logger.error("LLM call %s failed: %s\n%s", model_name, e, traceback.format_exc())
            return JSONResponse(status_code=500, content={"error": str(e)})

    # ── LLM stream endpoint ────────────────────────────────────────────
    # Contract: llm/http_proxy.go HTTPProxyClient.Stream
    #   POST {callbackURL}/llm/{modelName}/stream
    #   Body: llm.Request JSON
    #   Response: SSE stream
    #     data: {"delta": "..."}\n\n
    #     data: {"tool_call": {"id": ..., "name": ..., "args": ...}}\n\n
    #     data: {"done": true}\n\n
    #
    # Go parses with bufio.Scanner looking for "data: " prefix lines.

    @app.post("/llm/{model_name}/stream")
    async def handle_llm_stream(model_name: str, request: Request) -> StreamingResponse:
        body = await request.json()
        llm_request = LLMRequest(**body)

        provider = llm_providers.get(model_name)
        if provider is None:
            return JSONResponse(
                status_code=400,
                content={"error": f"unknown LLM provider: {model_name}"},
            )

        async def generate() -> AsyncIterator[str]:
            try:
                result = provider(llm_request)

                # Async generator — stream chunks
                if inspect.isasyncgen(result):
                    async for chunk in result:
                        yield f"data: {chunk.model_dump_json()}\n\n"
                    # Ensure done is sent
                    yield f"data: {json.dumps({'done': True})}\n\n"
                    return

                # Coroutine returning LLMResponse — wrap as single stream
                if inspect.isawaitable(result):
                    result = await result

                if isinstance(result, LLMResponse):
                    if result.content:
                        yield f"data: {StreamChunk(delta=result.content).model_dump_json()}\n\n"
                    if result.tool_calls:
                        for tc in result.tool_calls:
                            yield f"data: {StreamChunk(tool_call=tc).model_dump_json()}\n\n"
                    yield f"data: {json.dumps({'done': True})}\n\n"

            except Exception as e:
                logger.error("LLM stream %s failed: %s\n%s", model_name, e, traceback.format_exc())
                yield f"data: {json.dumps({'error': str(e)})}\n\n"

        return StreamingResponse(generate(), media_type="text/event-stream")

    # ── Health ──────────────────────────────────────────────────────────

    @app.get("/health")
    async def health() -> dict[str, Any]:
        return {
            "status": "ok",
            "tools": list(tools.keys()),
            "llm_providers": list(llm_providers.keys()),
        }

    return app
