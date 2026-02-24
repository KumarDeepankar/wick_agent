#!/usr/bin/env python3
"""wick_go standalone app — uses wick_deep_agent to run the Go agent server.

Everything is defined inline — no agents.yaml needed.

Usage:
    python app.py                       # default: port 8000
    python app.py --port 8001           # custom port
    python app.py --build               # dev mode: compile Go source first
"""

from __future__ import annotations

import argparse
import signal
import sys
import time
from pathlib import Path

from wick_deep_agent import WickServer, tool

HERE = Path(__file__).resolve().parent
SKILLS_DIR = str(HERE / "skills")

# Custom model definitions — edit models.py to add your own LLMs
sys.path.insert(0, str(HERE))
from models import LLAMA_LOCAL, CLAUDE_BEDROCK  # noqa: E402


# ---------------------------------------------------------------------------
# System prompt (shared across agents)
# ---------------------------------------------------------------------------

SYSTEM_PROMPT = """\
You are a versatile AI assistant that creates high-quality content using your skills library.

When a user asks you to create documents, presentations, reports, spreadsheets, or other structured content:
1. Check your available skills first — read the relevant SKILL.md for full instructions before acting.
2. Always follow the skill's workflow (file format, markers, naming conventions).
3. Write output files to /workspace/ using write_file.

For presentations or slide decks: always use the slides skill.
For data analysis or CSV work: always use the csv-analyzer or data-analysis skill.
For research tasks: always use the research skill.

Prefer using skills over writing custom code. Skills give you proven, consistent workflows."""


# ---------------------------------------------------------------------------
# App-level tools — define your own tools here.
# These are registered with the Go agent at startup via HTTP callback.
# The LLM can call them like any built-in tool.
# ---------------------------------------------------------------------------


@tool(description="Add two numbers together and return the sum.")
def add(a: int, b: int) -> str:
    return str(a + b)


@tool(description="Get the current weather for a city (demo — returns mock data).")
def weather(city: str) -> str:
    return f"Weather in {city}: 72°F, sunny"


APP_TOOLS = [add, weather]


# ---------------------------------------------------------------------------
# Agent definitions (replaces agents.yaml)
# ---------------------------------------------------------------------------

DEFAULTS = {
    "backend": {"type": "local", "workdir": "/workspace"},
    "debug": True,
}

AGENTS = {
    "default": {
        "name": "Ollama Local",
        "model": LLAMA_LOCAL,
        "system_prompt": SYSTEM_PROMPT,
        "tools": ["internet_search", "calculate", "current_datetime"],
        "skills": {"paths": [SKILLS_DIR]},
        "subagents": [
            {
                "name": "researcher",
                "description": "Research a topic using web search and return a summary with sources.",
                "system_prompt": "You are a research assistant. Search the web, verify facts, and provide a concise summary with sources.",
                "tools": ["internet_search"],
            }
        ],
    },
    "gateway-claude": {
        "name": "Bedrock Claude",
        "model": CLAUDE_BEDROCK,
        "system_prompt": SYSTEM_PROMPT,
        "tools": ["internet_search", "calculate", "current_datetime"],
        "skills": {"paths": [SKILLS_DIR]},
    },
}


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main() -> None:
    parser = argparse.ArgumentParser(description="Run wick_go agent server")
    parser.add_argument("--port", type=int, default=8000, help="Server port (default: 8000)")
    parser.add_argument("--host", type=str, default="0.0.0.0", help="Server host (default: 0.0.0.0)")
    parser.add_argument("--build", action="store_true", help="Build Go binary before starting (dev mode)")
    parser.add_argument("--gateway", type=str, default=None,
                        help="URL of wick_gateway for auth (e.g. http://localhost:4000)")
    args = parser.parse_args()

    if args.build:
        print("Building Go server...")
        WickServer.build()
        print("Build complete.")

    env = {}
    if args.gateway:
        env["WICK_GATEWAY_URL"] = args.gateway

    server = WickServer(
        port=args.port,
        host=args.host,
        agents=AGENTS,
        defaults=DEFAULTS,
        tools=APP_TOOLS,
        env=env or None,
    )

    pid = server.start()
    if not server.wait_ready(timeout=15):
        print("ERROR: Server did not become ready.", file=sys.stderr)
        print(server.logs(n=20), file=sys.stderr)
        server.stop()
        sys.exit(1)

    server.register_agents()
    server.register_tools()

    tool_names = [t.name for t in APP_TOOLS]
    print(f"wick_go running on http://{args.host}:{args.port} (pid={pid})")
    print(f"Agents: {list(AGENTS.keys())}")
    print(f"External tools: {tool_names}")
    print("Press Ctrl+C to stop.")

    def _shutdown(sig: int, frame: object) -> None:
        print("\nStopping server...")
        server.stop()
        sys.exit(0)

    signal.signal(signal.SIGINT, _shutdown)
    signal.signal(signal.SIGTERM, _shutdown)

    try:
        signal.pause()
    except AttributeError:
        while True:
            time.sleep(1)


if __name__ == "__main__":
    main()
