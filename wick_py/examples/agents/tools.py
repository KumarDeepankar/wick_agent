"""Global tool definitions.

Tools are registered to wick's global pool on import. Agents select which
tools they see via `builtin_tools=[...]` — the tools here are the full
catalog available to any agent that opts in.

Importing this module has side effects (tool registration). Import it once
from the application entry point or from any module that builds an agent
which uses these tools.
"""

from __future__ import annotations

from datetime import datetime, timezone

from wick import tool


@tool(description="Get the current date and time in ISO format")
def current_datetime() -> str:
    return datetime.now(timezone.utc).isoformat()


@tool(description="Calculate a math expression (e.g. '2 + 3 * 4')")
def calculate(expression: str) -> str:
    allowed = set("0123456789+-*/.() ")
    if not all(c in allowed for c in expression):
        return "Error: only numeric expressions with +, -, *, /, (, ) are allowed"
    return str(eval(expression))
