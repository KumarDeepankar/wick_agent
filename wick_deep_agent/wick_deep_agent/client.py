"""WickClient — typed HTTP client for the wick_go agent server.

    from wick_deep_agent import WickClient
    from wick_deep_agent.messages import HumanMessage

    client = WickClient("http://localhost:8000")
    result = client.invoke(HumanMessage("Hello!"))
"""

from __future__ import annotations

import itertools
import json
from typing import Any, Iterator, Union
from urllib.parse import urlparse

import requests

from .messages import BaseMessage, Messages

# Type alias for all accepted message input forms.
MessageInput = Union[Messages, BaseMessage, "list[BaseMessage | dict[str, Any]]"]


def _normalize_messages(messages: MessageInput) -> list[dict[str, Any]]:
    """Accept Messages, list[BaseMessage], list[dict], or a single BaseMessage."""
    if isinstance(messages, Messages):
        return messages.to_list()
    if isinstance(messages, BaseMessage):
        return [messages.to_dict()]
    if isinstance(messages, list):
        out: list[dict[str, Any]] = []
        for m in messages:
            if isinstance(m, BaseMessage):
                out.append(m.to_dict())
            elif isinstance(m, dict):
                out.append(m)
            else:
                raise TypeError(f"unsupported message type: {type(m)}")
        return out
    raise TypeError(f"unsupported messages type: {type(messages)}")


class WickClient:
    """HTTP client for the wick_go agent API."""

    def __init__(
        self,
        base_url: str = "http://localhost:8000",
        timeout: int = 10,
        llm_timeout: int = 120,
        auto_start: bool = False,
    ) -> None:
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout
        self.llm_timeout = llm_timeout
        self._server: Any = None  # Optional[WickServer], deferred import

        self._session = requests.Session()

        if auto_start:
            try:
                self.health()
            except Exception:
                from .launcher import WickServer

                parsed = urlparse(self.base_url)
                port = parsed.port or 8000
                self._server = WickServer(port=port)
                self._server.start()
                if not self._server.wait_ready():
                    raise RuntimeError("auto-started server did not become ready")

    def shutdown(self) -> None:
        """Stop the auto-started server, if any, and close the HTTP session."""
        if self._server is not None:
            self._server.stop()
            self._server = None
        self._session.close()

    # -- Health --------------------------------------------------------------

    def health(self) -> dict[str, Any]:
        """GET /health"""
        resp = self._session.get(f"{self.base_url}/health", timeout=self.timeout)
        resp.raise_for_status()
        return resp.json()

    # -- Agent CRUD ----------------------------------------------------------

    def list_agents(self) -> list[dict[str, Any]]:
        """GET /agents/"""
        resp = self._session.get(f"{self.base_url}/agents/", timeout=self.timeout)
        resp.raise_for_status()
        return resp.json()

    def get_agent(self, agent_id: str) -> dict[str, Any]:
        """GET /agents/{agent_id}"""
        resp = self._session.get(
            f"{self.base_url}/agents/{agent_id}", timeout=self.timeout
        )
        resp.raise_for_status()
        return resp.json()

    def create_agent(
        self,
        agent_id: str,
        name: str = "",
        model: Any = "",
        system_prompt: str = "",
        tools: list[str] | None = None,
        middleware: list[str] | None = None,
        subagents: list[dict[str, Any]] | None = None,
        backend: dict[str, Any] | None = None,
        skills: dict[str, Any] | None = None,
        memory: dict[str, Any] | None = None,
        debug: bool = False,
        context_window: int = 0,
        builtin_config: dict[str, str] | None = None,
    ) -> dict[str, Any]:
        """POST /agents/"""
        body: dict[str, Any] = {"agent_id": agent_id}
        if name:
            body["name"] = name
        if model:
            body["model"] = model
        if system_prompt:
            body["system_prompt"] = system_prompt
        if tools is not None:
            body["tools"] = tools
        if middleware is not None:
            body["middleware"] = middleware
        if subagents is not None:
            body["subagents"] = subagents
        if backend is not None:
            body["backend"] = backend
        if skills is not None:
            body["skills"] = skills
        if memory is not None:
            body["memory"] = memory
        if debug:
            body["debug"] = debug
        if context_window > 0:
            body["context_window"] = context_window
        if builtin_config is not None:
            body["builtin_config"] = builtin_config
        resp = self._session.post(
            f"{self.base_url}/agents/", json=body, timeout=self.timeout
        )
        resp.raise_for_status()
        return resp.json()

    def delete_agent(self, agent_id: str) -> None:
        """DELETE /agents/{agent_id}"""
        resp = self._session.delete(
            f"{self.base_url}/agents/{agent_id}", timeout=self.timeout
        )
        resp.raise_for_status()

    def available_tools(self) -> list[str]:
        """GET /agents/tools/available"""
        resp = self._session.get(
            f"{self.base_url}/agents/tools/available", timeout=self.timeout
        )
        resp.raise_for_status()
        return resp.json()["tools"]

    # -- External Tool Registration -------------------------------------------

    def register_tool(
        self,
        name: str,
        description: str,
        parameters: dict[str, Any],
        callback_url: str,
    ) -> dict[str, Any]:
        """POST /agents/tools/register — register an external HTTP callback tool."""
        body = {
            "name": name,
            "description": description,
            "parameters": parameters,
            "callback_url": callback_url,
        }
        resp = self._session.post(
            f"{self.base_url}/agents/tools/register",
            json=body,
            timeout=self.timeout,
        )
        resp.raise_for_status()
        return resp.json()

    def deregister_tool(self, name: str) -> dict[str, Any]:
        """DELETE /agents/tools/deregister/{name} — remove an external tool."""
        resp = self._session.delete(
            f"{self.base_url}/agents/tools/deregister/{name}",
            timeout=self.timeout,
        )
        resp.raise_for_status()
        return resp.json()

    # -- Flow & Hooks --------------------------------------------------------

    def get_flow(self, agent_id: str) -> dict[str, Any]:
        """GET /agents/{agent_id}/flow — returns loop structure + hooks."""
        resp = self._session.get(
            f"{self.base_url}/agents/{agent_id}/flow", timeout=self.timeout
        )
        resp.raise_for_status()
        return resp.json()

    def get_hooks(self, agent_id: str) -> list[str]:
        """Returns the active hook names for an agent."""
        info = self.get_agent(agent_id)
        return info.get("hooks", [])

    def update_hooks(
        self,
        agent_id: str,
        *,
        add: list[str] | None = None,
        remove: list[str] | None = None,
        config: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        """PATCH /agents/{agent_id}/hooks — add/remove hooks."""
        body: dict[str, Any] = {}
        if add:
            body["add"] = add
        if remove:
            body["remove"] = remove
        if config:
            body["config"] = config
        resp = self._session.patch(
            f"{self.base_url}/agents/{agent_id}/hooks",
            json=body,
            timeout=self.timeout,
        )
        resp.raise_for_status()
        return resp.json()

    def print_flow(self, agent_id: str) -> None:
        """Fetch the flow for an agent and print an ASCII diagram."""
        from .flow import print_flow

        flow_data = self.get_flow(agent_id)
        print_flow(flow_data)

    # -- Invoke / Stream -----------------------------------------------------

    def invoke(
        self,
        messages: MessageInput,
        agent_id: str | None = None,
        thread_id: str | None = None,
    ) -> dict[str, Any]:
        """POST /agents/invoke (or /agents/{agent_id}/invoke).

        ``messages`` can be a Messages chain, list[BaseMessage], list[dict],
        or a single BaseMessage.
        """
        msgs = _normalize_messages(messages)

        body: dict[str, Any] = {"messages": msgs}
        if thread_id is not None:
            body["thread_id"] = thread_id

        url = (
            f"{self.base_url}/agents/{agent_id}/invoke"
            if agent_id
            else f"{self.base_url}/agents/invoke"
        )
        resp = self._session.post(url, json=body, timeout=self.llm_timeout)
        resp.raise_for_status()
        return resp.json()

    def stream(
        self,
        messages: MessageInput,
        agent_id: str | None = None,
        thread_id: str | None = None,
    ) -> Iterator[dict[str, Any]]:
        """POST /agents/stream (or /agents/{agent_id}/stream).

        Yields parsed SSE events as dicts with ``event`` and ``data`` keys.
        """
        msgs = _normalize_messages(messages)

        body: dict[str, Any] = {"messages": msgs}
        if thread_id is not None:
            body["thread_id"] = thread_id

        url = (
            f"{self.base_url}/agents/{agent_id}/stream"
            if agent_id
            else f"{self.base_url}/agents/stream"
        )
        resp = self._session.post(
            url,
            json=body,
            headers={"Accept": "text/event-stream"},
            stream=True,
            timeout=self.llm_timeout,
        )
        resp.raise_for_status()

        yield from _parse_sse(resp)

    # -- Traces --------------------------------------------------------------

    def get_trace(self, trace_id: str) -> dict[str, Any]:
        """GET /agents/traces/{trace_id}"""
        resp = self._session.get(
            f"{self.base_url}/agents/traces/{trace_id}", timeout=self.timeout
        )
        resp.raise_for_status()
        return resp.json()

    def list_traces(self, limit: int = 50) -> list[dict[str, Any]]:
        """GET /agents/traces?limit=N"""
        resp = self._session.get(
            f"{self.base_url}/agents/traces",
            params={"limit": limit},
            timeout=self.timeout,
        )
        resp.raise_for_status()
        return resp.json()

    # -- Raw request (escape hatch) ------------------------------------------

    def raw_post(self, path: str, **kwargs: Any) -> requests.Response:
        """Send a raw POST to ``base_url + path``."""
        kwargs.setdefault("timeout", self.timeout)
        return self._session.post(f"{self.base_url}{path}", **kwargs)

    def raw_get(self, path: str, **kwargs: Any) -> requests.Response:
        """Send a raw GET to ``base_url + path``."""
        kwargs.setdefault("timeout", self.timeout)
        return self._session.get(f"{self.base_url}{path}", **kwargs)


# ---------------------------------------------------------------------------
# SSE parser
# ---------------------------------------------------------------------------


def _flush_event(
    event_type: str | None, data_lines: list[str]
) -> dict[str, Any] | None:
    """Build an SSE event dict from accumulated lines, or None if empty."""
    if not data_lines:
        return None
    raw = "\n".join(data_lines)
    try:
        data: Any = json.loads(raw)
    except json.JSONDecodeError:
        data = raw
    return {"event": event_type, "data": data}


def _parse_sse(response: requests.Response) -> Iterator[dict[str, Any]]:
    """Parse SSE events from a streaming response."""
    event_type: str | None = None
    data_lines: list[str] = []

    # Chain a sentinel empty string to flush the final event without duplication.
    lines = itertools.chain(response.iter_lines(decode_unicode=True), [""])

    for line in lines:
        if line is None:
            continue

        if line.startswith("event:"):
            event_type = line[6:].strip()
        elif line.startswith("data:"):
            data_lines.append(line[5:].strip())
        elif line == "":
            event = _flush_event(event_type, data_lines)
            if event is not None:
                yield event
            event_type = None
            data_lines = []
