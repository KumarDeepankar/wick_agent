"""Full agent setup for containerized deployment.

Registers two agents:
  - "default" — Ollama local (Go-native LLM)
  - "gateway-claude" — Custom gateway proxy (Anthropic-compatible)

Uses local backend — each user gets /workspace/{username}/.
Users can switch to Remote Docker in settings for full isolation.
"""

import asyncio
import json
import logging
import os
import threading
from datetime import datetime, timezone
from pathlib import Path

import httpx
from wick import Agent, LLMRequest, StreamChunk, ToolCallResult, tool
from gateway_auth import fetch_token

logger = logging.getLogger("wick.gateway")

# ── Paths ────────────────────────────────────────────────────────────────

repo_root = Path(__file__).resolve().parent.parent.parent
skills_dir = os.environ.get("WICK_SKILLS_DIR") or str(repo_root / "wick_py" / "skills")

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


# ── Shared config ────────────────────────────────────────────────────────

system_prompt = """You are a helpful AI assistant. Use your available tools and skills to complete tasks. Write output files to the workspace directory."""

shared_config = {
    "backend": {"type": "local", "workdir": "/workspace"},
    "skills": {"paths": [skills_dir]},
    "debug": True,
}

# ── Sub-agents ───────────────────────────────────────────────────────────

math_agent = Agent(
    "math",
    name="Math Assistant",
    system_prompt="You are a math assistant. Use the calculate tool to evaluate expressions. Show your work step by step.",
    builtin_tools=["calculate"],
)

report_agent = Agent(
    "report-generator",
    name="Report Generator",
    system_prompt="You generate visual slide-deck reports from research artifacts on disk. "
    "The task message contains the source directory path and focus area. "
    "Read the artifacts, extract real data, and write a <!-- slides --> markdown file "
    "with charts (using the chart DSL) and detailed analysis. "
    "Never fabricate numbers — every data point must come from the artifacts.",
    builtin_tools=["read_file", "write_file", "ls", "glob"],
)

# ── Agent: gateway-claude (Anthropic via Python LLM proxy) ───────────────

claude_agent = Agent(
    "gateway-claude",
    name="Claude",
    system_prompt=system_prompt,
    builtin_tools=["calculate", "current_datetime"],
    subagents=[report_agent],
    **shared_config,
)

# ── Agent: default (Ollama, Go-native LLM) ──────────────────────────────

ollama_host = os.environ.get("OLLAMA_HOST", "http://localhost:11434")

ollama_agent = Agent(
    "default",
    name="Ollama Local",
    model={"provider": "ollama", "model": "llama3.1:8b", "base_url": f"{ollama_host}/v1"},
    system_prompt=system_prompt,
    subagents=[math_agent, report_agent],
    **shared_config,
)

# ── Gateway LLM provider for Claude ─────────────────────────────────────

GATEWAY_URL = os.environ.get("GATEWAY_URL", "https://xyz-abc")
GATEWAY_MODEL = os.environ.get("GATEWAY_MODEL", "anthropic.claude-4-5-sonnet-v1:0")
TOKEN_REFRESH_INTERVAL = 20 * 60  # 20 minutes

# Token state — refreshed by background thread
GATEWAY_TOKEN = ""
_token_lock = threading.Lock()


def _refresh_token():
    global GATEWAY_TOKEN
    try:
        new_token = fetch_token()
        with _token_lock:
            GATEWAY_TOKEN = new_token
        logger.info("Gateway token refreshed")
    except Exception as e:
        logger.error("Token refresh failed: %s", e)


def _token_refresh_loop():
    """Background thread that refreshes the token every 20 minutes."""
    while True:
        _refresh_token()
        threading.Event().wait(TOKEN_REFRESH_INTERVAL)


def _get_token() -> str:
    with _token_lock:
        return GATEWAY_TOKEN


# Fetch initial token and start refresh thread
_refresh_token()
_refresh_thread = threading.Thread(target=_token_refresh_loop, daemon=True)
_refresh_thread.start()


@claude_agent.llm_provider("claude-sonnet")
async def gateway_llm(request: LLMRequest):
    """Route LLM calls to gateway (OpenAI chat completions format)."""

    # Build messages in OpenAI chat completions format.
    # Must include all roles: system, user, assistant (with tool_calls), and tool.
    messages = []
    if request.system_prompt:
        messages.append({"role": "system", "content": request.system_prompt})
    for msg in request.messages:
        if msg.role == "user":
            messages.append({"role": "user", "content": msg.content})
        elif msg.role == "assistant":
            m = {"role": "assistant", "content": msg.content or ""}
            # Preserve tool_calls on assistant messages (OpenAI format)
            if hasattr(msg, "tool_calls") and msg.tool_calls:
                m["tool_calls"] = [
                    {
                        "id": tc.id,
                        "type": "function",
                        "function": {
                            "name": tc.name,
                            "arguments": json.dumps(tc.args) if isinstance(tc.args, dict) else tc.args,
                        },
                    }
                    for tc in msg.tool_calls
                ]
            messages.append(m)
        elif msg.role == "tool":
            messages.append({
                "role": "tool",
                "tool_call_id": msg.tool_call_id,
                "content": msg.content or "",
            })

    # Build tools in OpenAI function-calling format
    tools = []
    if request.tools:
        for t in request.tools:
            tools.append({
                "type": "function",
                "function": {
                    "name": t.name,
                    "description": t.description,
                    "parameters": t.parameters,
                },
            })

    payload = {
        "model": GATEWAY_MODEL,
        "max_tokens": request.max_tokens or 4096,
        "messages": messages,
    }
    if tools:
        payload["tools"] = tools

    headers = {
        "Content-Type": "application/json",
        "Authorization": f"Bearer {_get_token()}",
    }

    async with httpx.AsyncClient(timeout=httpx.Timeout(connect=10.0, read=120.0, write=10.0, pool=5.0)) as client:
        resp = await client.post(
            f"{GATEWAY_URL}/chat/completions",
            headers=headers,
            json=payload,
        )
        resp.raise_for_status()
        result = resp.json()

    logger.info("Gateway response keys: %s", list(result.keys()))

    # Parse OpenAI chat completions response
    choices = result.get("choices", [])
    if not choices:
        logger.error("No choices in gateway response")
        yield StreamChunk(done=True)
        return

    message = choices[0].get("message", {})

    # Handle text content
    content = message.get("content") or ""
    if content:
        CHUNK_SIZE = 4
        CHUNK_DELAY = 0.01
        for i in range(0, len(content), CHUNK_SIZE):
            yield StreamChunk(delta=content[i:i + CHUNK_SIZE])
            await asyncio.sleep(CHUNK_DELAY)

    # Handle tool calls
    tool_calls = message.get("tool_calls", [])
    for tc in tool_calls:
        func = tc.get("function", {})
        args = func.get("arguments", "{}")
        if isinstance(args, str):
            args = json.loads(args)
        yield StreamChunk(tool_call=ToolCallResult(
            id=tc.get("id", f"call_{id(tc)}"),
            name=func.get("name", ""),
            args=args,
        ))

    yield StreamChunk(done=True)


# ── Main ────────────────────────────────────────────────────────────────

if __name__ == "__main__":
    claude_agent.run(
        go_binary=os.environ.get("WICK_SERVER_BINARY"),
        go_port=8000,
        sidecar_port=9100,
        extra_agents=[ollama_agent],
    )
