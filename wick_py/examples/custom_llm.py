"""Full agent setup for containerized deployment.

Registers two agents:
  - "default" — Ollama local (Go-native LLM)
  - "gateway-claude" — Custom gateway proxy (Anthropic-compatible)

Uses local backend — each user gets /workspace/{username}/.
Users can switch to Remote Docker in settings for full isolation.
"""

import json
import logging
from datetime import datetime, timezone
from pathlib import Path

import httpx
from wick import Agent, LLMRequest, LLMResponse, StreamChunk, ToolCallResult, tool
from wick._client import WickClient

logger = logging.getLogger("wick.gateway")

# ── Paths ────────────────────────────────────────────────────────────────
import os
repo_root = Path(__file__).resolve().parent.parent.parent
skills_dir = os.environ.get("WICK_SKILLS_DIR") or str(repo_root / "wick_go" / "skills")

# ── Tools (global pool — agents select via builtin_tools) ─────────────────

@tool(description="Get the current date and time in ISO format")
def current_datetime() -> str:
    return datetime.now(timezone.utc).isoformat()


@tool(description="Calculate a math expression (e.g. '2 + 3 * 4')")
def calculate(expression: str) -> str:
    allowed = set("0123456789+-*/.() ")
    if not all(c in allowed for c in expression):
        return "Error: only numeric expressions with +, -, *, /, (, ) are allowed"
    return str(eval(expression))


# ── System prompt ─────────────────────────────────────────────────────────

system_prompt = """You are a helpful AI assistant. Use your available tools and skills to complete tasks. Write output files to /workspace/."""

# Shared config — local backend, per-user workspace folders
agent_config = {
    "builtin_tools": ["calculate", "current_datetime"],
    "backend": {"type": "local", "workdir": "/workspace"},
    "skills": {"paths": [skills_dir]},
    "debug": True,
}

# ── Agent: gateway-claude (Anthropic via Python) ────────────────────────
claude_agent = Agent(
    "gateway-claude",
    name="Claude",
    system_prompt=system_prompt,
    **agent_config,
)


GATEWAY_URL = os.environ.get("GATEWAY_URL", "https://my-gateway.com")
GATEWAY_TOKEN = os.environ.get("GATEWAY_TOKEN", "")
GATEWAY_MODEL = os.environ.get("GATEWAY_MODEL", "claude-sonnet-4-20250514")


@claude_agent.llm_provider("claude-sonnet")
async def gateway_llm(request: LLMRequest):
    """Route LLM calls to custom gateway (Anthropic-compatible)."""

    # Build messages in Anthropic format
    messages = []
    for msg in request.messages:
        if msg.role in ("user", "assistant"):
            messages.append({"role": msg.role, "content": msg.content})

    # Build tools in Anthropic format
    tools = []
    if request.tools:
        for t in request.tools:
            tools.append({
                "name": t.name,
                "description": t.description,
                "input_schema": t.parameters,
            })

    # Build gateway request payload
    payload = {
        "model": GATEWAY_MODEL,
        "max_tokens": request.max_tokens or 4096,
        "messages": messages,
    }
    if request.system_prompt:
        payload["system"] = request.system_prompt
    if tools:
        payload["tools"] = tools

    headers = {
        "Authorization": f"Bearer {GATEWAY_TOKEN}",
        "Content-Type": "application/json",
    }

    async with httpx.AsyncClient(timeout=httpx.Timeout(connect=10.0, read=120.0, write=10.0, pool=5.0)) as client:
        resp = await client.post(
            f"{GATEWAY_URL}/v1/messages",
            headers=headers,
            json=payload,
        )
        resp.raise_for_status()
        result = resp.json()

    logger.info("Gateway response keys: %s", list(result.keys()))

    # Extract content blocks — handle gateway wrapped format or standard Anthropic
    content_blocks = []
    if "result" in result and isinstance(result["result"], list):
        # Gateway wrapped: {"status": "success", "result": [{"content": [...]}]}
        first = result["result"][0] if result["result"] else {}
        content_blocks = first.get("content", []) if isinstance(first, dict) else []
    elif "content" in result:
        # Standard Anthropic: {"content": [...]}
        content_blocks = result.get("content", [])

    # Parse content blocks into StreamChunks with simulated streaming
    import asyncio

    CHUNK_SIZE = 4   # characters per chunk (smaller = smoother)
    CHUNK_DELAY = 0.01  # seconds between chunks

    for block in content_blocks:
        block_type = block.get("type", "")

        if block_type == "text":
            text = block.get("text", "")
            # Emit text in small chunks for typewriter effect
            for i in range(0, len(text), CHUNK_SIZE):
                yield StreamChunk(delta=text[i:i + CHUNK_SIZE])
                await asyncio.sleep(CHUNK_DELAY)

        elif block_type == "tool_use":
            yield StreamChunk(tool_call=ToolCallResult(
                id=block.get("id", f"call_{id(block)}"),
                name=block.get("name", ""),
                args=block.get("input", {}),
            ))

    yield StreamChunk(done=True)


def register_ollama_agent(go_url: str):
    """Register the default Ollama agent with the Go server."""
    ollama_host = os.environ.get("OLLAMA_HOST", "http://localhost:11434")
    client = WickClient(go_url)
    client.register_agent("default", {
        "name": "Ollama Local",
        "model": {"provider": "ollama", "model": "llama3.1:8b", "base_url": f"{ollama_host}/v1"},
        "system_prompt": system_prompt,
        "skills": {"paths": [skills_dir]},
        "backend": {"type": "local", "workdir": "/workspace"},
        "debug": True,
        "subagents": [{
            "name": "researcher",
            "description": "Research a topic using web search and return a summary with sources.",
            "system_prompt": "You are a research assistant. Search the web, verify facts, and provide a concise summary with sources.",
        }],
    })
    client.close()
    print("  registered agent 'default' (Ollama Local)")


if __name__ == "__main__":
    import os
    go_binary = os.environ.get("WICK_SERVER_BINARY") or str(repo_root / "wick_deep_agent" / "server" / "bin" / "wick_go")
    go_port = 8000
    sidecar_port = 9100

    # Register both agents: Claude (via Python LLM proxy) + Ollama (Go-native)
    original_register = claude_agent._register

    def register_both(client, sidecar_url):
        original_register(client, sidecar_url)
        register_ollama_agent(f"http://127.0.0.1:{go_port}")

    claude_agent._register = register_both

    claude_agent.run(
        go_binary=go_binary,
        go_port=go_port,
        sidecar_port=sidecar_port,
    )
