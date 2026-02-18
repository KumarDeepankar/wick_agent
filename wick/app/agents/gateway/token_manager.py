"""OAuth2 client_credentials token manager with async-first design.

Handles automatic token acquisition and proactive refresh for
gateway endpoints that require OAuth2 authentication.
"""

from __future__ import annotations

import asyncio
import logging
import time
from typing import Any

import httpx

logger = logging.getLogger(__name__)

# Refresh token this many seconds before actual expiry
_REFRESH_BUFFER_SECONDS = 60


class GatewayTokenManager:
    """Manages OAuth2 client_credentials tokens with caching and auto-refresh.

    Features:
        - Async-first with sync fallback
        - Double-checked locking — only one refresh at a time
        - Proactive refresh 60s before expiry
        - Reuses httpx.AsyncClient for connection pooling
    """

    def __init__(
        self,
        token_url: str,
        client_id: str,
        client_secret: str,
        scopes: list[str] | None = None,
    ) -> None:
        self._token_url = token_url
        self._client_id = client_id
        self._client_secret = client_secret
        self._scopes = scopes or []

        # Cached token state
        self._access_token: str | None = None
        self._expires_at: float = 0.0  # epoch timestamp

        # Async lock for concurrent refresh protection
        self._lock = asyncio.Lock()

        # Lazy-initialized HTTP clients
        self._async_client: httpx.AsyncClient | None = None

    @property
    def _is_token_valid(self) -> bool:
        """Check if the current token is valid (with buffer)."""
        if self._access_token is None:
            return False
        return time.time() < (self._expires_at - _REFRESH_BUFFER_SECONDS)

    async def get_token(self) -> str:
        """Get a valid access token, refreshing if needed (async)."""
        if self._is_token_valid:
            return self._access_token  # type: ignore[return-value]

        async with self._lock:
            # Double-check after acquiring lock
            if self._is_token_valid:
                return self._access_token  # type: ignore[return-value]

            await self._refresh_token()
            return self._access_token  # type: ignore[return-value]

    def get_token_sync(self) -> str:
        """Get a valid access token, refreshing if needed (sync fallback).

        Creates a temporary event loop if none is running, or uses
        httpx sync client directly.
        """
        if self._is_token_valid:
            return self._access_token  # type: ignore[return-value]

        self._refresh_token_sync()
        return self._access_token  # type: ignore[return-value]

    async def _refresh_token(self) -> None:
        """Fetch a new token from the OAuth2 endpoint (async)."""
        if self._async_client is None:
            self._async_client = httpx.AsyncClient(timeout=30.0)

        data: dict[str, Any] = {
            "grant_type": "client_credentials",
            "client_id": self._client_id,
            "client_secret": self._client_secret,
        }
        if self._scopes:
            data["scope"] = " ".join(self._scopes)

        logger.info("Refreshing OAuth2 token from %s", self._token_url)

        resp = await self._async_client.post(self._token_url, data=data)
        resp.raise_for_status()
        body = resp.json()

        self._access_token = body["access_token"]
        expires_in = body.get("expires_in", 1800)  # default 30 min
        self._expires_at = time.time() + expires_in

        logger.info(
            "OAuth2 token refreshed, expires_in=%ds (at %.0f)",
            expires_in, self._expires_at,
        )

    def _refresh_token_sync(self) -> None:
        """Fetch a new token from the OAuth2 endpoint (sync)."""
        data: dict[str, Any] = {
            "grant_type": "client_credentials",
            "client_id": self._client_id,
            "client_secret": self._client_secret,
        }
        if self._scopes:
            data["scope"] = " ".join(self._scopes)

        logger.info("Refreshing OAuth2 token (sync) from %s", self._token_url)

        with httpx.Client(timeout=30.0) as client:
            resp = client.post(self._token_url, data=data)
            resp.raise_for_status()
            body = resp.json()

        self._access_token = body["access_token"]
        expires_in = body.get("expires_in", 1800)
        self._expires_at = time.time() + expires_in

        logger.info(
            "OAuth2 token refreshed (sync), expires_in=%ds (at %.0f)",
            expires_in, self._expires_at,
        )

    async def close(self) -> None:
        """Close the underlying HTTP client."""
        if self._async_client:
            await self._async_client.aclose()
            self._async_client = None
