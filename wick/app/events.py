"""Gateway SSE subscriber + in-process event bus.

Connects to the gateway's ``/api/events`` SSE endpoint and relays
``config_changed`` events to all subscribed browser clients via
per-client asyncio queues.
"""

from __future__ import annotations

import asyncio
import logging

import httpx

from app.config import settings

logger = logging.getLogger(__name__)

_subscribers: set[asyncio.Queue] = set()
_subscriber_lock = asyncio.Lock()
_bg_task: asyncio.Task | None = None


async def subscribe() -> asyncio.Queue:
    """Register a new subscriber queue and return it."""
    q: asyncio.Queue = asyncio.Queue(maxsize=32)
    async with _subscriber_lock:
        _subscribers.add(q)
    return q


async def unsubscribe(q: asyncio.Queue) -> None:
    """Remove a subscriber queue."""
    async with _subscriber_lock:
        _subscribers.discard(q)


async def _broadcast(event: str, username: str | None = None) -> None:
    """Push an event string to all subscriber queues.

    When ``username`` is provided the payload is encoded as
    ``"event:username"`` so that per-user SSE filtering can skip
    events intended for other users.
    """
    payload = f"{event}:{username}" if username else event
    async with _subscriber_lock:
        dead: list[asyncio.Queue] = []
        for q in _subscribers:
            try:
                q.put_nowait(payload)
            except asyncio.QueueFull:
                dead.append(q)
        for q in dead:
            _subscribers.discard(q)


async def _gateway_sse_loop() -> None:
    """Subscribe to gateway SSE and relay events to local subscribers."""
    url = f"{settings.wick_gateway_url}/api/events"
    # Explicit timeout config: no read timeout (SSE is long-lived),
    # but keep a connect timeout so we don't hang forever on startup.
    timeout = httpx.Timeout(connect=10.0, read=None, write=None, pool=None)
    while True:
        try:
            async with httpx.AsyncClient(timeout=timeout) as client:
                async with client.stream(
                    "GET", url, headers={"Accept": "text/event-stream"}
                ) as resp:
                    logger.info("Connected to gateway SSE at %s", url)
                    async for line in resp.aiter_lines():
                        if line.startswith("data:"):
                            logger.debug("Gateway SSE event received, broadcasting")
                            await _broadcast("config_changed")
        except asyncio.CancelledError:
            return
        except Exception as exc:
            logger.warning("Gateway SSE connection lost: %s — retrying in 3s", exc)
        await asyncio.sleep(3)


async def start() -> None:
    """Start the background gateway SSE subscriber task."""
    global _bg_task
    if not settings.wick_gateway_url:
        return
    _bg_task = asyncio.create_task(_gateway_sse_loop())


async def stop() -> None:
    """Cancel the background gateway SSE subscriber task."""
    global _bg_task
    if _bg_task:
        _bg_task.cancel()
        try:
            await _bg_task
        except asyncio.CancelledError:
            pass
        _bg_task = None
