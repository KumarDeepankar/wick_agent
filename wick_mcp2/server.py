"""Wick MCP2 Server — multiply tool provider using FastMCP.

Run standalone:
    python wick_mcp2/server.py

Exposes tools over Streamable HTTP on port 8002.
"""

from __future__ import annotations

from fastmcp import FastMCP

mcp = FastMCP("Wick MCP2 Tools")


@mcp.tool
def multiply(a: float, b: float) -> float:
    """Multiply two numbers together and return the result."""
    return a * b


if __name__ == "__main__":
    mcp.run(transport="http", host="0.0.0.0", port=8002)
