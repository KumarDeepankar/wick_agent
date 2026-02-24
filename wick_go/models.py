"""Custom model definitions for the wick_go app.

Standard providers (Ollama, OpenAI, Anthropic) use string/dict specs — the Go
resolver handles them natively with zero boilerplate.

Use ``@model`` only when you need custom control over auth, request/response
transforms, or non-standard APIs (e.g. AWS Bedrock with SigV4).

Usage in app.py::

    from models import LLAMA_LOCAL, CLAUDE_BEDROCK

    AGENTS = {
        "default": {"model": LLAMA_LOCAL, ...},
        "claude":  {"model": CLAUDE_BEDROCK, ...},
    }
"""

from __future__ import annotations

import json
import os
from typing import Any, Generator

from wick_deep_agent import model


# ---------------------------------------------------------------------------
# Standard providers — Go resolver handles these natively
# ---------------------------------------------------------------------------

LLAMA_LOCAL = "ollama:llama3.1:8b"

LLAMA_70B_LOCAL = "ollama:llama3.1:70b"


# ---------------------------------------------------------------------------
# AWS Bedrock (Claude) — needs @model for SigV4 auth + Bedrock format
# ---------------------------------------------------------------------------


@model(name="bedrock-claude")
class BedrockClaude:
    """Claude via AWS Bedrock with SigV4 auth.

    Requires ``boto3`` and valid AWS credentials (env vars, profile, or IAM role).
    Set ``AWS_REGION`` / ``BEDROCK_MODEL_ID`` to override defaults.
    """

    MODEL_ID = os.environ.get("BEDROCK_MODEL_ID", "anthropic.claude-3-5-sonnet-20241022-v2:0")
    REGION = os.environ.get("AWS_REGION", "us-east-1")
    MAX_TOKENS_DEFAULT = 4096

    def call(self, request: dict[str, Any]) -> dict[str, Any]:
        """Invoke Bedrock synchronously and return parsed response."""
        import boto3

        client = boto3.client("bedrock-runtime", region_name=self.REGION)
        body = self._build_body(request)
        resp = client.invoke_model(modelId=self.MODEL_ID, body=json.dumps(body))
        data = json.loads(resp["body"].read())
        return self._parse_response(data)

    def stream(self, request: dict[str, Any]) -> Generator[dict[str, Any], None, None]:
        """Invoke Bedrock with streaming and yield SSE-compatible chunks."""
        import boto3

        client = boto3.client("bedrock-runtime", region_name=self.REGION)
        body = self._build_body(request)
        resp = client.invoke_model_with_response_stream(
            modelId=self.MODEL_ID, body=json.dumps(body),
        )
        yield from self._parse_stream(resp)

    def _build_body(self, request: dict[str, Any]) -> dict[str, Any]:
        """Convert the generic LLM request into Bedrock's Anthropic message format."""
        messages = []
        for m in request.get("messages", []):
            role = m["role"]
            if role == "tool":
                messages.append({
                    "role": "user",
                    "content": [{
                        "type": "tool_result",
                        "tool_use_id": m.get("tool_call_id", ""),
                        "content": m.get("content", ""),
                    }],
                })
            elif role == "assistant" and m.get("tool_calls"):
                content: list[dict[str, Any]] = []
                if m.get("content"):
                    content.append({"type": "text", "text": m["content"]})
                for tc in m["tool_calls"]:
                    content.append({
                        "type": "tool_use",
                        "id": tc["id"],
                        "name": tc["name"],
                        "input": tc.get("arguments", {}),
                    })
                messages.append({"role": "assistant", "content": content})
            else:
                messages.append({"role": role, "content": m.get("content", "")})

        body: dict[str, Any] = {
            "anthropic_version": "bedrock-2023-05-31",
            "messages": messages,
            "max_tokens": request.get("max_tokens") or self.MAX_TOKENS_DEFAULT,
        }
        if request.get("system_prompt"):
            body["system"] = request["system_prompt"]
        if request.get("tools"):
            body["tools"] = [
                {
                    "name": t["name"],
                    "description": t.get("description", ""),
                    "input_schema": t.get("parameters", {}),
                }
                for t in request["tools"]
            ]
        if request.get("temperature") is not None:
            body["temperature"] = request["temperature"]
        return body

    def _parse_response(self, data: dict[str, Any]) -> dict[str, Any]:
        """Extract content and tool_calls from Bedrock's response content blocks."""
        content_parts: list[str] = []
        tool_calls: list[dict[str, Any]] = []
        for block in data.get("content", []):
            if block["type"] == "text":
                content_parts.append(block["text"])
            elif block["type"] == "tool_use":
                tool_calls.append({
                    "id": block["id"],
                    "name": block["name"],
                    "arguments": block.get("input", {}),
                })
        return {"content": "".join(content_parts), "tool_calls": tool_calls}

    def _parse_stream(self, resp: Any) -> Generator[dict[str, Any], None, None]:
        """Parse Bedrock's streaming SSE events into delta/tool_call/done chunks."""
        current_tool: dict[str, Any] | None = None
        input_json = ""

        for event in resp["body"]:
            chunk = json.loads(event["chunk"]["bytes"])
            etype = chunk.get("type", "")

            if etype == "content_block_start":
                block = chunk.get("content_block", {})
                if block.get("type") == "tool_use":
                    current_tool = {"id": block["id"], "name": block["name"]}
                    input_json = ""

            elif etype == "content_block_delta":
                delta = chunk.get("delta", {})
                if delta.get("type") == "text_delta" and delta.get("text"):
                    yield {"delta": delta["text"]}
                elif delta.get("type") == "input_json_delta":
                    input_json += delta.get("partial_json", "")

            elif etype == "content_block_stop":
                if current_tool is not None:
                    try:
                        args = json.loads(input_json)
                    except json.JSONDecodeError:
                        args = {}
                    yield {"tool_call": {**current_tool, "arguments": args}}
                    current_tool = None

            elif etype == "message_stop":
                break

        yield {"done": True}


CLAUDE_BEDROCK = BedrockClaude
