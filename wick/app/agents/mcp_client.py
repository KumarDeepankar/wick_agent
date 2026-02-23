"""MCP client bridge — discovers tools from external MCP servers and registers them.

Reads ``mcp_servers`` config from agents.yaml, connects to each server via
FastMCP's Client (Streamable HTTP), and wraps every discovered tool as a
regular callable that ``register_tool()`` understands.

Persistent sessions: Each MCP server gets a single long-lived Client session
stored in ``_MCP_CLIENTS``.  Tool calls reuse the session instead of
reconnecting every time.  If a call fails the session is automatically
reconnected and the call retried once.

Authentication uses the MCP-spec-compliant OAuth 2.1 client_credentials flow
via ``ClientCredentialsOAuthProvider`` from the ``mcp`` package.
"""

from __future__ import annotations

import asyncio
import inspect
import logging
import os
from typing import Any

from mcp.client.auth.extensions.client_credentials import ClientCredentialsOAuthProvider
from mcp.shared.auth import OAuthClientInformationFull, OAuthToken

from app.agents.deep_agent import register_tool

logger = logging.getLogger(__name__)

# Persistent client sessions keyed by server name → (Client instance, server_url)
_MCP_CLIENTS: dict[str, tuple[Any, str]] = {}

# Cached OAuth providers keyed by server name (for reconnection)
_OAUTH_PROVIDERS: dict[str, Any] = {}

# JSON-Schema type → Python type mapping
_JSON_TYPE_MAP: dict[str, type] = {
    "string": str,
    "number": float,
    "integer": int,
    "boolean": bool,
    "array": list,
    "object": dict,
}


class InMemoryTokenStorage:
    """Simple in-memory implementation of the ``TokenStorage`` protocol."""

    def __init__(self) -> None:
        self._tokens: OAuthToken | None = None
        self._client_info: OAuthClientInformationFull | None = None

    async def get_tokens(self) -> OAuthToken | None:
        return self._tokens

    async def set_tokens(self, tokens: OAuthToken) -> None:
        self._tokens = tokens

    async def get_client_info(self) -> OAuthClientInformationFull | None:
        return self._client_info

    async def set_client_info(self, client_info: OAuthClientInformationFull) -> None:
        self._client_info = client_info


def _run_async(coro: Any) -> Any:
    """Run an async coroutine from sync code, handling event-loop edge cases."""
    try:
        loop = asyncio.get_running_loop()
    except RuntimeError:
        loop = None

    if loop and loop.is_running():
        # We're inside an async context (e.g. FastAPI lifespan) — use a thread
        import concurrent.futures
        with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
            return pool.submit(asyncio.run, coro).result()
    else:
        return asyncio.run(coro)


async def _connect_client(server_name: str, server_url: str, auth: Any | None = None) -> Any:
    """Create and connect a persistent Client session for a server.

    Stores the session in ``_MCP_CLIENTS`` and returns the connected client.
    When ``auth`` is provided (an httpx.Auth instance such as
    ClientCredentialsOAuthProvider), it handles authentication automatically.
    """
    from fastmcp import Client

    # Close existing session if any
    await _close_client(server_name)

    kwargs: dict[str, Any] = {}
    if auth is not None:
        kwargs["auth"] = auth

    client = Client(server_url, **kwargs)
    connected = await client.__aenter__()
    _MCP_CLIENTS[server_name] = (connected, server_url)
    logger.info("Persistent MCP session opened for '%s' at %s (auth=%s)", server_name, server_url, auth is not None)
    return connected


async def _close_client(server_name: str) -> None:
    """Close a persistent Client session if it exists."""
    if server_name in _MCP_CLIENTS:
        client, _ = _MCP_CLIENTS.pop(server_name)
        try:
            await client.__aexit__(None, None, None)
            logger.info("Closed MCP session for '%s'", server_name)
        except Exception:
            pass


async def _get_client(server_name: str) -> Any:
    """Return the persistent client for a server, reconnecting if needed."""
    if server_name not in _MCP_CLIENTS:
        raise RuntimeError(f"No MCP session for server '{server_name}'")
    client, server_url = _MCP_CLIENTS[server_name]
    return client


async def _call_with_reconnect(
    server_name: str,
    tool_name: str,
    kwargs: dict[str, Any],
) -> str:
    """Call a tool on the persistent session, reconnecting once on failure."""
    for attempt in range(2):
        try:
            client = await _get_client(server_name)
            result = await client.call_tool(tool_name, kwargs, raise_on_error=False)
            if result.is_error:
                text_parts = [
                    c.text for c in (result.content or []) if hasattr(c, "text")
                ]
                return f"Error: {' '.join(text_parts)}" if text_parts else "Error: tool call failed"
            if result.data is not None:
                return str(result.data)
            text_parts = [
                c.text for c in (result.content or []) if hasattr(c, "text")
            ]
            return " ".join(text_parts) if text_parts else ""
        except Exception as exc:
            if attempt == 0 and server_name in _MCP_CLIENTS:
                _, server_url = _MCP_CLIENTS[server_name]
                logger.warning(
                    "MCP call '%s' on '%s' failed, reconnecting (%s)",
                    tool_name, server_name, exc,
                )
                try:
                    await _connect_client(server_name, server_url, _OAUTH_PROVIDERS.get(server_name))
                except Exception as reconn_exc:
                    return f"Error: reconnect to '{server_name}' failed ({reconn_exc})"
            else:
                return f"Error: {exc}"
    return "Error: tool call failed after retry"


def _build_tool_wrapper(
    server_name: str,
    tool_name: str,
    tool_description: str,
    input_schema: dict[str, Any],
) -> callable:
    """Build a sync wrapper callable for an MCP tool.

    The wrapper has proper ``__name__``, ``__doc__``, and ``__annotations__``
    so the LLM sees a well-described tool.  Calls go through the persistent
    session with automatic reconnect.
    """
    properties = input_schema.get("properties", {})
    required = set(input_schema.get("required", []))

    # Build parameter annotations
    annotations: dict[str, type] = {}
    for param_name, param_schema in properties.items():
        json_type = param_schema.get("type", "string")
        py_type = _JSON_TYPE_MAP.get(json_type, str)
        annotations[param_name] = py_type
    annotations["return"] = str

    # Build the parameter list for the function signature
    params = []
    for param_name in properties:
        if param_name in required:
            params.append(
                inspect.Parameter(param_name, inspect.Parameter.POSITIONAL_OR_KEYWORD)
            )
        else:
            params.append(
                inspect.Parameter(
                    param_name,
                    inspect.Parameter.POSITIONAL_OR_KEYWORD,
                    default=None,
                )
            )

    def wrapper(**kwargs: Any) -> str:
        return _run_async(_call_with_reconnect(server_name, tool_name, kwargs))

    # Set metadata so the LLM and tool-schema extractor see a proper tool
    registered_name = f"mcp_{server_name}_{tool_name}"
    wrapper.__name__ = registered_name
    wrapper.__qualname__ = registered_name
    wrapper.__doc__ = tool_description or f"MCP tool: {tool_name}"
    wrapper.__annotations__ = annotations

    # Attach a proper signature
    sig = inspect.Signature(parameters=params)
    wrapper.__signature__ = sig  # type: ignore[attr-defined]

    return wrapper


async def _discover_and_register(server_name: str, server_url: str, auth: Any | None = None) -> int:
    """Open a persistent session to one MCP server, discover tools, register them.

    Returns the number of tools registered.
    """
    count = 0
    try:
        client = await _connect_client(server_name, server_url, auth)
        tools = await client.list_tools()
        for tool in tools:
            wrapper = _build_tool_wrapper(
                server_name=server_name,
                tool_name=tool.name,
                tool_description=tool.description or "",
                input_schema=tool.inputSchema or {},
            )
            registered_name = f"mcp_{server_name}_{tool.name}"
            register_tool(registered_name, wrapper)
            logger.info("Registered MCP tool: %s", registered_name)
            count += 1
    except Exception as exc:
        logger.warning(
            "Could not connect to MCP server '%s' at %s — skipping (%s)",
            server_name,
            server_url,
            exc,
        )
    return count


def load_mcp_tools(servers_config: dict[str, Any]) -> int:
    """Discover and register tools from all configured MCP servers.

    ``servers_config`` maps server names to config dicts, each containing
    at least a ``url`` key.  Example::

        {"wick": {"url": "http://localhost:8001/mcp",
                  "auth": {"client_id": "agent", "client_secret": "secret"}}}

    Returns total number of MCP tools registered.
    """
    if not servers_config:
        return 0

    total = 0
    for name, cfg in servers_config.items():
        url = cfg.get("url") if isinstance(cfg, dict) else None
        # Environment variable override: WICK_MCP_<NAME>_URL
        env_url = os.environ.get(f"WICK_MCP_{name.upper()}_URL")
        if env_url:
            url = env_url
        if not url:
            logger.warning("MCP server '%s' has no url — skipping", name)
            continue

        # Build OAuth provider if client credentials are configured
        auth: Any | None = None
        auth_cfg = cfg.get("auth") if isinstance(cfg, dict) else None
        if auth_cfg and isinstance(auth_cfg, dict):
            client_id = auth_cfg.get("client_id", "")
            client_secret = auth_cfg.get("client_secret", "")
            if client_id and client_secret:
                try:
                    provider = ClientCredentialsOAuthProvider(
                        server_url=url,
                        storage=InMemoryTokenStorage(),
                        client_id=client_id,
                        client_secret=client_secret,
                    )
                    auth = provider
                    _OAUTH_PROVIDERS[name] = provider
                    logger.info("OAuth client_credentials provider created for '%s'", name)
                except Exception as exc:
                    logger.warning(
                        "Failed to create OAuth provider for '%s' — trying without auth (%s)",
                        name, exc,
                    )

        logger.info("Discovering tools from MCP server '%s' at %s (auth=%s)", name, url, auth is not None)
        count = _run_async(_discover_and_register(name, url, auth))
        total += count

    logger.info("Registered %d MCP tool(s) total", total)
    return total


async def close_all_mcp_clients() -> None:
    """Gracefully close all persistent MCP sessions.

    Call this during application shutdown.
    """
    for name in list(_MCP_CLIENTS.keys()):
        await _close_client(name)
    logger.info("All MCP sessions closed")
