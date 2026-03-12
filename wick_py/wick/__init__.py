"""wick — Python SDK for wick_server.

Define agents with custom tools and LLM providers in Python,
backed by the Go wick_server engine.

Usage:
    from wick import Agent, StreamChunk, LLMRequest

    agent = Agent("my-agent", system_prompt="You are helpful.")

    @agent.tool(description="Add two numbers")
    def add(a: float, b: float) -> str:
        return str(a + b)

    @agent.llm_provider("my-model")
    async def my_llm(request: LLMRequest):
        yield StreamChunk(delta="Hello!")
        yield StreamChunk(done=True)

    # Dev mode: starts Go binary + sidecar
    agent.run()

    # Production mode: sidecar only, Go runs separately
    # agent.serve_sidecar(go_url="http://go-server:8000")
"""

__version__ = "0.1.0"

from ._agent import Agent
from ._tools import tool
from ._types import (
    BackendConfig,
    LLMMessage,
    LLMRequest,
    LLMResponse,
    MemoryConfig,
    SkillsConfig,
    StreamChunk,
    SubAgentConfig,
    ToolCallbackRequest,
    ToolCallbackResponse,
    ToolCallResult,
    ToolSchema,
)

__all__ = [
    "Agent",
    "BackendConfig",
    "tool",
    "LLMMessage",
    "LLMRequest",
    "LLMResponse",
    "MemoryConfig",
    "SkillsConfig",
    "StreamChunk",
    "SubAgentConfig",
    "ToolCallbackRequest",
    "ToolCallbackResponse",
    "ToolCallResult",
    "ToolSchema",
]
