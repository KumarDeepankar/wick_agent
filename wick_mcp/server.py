"""Wick MCP Server — external tool provider using FastMCP.

Run standalone:
    python wick_mcp/server.py

Exposes tools over Streamable HTTP on port 8001.
"""

from __future__ import annotations

from fastmcp import FastMCP

mcp = FastMCP("Wick MCP Tools")


@mcp.tool
def add(a: float, b: float) -> float:
    """Add two numbers together and return the result."""
    return a + b


if __name__ == "__main__":
    mcp.run(transport="http", host="0.0.0.0", port=8001)
