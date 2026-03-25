"""HTTP client for the Go wick_server API.

Handles agent and tool registration with the Go server.
"""

from __future__ import annotations

import logging
import time
from typing import Any

import httpx

logger = logging.getLogger("wick.client")


class WickClient:
    """Client for the Go wick_server HTTP API."""

    def __init__(self, base_url: str = "http://localhost:8000") -> None:
        self._base = base_url.rstrip("/")
        self._http = httpx.Client(timeout=10.0)

    def health(self) -> dict[str, Any]:
        """GET /health"""
        resp = self._http.get(f"{self._base}/health")
        resp.raise_for_status()
        return resp.json()

    def wait_ready(self, timeout: float = 15.0) -> None:
        """Poll health endpoint until the server is ready."""
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            try:
                self.health()
                return
            except (httpx.ConnectError, httpx.ReadTimeout):
                time.sleep(0.3)
        raise TimeoutError(f"Go server not ready after {timeout}s")

    def register_agent(self, agent_id: str, config: dict[str, Any]) -> dict[str, Any]:
        """POST /agents/ — create or update an agent.

        Contract: handlers.go createAgent
        Body: {agent_id, name, model, system_prompt, tools, middleware,
               subagents, backend, skills, memory, debug, context_window}
        """
        payload = {"agent_id": agent_id, **config}
        resp = self._http.post(f"{self._base}/agents/", json=payload)
        resp.raise_for_status()
        return resp.json()

    def register_tool(
        self,
        name: str,
        description: str,
        parameters: dict[str, Any],
        callback_url: str,
        agent_id: str | None = None,
    ) -> dict[str, Any]:
        """POST /agents/tools/register — register an external HTTP tool.

        Contract: handlers.go registerTool
        Body: {name, description, parameters, callback_url, agent_id?}
        Response: {status: "registered", name: str, agent_id: str}
        """
        payload: dict[str, Any] = {
            "name": name,
            "description": description,
            "parameters": parameters,
            "callback_url": callback_url,
        }
        if agent_id:
            payload["agent_id"] = agent_id
        resp = self._http.post(f"{self._base}/agents/tools/register", json=payload)
        resp.raise_for_status()
        return resp.json()

    def deregister_tool(self, name: str) -> dict[str, Any]:
        """DELETE /agents/tools/deregister/{name}"""
        resp = self._http.delete(f"{self._base}/agents/tools/deregister/{name}")
        resp.raise_for_status()
        return resp.json()

    def close(self) -> None:
        self._http.close()
