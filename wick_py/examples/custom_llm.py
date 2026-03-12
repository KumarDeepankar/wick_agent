"""Full agent setup for containerized deployment.

Registers two agents:
  - "default" — Ollama local (Go-native LLM)
  - "gateway-claude" — Anthropic via Python LLM proxy

Uses local backend — each user gets /workspace/{username}/.
Users can switch to Remote Docker in settings for full isolation.
"""

import json
from datetime import datetime, timezone
from pathlib import Path

from wick import Agent, LLMRequest, StreamChunk, ToolCallResult, tool
from wick._client import WickClient

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

system_prompt = """You are a versatile AI assistant that creates high-quality content using your skills library.

When a user asks you to create documents, presentations, reports, spreadsheets, or other structured content:
1. Check your available skills first — read the relevant SKILL.md for full instructions before acting.
2. Always follow the skill's workflow (file format, markers, naming conventions).
3. Write output files to /workspace/ using write_file.

For presentations or slide decks: always use the slides skill.
For data analysis or CSV work: always use the csv-analyzer or data-analysis skill.
For research tasks: always use the research skill.

Prefer using skills over writing custom code. Skills give you proven, consistent workflows."""

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


@claude_agent.llm_provider("claude-sonnet")
async def anthropic_llm(request: LLMRequest):
    """Route LLM calls to Anthropic Claude via Python."""
    import anthropic

    client = anthropic.AsyncAnthropic()

    messages = []
    for msg in request.messages:
        if msg.role in ("user", "assistant"):
            messages.append({"role": msg.role, "content": msg.content})

    tools = []
    if request.tools:
        for t in request.tools:
            tools.append({
                "name": t.name,
                "description": t.description,
                "input_schema": t.parameters,
            })

    async with client.messages.stream(
        model="claude-sonnet-4-20250514",
        system=request.system_prompt or "",
        messages=messages,
        tools=tools if tools else anthropic.NOT_GIVEN,
        max_tokens=request.max_tokens or 4096,
    ) as stream:
        current_tool_id = None
        current_tool_name = None
        args_json = ""

        async for event in stream:
            if event.type == "content_block_start":
                if hasattr(event.content_block, "type") and event.content_block.type == "tool_use":
                    current_tool_id = event.content_block.id
                    current_tool_name = event.content_block.name
                    args_json = ""

            elif event.type == "content_block_delta":
                if hasattr(event.delta, "text"):
                    yield StreamChunk(delta=event.delta.text)
                elif hasattr(event.delta, "partial_json"):
                    args_json += event.delta.partial_json

            elif event.type == "content_block_stop":
                if current_tool_id:
                    args = json.loads(args_json) if args_json else {}
                    yield StreamChunk(tool_call=ToolCallResult(
                        id=current_tool_id,
                        name=current_tool_name,
                        args=args,
                    ))
                    current_tool_id = None
                    current_tool_name = None

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
