"""Types mirroring Go's llm and agent packages.

These must stay in sync with:
  - wick_deep_agent/server/llm/client.go   (Request, Response, StreamChunk, ToolSchema, Message)
  - wick_deep_agent/server/agent/messages.go (ToolCall, ToolResult)
  - wick_deep_agent/server/agent/config.go   (AgentConfig, BackendCfg, SkillsCfg, etc.)
"""

from __future__ import annotations

from typing import Any

from pydantic import BaseModel, Field


# ── LLM types (mirrors llm/client.go) ──────────────────────────────────────


class ToolCallInfo(BaseModel):
    id: str
    name: str
    args: dict[str, Any] = Field(default_factory=dict, alias="arguments")

    model_config = {"populate_by_name": True}


class LLMMessage(BaseModel):
    role: str
    content: str = ""
    tool_call_id: str | None = None
    name: str | None = None
    tool_calls: list[ToolCallInfo] | None = None

    model_config = {"populate_by_name": True}


class ToolSchema(BaseModel):
    name: str
    description: str
    parameters: dict[str, Any] = Field(default_factory=dict)


class LLMRequest(BaseModel):
    model: str = ""
    messages: list[LLMMessage] = Field(default_factory=list)
    tools: list[ToolSchema] | None = None
    system_prompt: str | None = None
    max_tokens: int | None = None
    temperature: float | None = None

    model_config = {"populate_by_name": True}


class ToolCallResult(BaseModel):
    id: str
    name: str
    args: dict[str, Any] = Field(default_factory=dict, alias="arguments")

    model_config = {"populate_by_name": True}


class LLMResponse(BaseModel):
    content: str = ""
    tool_calls: list[ToolCallResult] | None = None

    model_config = {"populate_by_name": True}


class StreamChunk(BaseModel):
    delta: str | None = None
    tool_call: ToolCallResult | None = None
    done: bool | None = None

    model_config = {"populate_by_name": True}


# ── Tool callback types (mirrors agent/http_tool.go) ───────────────────────


class ToolCallbackRequest(BaseModel):
    """Incoming request from Go's HTTPTool.Execute."""
    name: str
    args: dict[str, Any] = Field(default_factory=dict)


class ToolCallbackResponse(BaseModel):
    """Response back to Go's HTTPTool.Execute."""
    result: str | None = None
    error: str | None = None


# ── Agent config types (mirrors agent/config.go) ───────────────────────────


class BackendConfig(BaseModel):
    type: str = "local"
    workdir: str = "/workspace"
    image: str | None = None
    container_name: str | None = None
    max_tool_output_chars: int | None = None  # 0/None = default 80000; -1 = disable truncation


class SkillsConfig(BaseModel):
    paths: list[str] = Field(default_factory=list)
    host_paths: list[str] = Field(default_factory=list)
    include: list[str] = Field(default_factory=list)  # if set, only these skill names are visible
    exclude: list[str] = Field(default_factory=list)  # skill names to hide


class MemoryConfig(BaseModel):
    paths: list[str] = Field(default_factory=list)


class SubAgentConfig(BaseModel):
    name: str
    description: str = ""
    system_prompt: str = ""
    tools: list[str] = Field(default_factory=list)
