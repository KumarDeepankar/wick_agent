"""Gateway-delegated authentication & authorization.

When ``settings.wick_gateway_url`` is set the agent delegates all auth to
the gateway service — it never validates JWTs locally.  When the setting
is empty (default) every helper is a no-op so local dev works unchanged.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass

import httpx
from fastapi import HTTPException, Request

from app.config import settings

logger = logging.getLogger(__name__)


@dataclass
class GatewayUser:
    username: str
    role: str
    enabled: bool = True


def _extract_token(request: Request) -> str:
    """Pull the Bearer token from the Authorization header or query param.

    Falls back to ``?token=`` query param to support ``EventSource``
    which cannot set custom headers.
    """
    auth = request.headers.get("authorization", "")
    if auth.lower().startswith("bearer "):
        return auth[7:]
    # Fallback for EventSource (can't set headers)
    token = request.query_params.get("token")
    if token:
        return token
    raise HTTPException(status_code=401, detail="Missing or invalid Authorization header")


async def get_current_user(request: Request) -> GatewayUser:
    """FastAPI dependency — validates the caller via the gateway's ``/auth/me``."""
    if not settings.wick_gateway_url:
        # Auth disabled — return a synthetic admin user
        return GatewayUser(username="local", role="admin")

    token = _extract_token(request)

    async with httpx.AsyncClient(timeout=10) as client:
        try:
            resp = await client.get(
                f"{settings.wick_gateway_url}/auth/me",
                headers={"Authorization": f"Bearer {token}"},
            )
        except httpx.RequestError as exc:
            logger.error("Gateway auth request failed: %s", exc)
            raise HTTPException(status_code=502, detail="Auth gateway unreachable")

    if resp.status_code == 401:
        raise HTTPException(status_code=401, detail="Invalid or expired token")
    if resp.status_code != 200:
        raise HTTPException(status_code=502, detail=f"Gateway auth error: {resp.status_code}")

    data = resp.json()
    return GatewayUser(
        username=data.get("username", ""),
        role=data.get("role", "viewer"),
        enabled=data.get("enabled", True),
    )


async def get_allowed_tools(token: str) -> set[str]:
    """Ask the gateway which MCP tool names this token is allowed to use.

    Returns a set of tool name strings.  The special value ``"*"`` means
    all tools are allowed (local-dev shortcut).
    """
    if not settings.wick_gateway_url:
        return {"*"}

    async with httpx.AsyncClient(timeout=10) as client:
        try:
            resp = await client.get(
                f"{settings.wick_gateway_url}/api/tools",
                headers={"Authorization": f"Bearer {token}"},
            )
        except httpx.RequestError as exc:
            logger.error("Gateway tools request failed: %s", exc)
            return set()

    if resp.status_code != 200:
        return set()

    data = resp.json()
    # Gateway returns a flat array [{"name":...}, ...] (not wrapped in {"tools":...}).
    # When a role has zero permitted tools, the gateway may return null/None.
    if data is None:
        return set()
    tools_list = data if isinstance(data, list) else data.get("tools", [])
    return {t["name"] if isinstance(t, dict) else str(t) for t in tools_list}
