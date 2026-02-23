"""Thin auth proxy so the UI only talks to the agent origin (no CORS issues).

Forwards ``/auth/login`` and ``/auth/me`` to the gateway.  When the
gateway URL is not configured the endpoints return a helpful error.
"""

from __future__ import annotations

import httpx
from fastapi import APIRouter, HTTPException, Request
from fastapi.responses import JSONResponse

from app.config import settings

router = APIRouter(prefix="/auth", tags=["auth"])


def _require_gateway() -> str:
    if not settings.wick_gateway_url:
        raise HTTPException(status_code=501, detail="Auth not configured (WICK_GATEWAY_URL not set)")
    return settings.wick_gateway_url


@router.post("/login")
async def proxy_login(request: Request):
    """Proxy login to gateway and return ``{token, user}``."""
    gw = _require_gateway()
    body = await request.body()

    async with httpx.AsyncClient(timeout=10) as client:
        try:
            resp = await client.post(
                f"{gw}/auth/login",
                content=body,
                headers={"Content-Type": "application/json"},
            )
        except httpx.RequestError as exc:
            raise HTTPException(status_code=502, detail=f"Gateway unreachable: {exc}")

    return JSONResponse(content=resp.json(), status_code=resp.status_code)


@router.get("/me")
async def proxy_me(request: Request):
    """Proxy ``/auth/me`` — validates token and returns user info."""
    gw = _require_gateway()
    auth_header = request.headers.get("authorization", "")

    async with httpx.AsyncClient(timeout=10) as client:
        try:
            resp = await client.get(
                f"{gw}/auth/me",
                headers={"Authorization": auth_header},
            )
        except httpx.RequestError as exc:
            raise HTTPException(status_code=502, detail=f"Gateway unreachable: {exc}")

    return JSONResponse(content=resp.json(), status_code=resp.status_code)
