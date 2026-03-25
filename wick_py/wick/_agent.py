"""Agent class — the main user-facing API.

Usage:
    from wick import Agent, StreamChunk

    agent = Agent("my-agent", system_prompt="You are helpful.")

    @agent.tool(description="Add two numbers")
    def add(a: float, b: float) -> str:
        return str(a + b)

    @agent.llm_provider("my-model")
    async def my_llm(request):
        yield StreamChunk(delta="Hello!")
        yield StreamChunk(done=True)

    agent.run()
"""

from __future__ import annotations

import inspect
import json
import logging
import os
import signal
import threading
from collections.abc import Callable
from typing import Any

import uvicorn

from ._client import WickClient
from ._runtime import GoRuntime
from ._sidecar import build_app
from ._tools import get_tool as _get_global_tool
from ._types import (
    BackendConfig,
    MemoryConfig,
    SkillsConfig,
    SubAgentConfig,
)

logger = logging.getLogger("wick")


class _ToolDef:
    """Internal tool definition."""

    __slots__ = ("name", "description", "parameters", "fn")

    def __init__(
        self,
        name: str,
        description: str,
        parameters: dict[str, Any],
        fn: Callable,
    ) -> None:
        self.name = name
        self.description = description
        self.parameters = parameters
        self.fn = fn


def _to_subagent_dict(s: Any) -> dict[str, Any]:
    """Convert an Agent, SubAgentConfig, or dict to a subagent config dict."""
    if isinstance(s, dict):
        return s
    # Agent instance → extract SubAgentCfg fields
    if hasattr(s, "agent_id") and hasattr(s, "system_prompt"):
        d: dict[str, Any] = {
            "name": s.agent_id,
            "description": getattr(s, "name", "") or s.agent_id,
            "system_prompt": s.system_prompt,
        }
        if hasattr(s, "builtin_tools") and s.builtin_tools:
            d["tools"] = s.builtin_tools
        if hasattr(s, "_model") and s._model:
            model = s._model
            d["model"] = model if isinstance(model, str) else json.dumps(model)
        return d
    # SubAgentConfig (Pydantic)
    return s.model_dump()


class Agent:
    """Define and run a wick agent with Python tools and LLM providers."""

    def __init__(
        self,
        agent_id: str,
        *,
        name: str = "",
        model: str | dict[str, Any] | None = None,
        system_prompt: str = "",
        builtin_tools: list[str] | None = None,
        backend: BackendConfig | dict[str, Any] | None = None,
        skills: SkillsConfig | dict[str, Any] | None = None,
        memory: MemoryConfig | dict[str, Any] | None = None,
        subagents: list["Agent | SubAgentConfig | dict[str, Any]"] | None = None,
        debug: bool = False,
        context_window: int = 0,
    ) -> None:
        self.agent_id = agent_id
        self.name = name or agent_id
        self._model = model
        self.system_prompt = system_prompt
        self.builtin_tools = builtin_tools or []
        self.debug = debug
        self.context_window = context_window

        # Config objects
        self._backend = (
            backend if isinstance(backend, dict) else
            backend.model_dump() if backend else None
        )
        self._skills = (
            skills if isinstance(skills, dict) else
            skills.model_dump() if skills else None
        )
        self._memory = (
            memory if isinstance(memory, dict) else
            memory.model_dump() if memory else None
        )
        self._subagents = [
            _to_subagent_dict(s) for s in (subagents or [])
        ]

        # Registries (populated by decorators)
        self._tools: dict[str, _ToolDef] = {}
        self._llm_providers: dict[str, Callable] = {}

    # ── Decorators ──────────────────────────────────────────────────────

    def tool(
        self,
        name: str | None = None,
        description: str = "",
        parameters: dict[str, Any] | None = None,
    ) -> Callable:
        """Decorator to register a Python function as a tool.

        Usage:
            @agent.tool(description="Add two numbers")
            def add(a: float, b: float) -> str:
                return str(a + b)
        """
        def decorator(fn: Callable) -> Callable:
            tool_name = name or fn.__name__
            tool_desc = description or fn.__doc__ or ""
            tool_params = parameters or _infer_parameters(fn)
            self._tools[tool_name] = _ToolDef(tool_name, tool_desc, tool_params, fn)
            return fn
        return decorator

    def llm_provider(self, model_name: str | None = None) -> Callable:
        """Decorator to register a custom LLM handler.

        The handler can be:
          - Async generator yielding StreamChunk objects (for streaming)
          - Async function returning LLMResponse (for sync calls)

        Usage:
            @agent.llm_provider("my-model")
            async def handler(request: LLMRequest) -> AsyncIterator[StreamChunk]:
                yield StreamChunk(delta="Hello!")
                yield StreamChunk(done=True)
        """
        def decorator(fn: Callable) -> Callable:
            key = model_name or fn.__name__
            self._llm_providers[key] = fn
            return fn
        return decorator

    # ── Run modes ───────────────────────────────────────────────────────

    def run(
        self,
        *,
        go_binary: str | None = None,
        go_port: int = 8000,
        sidecar_port: int = 9100,
        sidecar_host: str = "127.0.0.1",
        ui: bool = True,
        extra_agents: list["Agent"] | None = None,
    ) -> None:
        """Dev mode: start Go binary + sidecar, register agent, block until SIGINT.

        Args:
            go_binary: path to wick_server binary (auto-discovered if None)
            go_port: port for the Go server
            sidecar_port: port for the Python sidecar (only if tools/LLM registered)
            sidecar_host: host for the sidecar
            ui: serve the bundled UI (default True)
            extra_agents: additional Agent instances to register with the same server
        """
        all_agents = [self] + (extra_agents or [])

        # Check if any agent needs a sidecar (has Python tools or LLM providers)
        needs_sidecar = any(
            bool(a._tools or a._resolve_builtin_tools() or a._llm_providers)
            for a in all_agents
        )
        sidecar_url = f"http://{sidecar_host}:{sidecar_port}"

        # Start sidecar if needed — serves tools from all agents
        sidecar_thread = None
        if needs_sidecar:
            sidecar_thread = self._start_sidecar(sidecar_host, sidecar_port, all_agents)

        # Resolve cwd for Go binary so it finds static/ for UI serving
        go_cwd = None
        if ui:
            go_cwd = self._find_static_dir()

        # Start Go binary — bind to 0.0.0.0 when running inside Docker
        go_host = os.environ.get("WICK_GO_HOST", "127.0.0.1")
        runtime = GoRuntime(binary=go_binary, port=go_port, host=go_host, cwd=go_cwd)
        runtime.start()
        try:
            runtime.wait_ready()
        except Exception:
            runtime.stop()
            raise

        # Register all agents and their tools
        try:
            client = WickClient(runtime.base_url)
            for a in all_agents:
                a_needs_sidecar = bool(a._tools or a._resolve_builtin_tools() or a._llm_providers)
                a._register(client, sidecar_url if a_needs_sidecar else None)
                print(f"  registered agent '{a.agent_id}'")

            logger.info("All agents ready at %s", runtime.base_url)
            print(f"\n  wick server running at {runtime.base_url}\n")

            # Block until SIGINT/SIGTERM
            stop = threading.Event()
            signal.signal(signal.SIGINT, lambda *_: stop.set())
            signal.signal(signal.SIGTERM, lambda *_: stop.set())
            stop.wait()
        finally:
            client.close()
            runtime.stop()
            print("\nStopped.")

    def serve_sidecar(
        self,
        *,
        port: int = 9100,
        host: str = "0.0.0.0",
        go_url: str = "http://localhost:8000",
    ) -> None:
        """Production mode: start only the sidecar and register with an existing Go server.

        Args:
            port: sidecar port
            host: sidecar host
            go_url: URL of the running Go server
        """
        sidecar_url = f"http://127.0.0.1:{port}"

        # Register with Go server
        client = WickClient(go_url)
        client.wait_ready()
        self._register(client, sidecar_url)
        client.close()

        logger.info("Sidecar registered with Go server at %s", go_url)
        print(f"\n  wick sidecar serving at {host}:{port}")
        print(f"  registered with Go server at {go_url}\n")

        # Run sidecar (blocks)
        app = build_app(
            tools=self._all_tool_fns(),
            llm_providers=self._llm_providers,
        )
        uvicorn.run(app, host=host, port=port, log_level="info")

    # ── Internal ────────────────────────────────────────────────────────

    @staticmethod
    def _find_static_dir() -> str | None:
        """Find the directory containing static/ for UI serving."""
        import pathlib

        # Check relative to this package (wick_py/wick/_agent.py → wick_py/)
        pkg_dir = pathlib.Path(__file__).resolve().parent.parent
        static = pkg_dir / "static"
        if static.is_dir():
            return str(pkg_dir)

        # Check CWD
        cwd_static = pathlib.Path.cwd() / "static"
        if cwd_static.is_dir():
            return str(pathlib.Path.cwd())

        return None

    def _all_tool_fns(self) -> dict[str, Callable]:
        """Merge builtin + @agent.tool functions for the sidecar."""
        fns: dict[str, Callable] = {}
        for name, td in self._resolve_builtin_tools().items():
            fns[name] = td.fn
        for name, td in self._tools.items():
            fns[name] = td.fn  # @agent.tool overrides globals
        return fns

    def _start_sidecar(self, host: str, port: int, all_agents: list["Agent"] | None = None) -> threading.Thread:
        """Start the FastAPI sidecar in a background thread."""
        # Merge tools and LLM providers from all agents
        agents = all_agents or [self]
        merged_tools: dict[str, Callable] = {}
        merged_llm: dict[str, Callable] = {}
        for a in agents:
            merged_tools.update(a._all_tool_fns())
            merged_llm.update(a._llm_providers)
        app = build_app(
            tools=merged_tools,
            llm_providers=merged_llm,
        )
        config = uvicorn.Config(app, host=host, port=port, log_level="warning")
        server = uvicorn.Server(config)

        thread = threading.Thread(target=server.run, daemon=True)
        thread.start()

        # Wait for sidecar to be ready
        import time
        import httpx
        deadline = time.monotonic() + 10.0
        while time.monotonic() < deadline:
            try:
                resp = httpx.get(f"http://{host}:{port}/health", timeout=1.0)
                if resp.status_code == 200:
                    logger.info("Sidecar ready at %s:%d", host, port)
                    return thread
            except (httpx.ConnectError, httpx.ReadTimeout):
                time.sleep(0.2)

        raise TimeoutError(f"Sidecar not ready after 10s at {host}:{port}")

    def _resolve_builtin_tools(self) -> dict[str, _ToolDef]:
        """Resolve builtin_tools names to ToolDefs from the global registry."""
        resolved: dict[str, _ToolDef] = {}
        for name in self.builtin_tools:
            td = _get_global_tool(name)
            if td is None:
                logger.warning("builtin_tools: '%s' not found in global tool registry — skipped", name)
                continue
            resolved[name] = _ToolDef(td.name, td.description, td.parameters, td.fn)
        return resolved

    def _register(self, client: WickClient, sidecar_url: str | None) -> None:
        """Register the agent and tools with the Go server."""
        # Build model config
        model_config = self._model
        if self._llm_providers and sidecar_url:
            # Use first registered provider as the model
            model_name = next(iter(self._llm_providers))
            model_config = {
                "provider": "proxy",
                "model": model_name,
                "callback_url": sidecar_url,
            }

        # Register agent
        agent_config: dict[str, Any] = {
            "name": self.name,
            "system_prompt": self.system_prompt,
            "debug": self.debug,
        }
        if model_config:
            agent_config["model"] = model_config
        if self._backend:
            agent_config["backend"] = self._backend
        if self._skills:
            agent_config["skills"] = self._skills
        if self._memory:
            agent_config["memory"] = self._memory
        if self._subagents:
            agent_config["subagents"] = self._subagents
        if self.context_window:
            agent_config["context_window"] = self.context_window

        result = client.register_agent(self.agent_id, agent_config)
        logger.info("Agent registered: %s", result)

        # Merge all tools: @agent.tool + builtin_tools from global registry
        all_tools: dict[str, _ToolDef] = {}
        all_tools.update(self._resolve_builtin_tools())
        all_tools.update(self._tools)  # @agent.tool overrides globals if name clashes

        # Register tools as HTTPTools (scoped to this agent)
        if all_tools and sidecar_url:
            for td in all_tools.values():
                result = client.register_tool(
                    name=td.name,
                    description=td.description,
                    parameters=td.parameters,
                    callback_url=sidecar_url,
                    agent_id=self.agent_id,
                )
                logger.info("Tool registered: %s", result)

    def _build_agent_config(self) -> dict[str, Any]:
        """Build the full agent config dict for registration."""
        return {
            "name": self.name,
            "model": self._model,
            "system_prompt": self.system_prompt,
            "tools": self.builtin_tools,
            "debug": self.debug,
        }


def _infer_parameters(fn: Callable) -> dict[str, Any]:
    """Infer JSON Schema parameters from function type hints."""
    sig = inspect.signature(fn)
    properties: dict[str, Any] = {}
    required: list[str] = []

    type_map = {
        str: "string",
        int: "integer",
        float: "number",
        bool: "boolean",
    }

    for name, param in sig.parameters.items():
        annotation = param.annotation
        json_type = type_map.get(annotation, "string")
        properties[name] = {"type": json_type}

        if param.default is inspect.Parameter.empty:
            required.append(name)

    schema: dict[str, Any] = {
        "type": "object",
        "properties": properties,
    }
    if required:
        schema["required"] = required

    return schema
