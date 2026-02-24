"""External tool registration — @tool decorator and HTTP sidecar server.

Usage::

    from wick_deep_agent import WickServer, tool

    @tool(description="Add two numbers")
    def add(a: int, b: int) -> str:
        return str(a + b)

    server = WickServer(port=8000, tools=[add], agents={...})
    with server:
        ...  # LLM can now call add() via HTTP callback
"""

from __future__ import annotations

import functools
import inspect
import json
import logging
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from typing import Any, Callable

logger = logging.getLogger(__name__)

# Python type → JSON Schema type
_TYPE_MAP: dict[type, str] = {
    str: "string",
    int: "integer",
    float: "number",
    bool: "boolean",
}


class ToolDef:
    """Wraps a Python function with tool metadata for registration with the Go server."""

    def __init__(
        self,
        fn: Callable[..., str],
        name: str | None = None,
        description: str = "",
    ) -> None:
        self.fn = fn
        self.name = name or fn.__name__
        self.description = description or fn.__doc__ or ""
        self.parameters = self._build_parameters()
        functools.update_wrapper(self, fn)

    def __call__(self, *args: Any, **kwargs: Any) -> str:
        return self.fn(*args, **kwargs)

    def _build_parameters(self) -> dict[str, Any]:
        """Extract JSON Schema from function type hints."""
        sig = inspect.signature(self.fn)
        hints = self.fn.__annotations__ if hasattr(self.fn, "__annotations__") else {}

        properties: dict[str, Any] = {}
        required: list[str] = []

        for param_name, param in sig.parameters.items():
            if param_name == "return":
                continue

            prop: dict[str, Any] = {}
            hint = hints.get(param_name)
            if hint in _TYPE_MAP:
                prop["type"] = _TYPE_MAP[hint]
            else:
                prop["type"] = "string"

            properties[param_name] = prop

            if param.default is inspect.Parameter.empty:
                required.append(param_name)

        schema: dict[str, Any] = {
            "type": "object",
            "properties": properties,
        }
        if required:
            schema["required"] = required
        return schema

    def to_schema(self, callback_url: str) -> dict[str, Any]:
        """Return the registration payload for POST /agents/tools/register."""
        return {
            "name": self.name,
            "description": self.description,
            "parameters": self.parameters,
            "callback_url": callback_url,
        }


def tool(
    fn: Callable[..., str] | None = None,
    *,
    description: str = "",
    name: str | None = None,
) -> ToolDef | Callable[[Callable[..., str]], ToolDef]:
    """Decorator to define an external tool.

    Can be used as ``@tool`` or ``@tool(description="...", name="...")``.
    """
    if fn is not None:
        # Called as @tool (no arguments)
        return ToolDef(fn, name=name, description=description)

    # Called as @tool(description="...", ...)
    def wrapper(f: Callable[..., str]) -> ToolDef:
        return ToolDef(f, name=name, description=description)

    return wrapper


class _ToolHandler(BaseHTTPRequestHandler):
    """HTTP handler for tool and model execution requests from the Go server."""

    # Set by ToolServer before binding
    tool_registry: dict[str, ToolDef] = {}
    model_registry: dict[str, Any] = {}  # str -> ModelDef

    def do_POST(self) -> None:  # noqa: N802
        parts = self.path.strip("/").split("/")

        # Route: /llm/{model_name}/call or /llm/{model_name}/stream
        if len(parts) >= 3 and parts[0] == "llm":
            model_name = parts[1]
            action = parts[2]
            self._handle_llm(model_name, action)
            return

        # Route: /tools/{name}
        if len(parts) < 2 or parts[0] != "tools":
            self._respond(404, {"error": f"not found: {self.path}"})
            return

        tool_name = parts[1]
        tool_fn = self.tool_registry.get(tool_name)
        if tool_fn is None:
            self._respond(404, {"error": f"tool not found: {tool_name}"})
            return

        # Read request body
        content_length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(content_length)

        try:
            payload = json.loads(body) if body else {}
        except json.JSONDecodeError as e:
            self._respond(400, {"error": f"invalid JSON: {e}"})
            return

        args = payload.get("args", {})

        # Coerce arg types based on function hints
        sig = inspect.signature(tool_fn.fn)
        hints = tool_fn.fn.__annotations__ if hasattr(tool_fn.fn, "__annotations__") else {}
        coerced: dict[str, Any] = {}
        for param_name, param in sig.parameters.items():
            if param_name in args:
                val = args[param_name]
                expected = hints.get(param_name)
                if expected is int and isinstance(val, (str, float)):
                    val = int(val)
                elif expected is float and isinstance(val, (str, int)):
                    val = float(val)
                elif expected is bool and isinstance(val, str):
                    val = val.lower() in ("true", "1", "yes")
                coerced[param_name] = val
            elif param.default is not inspect.Parameter.empty:
                pass  # let Python use the default

        try:
            result = tool_fn.fn(**coerced)
            self._respond(200, {"result": str(result)})
        except Exception as e:
            logger.exception("Tool %s failed", tool_name)
            self._respond(200, {"error": str(e)})

    def _handle_llm(self, model_name: str, action: str) -> None:
        """Handle /llm/{model_name}/call or /llm/{model_name}/stream."""
        model_def = self.model_registry.get(model_name)
        if model_def is None:
            self._respond(404, {"error": f"model not found: {model_name}"})
            return

        # Read request body
        content_length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(content_length)
        try:
            request = json.loads(body) if body else {}
        except json.JSONDecodeError as e:
            self._respond(400, {"error": f"invalid JSON: {e}"})
            return

        if action == "call":
            try:
                result = model_def.call(request)
                self._respond(200, result)
            except Exception as e:
                logger.exception("Model %s call failed", model_name)
                self._respond(500, {"error": str(e)})

        elif action == "stream":
            if not model_def.has_stream:
                self._respond(400, {"error": f"model {model_name!r} does not support streaming"})
                return
            try:
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.send_header("Cache-Control", "no-cache")
                self.send_header("Connection", "close")
                self.end_headers()

                for chunk in model_def.stream(request):
                    event_data = json.dumps(chunk)
                    self.wfile.write(f"data: {event_data}\n\n".encode())
                    self.wfile.flush()
            except Exception as e:
                logger.exception("Model %s stream failed", model_name)
                # Try to send error as final SSE event
                try:
                    err_data = json.dumps({"error": str(e), "done": True})
                    self.wfile.write(f"data: {err_data}\n\n".encode())
                    self.wfile.flush()
                except Exception:
                    pass
        else:
            self._respond(404, {"error": f"unknown action: {action}"})

    def _respond(self, status: int, body: dict[str, Any]) -> None:
        data = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, format: str, *args: Any) -> None:
        # Suppress default stderr logging
        logger.debug(format, *args)


class ToolServer:
    """Lightweight HTTP server that handles tool and model callbacks from the Go agent."""

    def __init__(
        self,
        tools: list[ToolDef] | None = None,
        models: list | None = None,
        host: str = "127.0.0.1",
        port: int = 0,
    ) -> None:
        self._tools = {t.name: t for t in (tools or [])}
        self._models = {m.name: m for m in (models or [])}
        self._host = host
        self._port = port
        self._server: HTTPServer | None = None
        self._thread: threading.Thread | None = None

    @property
    def port(self) -> int:
        if self._server is not None:
            return self._server.server_address[1]
        return self._port

    @property
    def callback_url(self) -> str:
        return f"http://{self._host}:{self.port}"

    @property
    def is_alive(self) -> bool:
        return self._thread is not None and self._thread.is_alive()

    def start(self) -> None:
        """Start the tool server in a daemon thread."""
        if self.is_alive:
            return

        # Create a handler class with registries bound
        tool_reg = self._tools
        model_reg = self._models

        class Handler(_ToolHandler):
            tool_registry = tool_reg
            model_registry = model_reg

        self._server = HTTPServer((self._host, self._port), Handler)
        self._thread = threading.Thread(
            target=self._server.serve_forever,
            daemon=True,
            name="wick-tool-server",
        )
        self._thread.start()
        logger.info(
            "ToolServer started on %s (tools: %s, models: %s)",
            self.callback_url,
            list(self._tools.keys()),
            list(self._models.keys()),
        )

    def stop(self) -> None:
        """Shut down the tool server."""
        if self._server is not None:
            self._server.shutdown()
            self._server = None
        if self._thread is not None:
            self._thread.join(timeout=5)
            self._thread = None

    @property
    def tool_defs(self) -> list[ToolDef]:
        return list(self._tools.values())
