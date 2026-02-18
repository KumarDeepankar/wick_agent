"""Custom tool and middleware definitions.

Add your own tools/middleware here — they are auto-registered at startup.

Tools:   Plain Python functions with docstrings (used by the LLM).
Middleware: Decorated with @wrap_tool_call to intercept tool execution.
"""

from __future__ import annotations

import logging

from app.config import settings
from app.agents.deep_agent import register_middleware, register_tool

logger = logging.getLogger(__name__)


# ═══════════════════════════════════════════════════════════════════════════
# Custom tools
# ═══════════════════════════════════════════════════════════════════════════


def internet_search(query: str) -> str:
    """Search the internet for information using Tavily.

    Returns a summary of search results for the given query.
    """
    api_key = settings.tavily_api_key
    if not api_key:
        return "Error: TAVILY_API_KEY is not set. Add it to your .env file."
    try:
        from tavily import TavilyClient
        client = TavilyClient(api_key=api_key)
        response = client.search(query, max_results=5)
        results = []
        for r in response.get("results", []):
            results.append(f"**{r.get('title', '')}**\n{r.get('url', '')}\n{r.get('content', '')}")
        return "\n\n---\n\n".join(results) if results else "No results found."
    except Exception as e:
        return f"Search error: {e}"


def calculate(expression: str) -> str:
    """Evaluate a mathematical expression and return the result.

    Only supports basic arithmetic for safety.
    """
    allowed = set("0123456789+-*/.() ")
    if not all(c in allowed for c in expression):
        return "Error: only basic arithmetic is supported."
    try:
        result = eval(expression, {"__builtins__": {}})  # noqa: S307
        return str(result)
    except Exception as e:
        return f"Error: {e}"


def current_datetime() -> str:
    """Return the current date and time in ISO format."""
    from datetime import datetime, timezone

    return datetime.now(timezone.utc).isoformat()


register_tool("internet_search", internet_search)
register_tool("calculate", calculate)
register_tool("current_datetime", current_datetime)


# ═══════════════════════════════════════════════════════════════════════════
# Custom middleware
# ═══════════════════════════════════════════════════════════════════════════
# Middleware intercepts every tool call. Use @wrap_tool_call from
# langchain.agents.middleware to create one.
#
# Example:
#   from langchain.agents.middleware import wrap_tool_call
#
#   @wrap_tool_call
#   def my_middleware(request, handler):
#       print(f"Tool: {request.name}, Input: {request.input}")
#       result = handler(request)
#       print(f"Output: {result}")
#       return result
#
#   register_middleware("my_middleware", my_middleware)
#
# For now we register a simple logging middleware as a reference:


def _create_logging_middleware():
    """Create a logging middleware (only if langchain middleware is available)."""
    try:
        from langchain.agents.middleware import wrap_tool_call

        @wrap_tool_call
        def logging_middleware(request, handler):
            """Log all tool invocations and their results."""
            logger.info("Tool call: %s | Input: %s", request.name, request.input)
            result = handler(request)
            logger.info("Tool done: %s | Output length: %d", request.name, len(str(result)))
            return result

        register_middleware("logging", logging_middleware)
    except ImportError:
        pass


_create_logging_middleware()
