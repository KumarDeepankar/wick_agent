"""Deep Agent factory and registry.

Manages creation, caching, and invocation of deep agents
built on the `deepagents` library (LangChain + LangGraph).

Supports every customization knob from the deep-agents docs:
  model, tools, system_prompt, middleware, subagents, backend,
  store, interrupt_on, checkpointer, skills, memory,
  response_format, cache, debug, name
"""

from __future__ import annotations

import logging
import time
import uuid
from typing import Any, Callable

from deepagents import create_deep_agent
from langgraph.checkpoint.memory import MemorySaver
from langgraph.store.memory import InMemoryStore
from pydantic import create_model

from app.agents.model_resolver import resolve_model
from app.agents.tracing import AgentTrace
from app.config import settings

logger = logging.getLogger(__name__)

# ═══════════════════════════════════════════════════════════════════════════
# Tool registry
# ═══════════════════════════════════════════════════════════════════════════

_TOOL_REGISTRY: dict[str, Callable] = {}


def register_tool(name: str, func: Callable) -> None:
    _TOOL_REGISTRY[name] = func


def get_tool(name: str) -> Callable:
    tool = _TOOL_REGISTRY.get(name)
    if tool is None:
        raise KeyError(f"Tool '{name}' not registered. Available: {list(_TOOL_REGISTRY.keys())}")
    return tool


def list_tools() -> list[str]:
    return list(_TOOL_REGISTRY.keys())


# ═══════════════════════════════════════════════════════════════════════════
# Middleware registry
# ═══════════════════════════════════════════════════════════════════════════

_MIDDLEWARE_REGISTRY: dict[str, Any] = {}


def register_middleware(name: str, mw: Any) -> None:
    """Register a middleware callable (decorated with @wrap_tool_call)."""
    _MIDDLEWARE_REGISTRY[name] = mw


def get_middleware(name: str) -> Any:
    mw = _MIDDLEWARE_REGISTRY.get(name)
    if mw is None:
        raise KeyError(f"Middleware '{name}' not registered. Available: {list(_MIDDLEWARE_REGISTRY.keys())}")
    return mw


def list_middleware() -> list[str]:
    return list(_MIDDLEWARE_REGISTRY.keys())


# ═══════════════════════════════════════════════════════════════════════════
# Backend helpers
# ═══════════════════════════════════════════════════════════════════════════


def _build_backend(
    backend_cfg: dict[str, Any] | None,
) -> tuple[Any | None, Any | None]:
    """Resolve a BackendConfig dict into (backend, store) arguments.

    Returns (backend_arg, store_arg) suitable for create_deep_agent().
    """
    if backend_cfg is None:
        return None, None

    btype = backend_cfg.get("type", "state")

    if btype == "state":
        return None, None  # StateBackend is the default

    if btype == "filesystem":
        from deepagents.backends import FilesystemBackend

        return FilesystemBackend(
            root_dir=backend_cfg.get("root_dir", "."),
            virtual_mode=backend_cfg.get("virtual_mode", True),
        ), None

    if btype == "store":
        from deepagents.backends import StoreBackend

        store = InMemoryStore()
        return (lambda rt: StoreBackend(rt)), store

    if btype == "composite":
        from deepagents.backends import CompositeBackend, StateBackend, StoreBackend

        routes_cfg = backend_cfg.get("routes") or {}
        store = InMemoryStore()

        def _composite_factory(rt: Any) -> CompositeBackend:
            route_map: dict[str, Any] = {}
            for prefix, sub_type in routes_cfg.items():
                if sub_type == "store":
                    route_map[prefix] = StoreBackend(rt)
                elif sub_type == "state":
                    route_map[prefix] = StateBackend(rt)
            return CompositeBackend(
                default=StateBackend(rt),
                routes=route_map,
            )

        return _composite_factory, store

    if btype == "docker":
        from app.agents.docker_backend import DockerSandboxBackend

        return DockerSandboxBackend(
            container_name=backend_cfg.get("container_name", "wick-skills-sandbox"),
            workdir=backend_cfg.get("workdir", "/workspace"),
            timeout=backend_cfg.get("timeout", 120.0),
            max_output_bytes=backend_cfg.get("max_output_bytes", 100_000),
        ), None

    return None, None


# ═══════════════════════════════════════════════════════════════════════════
# Response format helpers
# ═══════════════════════════════════════════════════════════════════════════

_TYPE_MAP: dict[str, type] = {
    "string": str,
    "number": float,
    "integer": int,
    "boolean": bool,
    "array": list,
    "object": dict,
}


def _build_response_format(
    rf_cfg: dict[str, Any] | None,
) -> type | None:
    """Build a Pydantic model from a ResponseFormatConfig dict."""
    if rf_cfg is None:
        return None

    fields_spec: dict[str, Any] = {}
    for field in rf_cfg.get("fields", []):
        py_type = _TYPE_MAP.get(field.get("type", "string"), str)
        default = ... if field.get("required", True) else None
        if not field.get("required", True):
            py_type = py_type | None  # type: ignore[operator]
        fields_spec[field["name"]] = (
            py_type,
            default,
        )

    return create_model(rf_cfg.get("name", "StructuredResponse"), **fields_spec)


# ═══════════════════════════════════════════════════════════════════════════
# Interrupt helpers
# ═══════════════════════════════════════════════════════════════════════════


def _build_interrupt_on(
    interrupt_cfg: dict[str, Any] | None,
) -> dict[str, bool | dict[str, Any]] | None:
    """Normalize the interrupt_on config from the API schema."""
    if not interrupt_cfg:
        return None

    result: dict[str, bool | dict[str, Any]] = {}
    for tool_name, rule in interrupt_cfg.items():
        if isinstance(rule, bool):
            result[tool_name] = rule
        elif isinstance(rule, dict):
            enabled = rule.get("enabled", True)
            if not enabled:
                result[tool_name] = False
            elif rule.get("allowed_decisions"):
                result[tool_name] = {"allowed_decisions": rule["allowed_decisions"]}
            else:
                result[tool_name] = True
        else:
            result[tool_name] = True
    return result


# ═══════════════════════════════════════════════════════════════════════════
# Cache helpers
# ═══════════════════════════════════════════════════════════════════════════


def _build_cache(cache_cfg: dict[str, Any] | None) -> Any | None:
    """Build an LLM cache object from CacheConfig."""
    if not cache_cfg or not cache_cfg.get("enabled"):
        return None

    cache_type = cache_cfg.get("type", "in_memory")

    if cache_type == "in_memory":
        from langchain_core.caches import InMemoryCache

        return InMemoryCache()

    if cache_type == "sqlite":
        from langchain_community.cache import SQLiteCache

        return SQLiteCache(database_path=".langchain_cache.db")

    # Fallback to in-memory
    from langchain_core.caches import InMemoryCache

    return InMemoryCache()


# ═══════════════════════════════════════════════════════════════════════════
# Agent registry
# ═══════════════════════════════════════════════════════════════════════════

_AGENT_REGISTRY: dict[str, dict[str, Any]] = {}

# Shared checkpointer for all agents
_checkpointer = MemorySaver()


def create_deep_agent_from_config(
    agent_id: str,
    *,
    # Identity
    name: str | None = None,
    # Model — string ("ollama:llama3.1:8b") or dict with full config
    model: str | dict[str, Any] | None = None,
    # Prompt
    system_prompt: str | None = None,
    # Tools
    tool_names: list[str] | None = None,
    # Middleware
    middleware_names: list[str] | None = None,
    # Subagents
    subagents: list[dict[str, Any]] | None = None,
    # Backend
    backend_cfg: dict[str, Any] | None = None,
    # Human-in-the-loop
    interrupt_on_cfg: dict[str, Any] | None = None,
    # Skills
    skills_cfg: dict[str, Any] | None = None,
    # Memory
    memory_cfg: dict[str, Any] | None = None,
    # Structured output
    response_format_cfg: dict[str, Any] | None = None,
    # Cache
    cache_cfg: dict[str, Any] | None = None,
    # Debug
    debug: bool = False,
) -> dict[str, Any]:
    """Create and register a deep agent with all available knobs.

    Returns metadata dict about the created agent.
    """
    model_input = model or settings.default_model
    resolved_model = resolve_model(model_input)
    # Store display string for metadata
    model_str = model_input if isinstance(model_input, str) else (
        f"{model_input.get('provider', '')}:{model_input.get('model', '')}"
    )
    resolved_prompt = system_prompt

    # ── Tools ────────────────────────────────────────────────────────────
    tools: list[Callable] = []
    resolved_tool_names: list[str] = []
    for tname in (tool_names or []):
        tools.append(get_tool(tname))
        resolved_tool_names.append(tname)

    # ── Middleware ────────────────────────────────────────────────────────
    middleware_list: list[Any] = []
    resolved_mw_names: list[str] = []
    for mw_name in (middleware_names or []):
        middleware_list.append(get_middleware(mw_name))
        resolved_mw_names.append(mw_name)

    # ── Subagents ────────────────────────────────────────────────────────
    resolved_subagents = None
    subagent_names: list[str] = []
    if subagents:
        resolved_subagents = []
        for sa in subagents:
            sa_tools = [get_tool(n) for n in (sa.get("tools") or [])]
            sa_mw = [get_middleware(n) for n in (sa.get("middleware") or [])]
            spec: dict[str, Any] = {
                "name": sa["name"],
                "description": sa["description"],
                "system_prompt": sa["system_prompt"],
            }
            if sa_tools:
                spec["tools"] = sa_tools
            if sa.get("model"):
                spec["model"] = sa["model"]
            if sa_mw:
                spec["middleware"] = sa_mw
            resolved_subagents.append(spec)
            subagent_names.append(sa["name"])

    # ── Backend & Store ──────────────────────────────────────────────────
    backend_arg, store_arg = _build_backend(backend_cfg)

    # ── Interrupt (human-in-the-loop) ────────────────────────────────────
    interrupt_on = _build_interrupt_on(interrupt_on_cfg)

    # ── Skills ───────────────────────────────────────────────────────────
    # With FilesystemBackend, the framework handles everything:
    #   1. SkillsMiddleware reads SKILL.md files directly from disk
    #   2. Parses YAML frontmatter (name + description)
    #   3. Injects skill catalog into agent's system prompt
    #   4. Agent calls read_file on-demand (progressive disclosure)
    skills_paths: list[str] | None = None
    raw_skill_dirs = (skills_cfg or {}).get("paths")
    if raw_skill_dirs:
        # Convert disk paths (e.g. "./skills/") to virtual paths (e.g. "/skills/")
        # that the framework's SkillsMiddleware expects.
        skills_paths = [f"/{d.strip('./')}/" for d in raw_skill_dirs]

    # ── Memory ───────────────────────────────────────────────────────────
    memory_paths: list[str] | None = None
    if memory_cfg and memory_cfg.get("paths"):
        memory_paths = memory_cfg["paths"]

    # ── Response format ──────────────────────────────────────────────────
    response_format = _build_response_format(response_format_cfg)

    # ── Cache ────────────────────────────────────────────────────────────
    cache = _build_cache(cache_cfg)

    # ── Create the agent ─────────────────────────────────────────────────
    kwargs: dict[str, Any] = {
        "model": resolved_model,
        "checkpointer": _checkpointer,
        "debug": debug,
    }
    if resolved_prompt:
        kwargs["system_prompt"] = resolved_prompt
    if tools:
        kwargs["tools"] = tools
    if middleware_list:
        kwargs["middleware"] = middleware_list
    if resolved_subagents:
        kwargs["subagents"] = resolved_subagents
    if backend_arg is not None:
        kwargs["backend"] = backend_arg
    if store_arg is not None:
        kwargs["store"] = store_arg
    if interrupt_on:
        kwargs["interrupt_on"] = interrupt_on
    if skills_paths:
        kwargs["skills"] = skills_paths
    if memory_paths:
        kwargs["memory"] = memory_paths
    if response_format:
        kwargs["response_format"] = response_format
    if cache:
        kwargs["cache"] = cache
    if name:
        kwargs["name"] = name

    agent = create_deep_agent(**kwargs)

    # ── Extract tool schemas for tracing ──────────────────────────────────
    tool_schemas = _extract_tool_schemas(tools)

    # ── Store metadata ───────────────────────────────────────────────────
    meta: dict[str, Any] = {
        "agent_id": agent_id,
        "agent": agent,
        "name": name,
        "model": model_str,
        "system_prompt": resolved_prompt,
        "tools": resolved_tool_names,
        "tool_schemas": tool_schemas,
        "middleware": resolved_mw_names,
        "subagents": subagent_names,
        "backend_type": (backend_cfg or {}).get("type", "state"),
        "has_interrupt_on": bool(interrupt_on),
        "interrupt_on": interrupt_on or {},
        "skills": skills_paths or [],
        "memory_paths": memory_paths or [],
        "has_response_format": response_format is not None,
        "response_format_schema": response_format,
        "cache_enabled": cache is not None,
        "debug": debug,
        "_backend": backend_arg,
    }
    _AGENT_REGISTRY[agent_id] = meta
    return meta


def get_or_create_default_agent() -> dict[str, Any]:
    """Return the default agent, creating it on first access."""
    if "default" not in _AGENT_REGISTRY:
        tool_names = list_tools() or None
        create_deep_agent_from_config("default", tool_names=tool_names)
    return _AGENT_REGISTRY["default"]


def get_agent(agent_id: str) -> dict[str, Any]:
    meta = _AGENT_REGISTRY.get(agent_id)
    if meta is None:
        raise KeyError(f"Agent '{agent_id}' not found. Available: {list(_AGENT_REGISTRY.keys())}")
    return meta


def list_agents() -> list[dict[str, Any]]:
    return [
        {
            "agent_id": m["agent_id"],
            "name": m.get("name"),
            "model": m["model"],
            "system_prompt": (
                m["system_prompt"][:120] + "..."
                if m.get("system_prompt") and len(m["system_prompt"]) > 120
                else m.get("system_prompt")
            ),
            "tools": m["tools"],
            "subagents": m["subagents"],
            "middleware": m.get("middleware", []),
            "backend_type": m.get("backend_type", "state"),
            "has_interrupt_on": m.get("has_interrupt_on", False),
            "skills": m.get("skills", []),
            "loaded_skills": m.get("loaded_skills", []),
            "memory": m.get("memory_paths", []),
            "has_response_format": m.get("has_response_format", False),
            "cache_enabled": m.get("cache_enabled", False),
            "debug": m.get("debug", False),
        }
        for m in _AGENT_REGISTRY.values()
    ]


def delete_agent(agent_id: str) -> None:
    if agent_id not in _AGENT_REGISTRY:
        raise KeyError(f"Agent '{agent_id}' not found.")
    del _AGENT_REGISTRY[agent_id]


# ═══════════════════════════════════════════════════════════════════════════
# Invocation helpers
# ═══════════════════════════════════════════════════════════════════════════


def _extract_tool_schemas(tools: list[Callable]) -> list[dict[str, Any]]:
    """Extract JSON-serializable tool schemas from callables.

    Works with both plain functions (extracts name, docstring, params
    via inspect) and LangChain BaseTool instances (uses .get_input_schema()).
    """
    import inspect

    schemas: list[dict[str, Any]] = []
    for t in tools:
        try:
            # LangChain BaseTool
            if hasattr(t, "name") and hasattr(t, "get_input_schema"):
                schema = t.get_input_schema().model_json_schema()
                schemas.append({
                    "name": t.name,
                    "description": getattr(t, "description", "") or "",
                    "parameters": schema,
                })
            else:
                # Plain callable — extract from signature + docstring
                sig = inspect.signature(t)
                params = {
                    name: {
                        "type": (p.annotation.__name__
                                 if p.annotation != inspect.Parameter.empty
                                 else "any"),
                        **({"default": p.default}
                           if p.default != inspect.Parameter.empty
                           else {}),
                    }
                    for name, p in sig.parameters.items()
                }
                schemas.append({
                    "name": getattr(t, "__name__", str(t)),
                    "description": (inspect.getdoc(t) or "").strip(),
                    "parameters": params,
                })
        except Exception:
            schemas.append({
                "name": getattr(t, "__name__", getattr(t, "name", str(t))),
                "description": "",
                "parameters": {},
            })
    return schemas


def _build_invoke_input(
    messages: list[dict[str, str]],
) -> dict[str, Any]:
    return {"messages": messages}


def invoke_agent(
    agent_id: str | None,
    messages: list[dict[str, str]],
    thread_id: str | None = None,
    trace_enabled: bool = True,
) -> dict[str, Any]:
    """Invoke an agent synchronously.

    Returns dict with: thread_id, response, tool_calls, todos, files,
    structured_response, interrupted, interrupted_tool, interrupted_args,
    trace (when trace_enabled=True).
    """
    resolved_agent_id = agent_id or "default"
    if agent_id and agent_id != "default":
        meta = get_agent(agent_id)
    else:
        meta = get_or_create_default_agent()

    agent = meta["agent"]
    is_new_thread = thread_id is None
    thread_id = thread_id or str(uuid.uuid4())
    config = {"configurable": {"thread_id": thread_id}}

    # ── Trace: init ───────────────────────────────────────────────────────
    trace = AgentTrace(agent_id=resolved_agent_id, thread_id=thread_id)

    trace.add("agent_start", {
        "agent_id": resolved_agent_id,
        "agent_name": meta.get("name"),
        "model": meta["model"],
        "tools": meta["tools"],
        "tool_schemas": meta.get("tool_schemas", []),
        "system_prompt": meta["system_prompt"],
        "backend_type": meta.get("backend_type", "filesystem"),
        "is_new_thread": is_new_thread,
    })

    trace.add("input_prompt", {
        "messages": messages,
        "thread_id": thread_id,
    })

    inp = _build_invoke_input(messages)

    # Enable LangChain's built-in debug logging if agent has debug: true
    if meta.get("debug"):
        from langchain_core.globals import set_debug
        set_debug(True)

    logger.info(
        "INVOKE agent=%s thread=%s model=%s tools=%s",
        resolved_agent_id, thread_id, meta["model"], meta["tools"],
    )

    trace.start_timer("invoke")
    result = agent.invoke(inp, config=config)
    invoke_duration = trace.stop_timer("invoke")

    if meta.get("debug"):
        from langchain_core.globals import set_debug
        set_debug(False)

    extracted = _extract_result(result, thread_id, meta)

    # ── Trace: output ─────────────────────────────────────────────────────
    trace.add("agent_end", {
        "response_length": len(extracted["response"]),
        "tool_calls_count": len(extracted["tool_calls"]),
        "tool_calls": extracted["tool_calls"],
        "todos_count": len(extracted["todos"]),
        "files_written": list(extracted["files"].keys()),
        "interrupted": extracted["interrupted"],
        "duration_ms": round(invoke_duration, 2) if invoke_duration else None,
    })

    logger.info(
        "RESULT agent=%s thread=%s response_len=%d tools_called=%d duration_ms=%s",
        resolved_agent_id, thread_id,
        len(extracted["response"]),
        len(extracted["tool_calls"]),
        round(invoke_duration, 2) if invoke_duration else "?",
    )

    if trace_enabled:
        extracted["trace"] = trace.to_dict()

    return extracted


def resume_agent(
    agent_id: str | None,
    thread_id: str,
    decision: str = "approve",
    edit_args: dict[str, Any] | None = None,
) -> dict[str, Any]:
    """Resume an interrupted agent after human-in-the-loop decision.

    Decisions: approve, reject, edit (with edit_args).
    """
    if agent_id and agent_id != "default":
        meta = get_agent(agent_id)
    else:
        meta = get_or_create_default_agent()

    agent = meta["agent"]
    config = {"configurable": {"thread_id": thread_id}}

    # Build the resume payload based on decision
    if decision == "reject":
        resume_input = None  # Rejecting cancels the tool call
    elif decision == "edit" and edit_args:
        resume_input = {"args": edit_args}
    else:
        resume_input = None  # approve = continue as-is

    result = agent.invoke(resume_input, config=config)
    return _extract_result(result, thread_id, meta)


async def stream_agent(
    agent_id: str | None,
    messages: list[dict[str, str]],
    thread_id: str | None = None,
):
    """Async generator yielding SSE events from the agent.

    Forwards ALL LangGraph astream_events(v2) as raw serialized data,
    plus two custom bookend events:
      - done  — stream complete (thread_id + total duration)
      - error — something went wrong
    """
    resolved_agent_id = agent_id or "default"
    if agent_id and agent_id != "default":
        meta = get_agent(agent_id)
    else:
        meta = get_or_create_default_agent()

    agent = meta["agent"]
    thread_id = thread_id or str(uuid.uuid4())
    config = {"configurable": {"thread_id": thread_id}}

    stream_start = time.time()

    logger.info(
        "STREAM agent=%s thread=%s model=%s input=%s",
        resolved_agent_id, thread_id, meta["model"], messages,
    )

    inp = _build_invoke_input(messages)

    # Enable LangChain's built-in debug logging if agent has debug: true
    if meta.get("debug"):
        from langchain_core.globals import set_debug
        set_debug(True)

    try:
        async for event in agent.astream_events(inp, config=config, version="v2"):
            kind = event.get("event", "")
            data = event.get("data", {})

            # Serialize the raw framework event — make LangChain objects JSON-safe
            serialized: dict[str, Any] = {
                "event": kind,
                "name": event.get("name", ""),
                "run_id": event.get("run_id", ""),
                "tags": event.get("tags", []),
                "metadata": _safe_serialize(event.get("metadata", {})),
                "parent_ids": event.get("parent_ids", []),
                "data": _safe_serialize(data),
            }

            yield {"event": kind, "data": serialized}

        # ── done (our event) ──────────────────────────────────────────
        total_ms = round((time.time() - stream_start) * 1000, 2)
        yield {
            "event": "done",
            "data": {
                "thread_id": thread_id,
                "total_duration_ms": total_ms,
            },
        }
        logger.info("STREAM DONE agent=%s thread=%s duration=%sms",
                     resolved_agent_id, thread_id, total_ms)

    except Exception as exc:
        logger.error("STREAM ERROR agent=%s: %s", resolved_agent_id, exc, exc_info=True)
        yield {"event": "error", "data": {"error": str(exc)}}

    finally:
        if meta.get("debug"):
            from langchain_core.globals import set_debug
            set_debug(False)


def _safe_serialize(obj: Any, max_depth: int = 3, max_str_len: int = 2000) -> Any:
    """Safely serialize an object for JSON output, handling LangChain objects."""
    if max_depth <= 0:
        return str(obj)[:max_str_len] if obj is not None else None
    if obj is None or isinstance(obj, (bool, int, float)):
        return obj
    if isinstance(obj, str):
        return obj[:max_str_len] + "..." if len(obj) > max_str_len else obj
    if isinstance(obj, (list, tuple)):
        return [_safe_serialize(item, max_depth - 1, max_str_len) for item in obj[:50]]
    if isinstance(obj, dict):
        return {
            str(k): _safe_serialize(v, max_depth - 1, max_str_len)
            for k, v in list(obj.items())[:50]
        }
    # LangChain message objects, Pydantic models, etc.
    if hasattr(obj, "model_dump"):
        try:
            return _safe_serialize(obj.model_dump(), max_depth - 1, max_str_len)
        except Exception:
            pass
    if hasattr(obj, "__dict__"):
        return _safe_serialize(
            {k: v for k, v in obj.__dict__.items() if not k.startswith("_")},
            max_depth - 1,
            max_str_len,
        )
    return str(obj)[:max_str_len]


# ═══════════════════════════════════════════════════════════════════════════
# Result extraction
# ═══════════════════════════════════════════════════════════════════════════


def _extract_result(
    result: dict[str, Any],
    thread_id: str,
    meta: dict[str, Any],
) -> dict[str, Any]:
    """Extract a standardized response dict from agent invoke result."""
    response_text = ""
    tool_calls: list[dict[str, Any]] = []

    if result.get("messages"):
        last_msg = result["messages"][-1]
        response_text = getattr(last_msg, "content", str(last_msg))
        tool_calls = getattr(last_msg, "tool_calls", []) or []

    # Todos
    todos = result.get("todos", [])

    # Written files
    written_files: dict[str, Any] = {}
    if result.get("files"):
        for path, fdata in result["files"].items():
            if isinstance(fdata, dict):
                written_files[path] = fdata.get("content", "")
            else:
                written_files[path] = str(fdata)

    # Structured response
    structured_response = None
    if result.get("structured_response"):
        sr = result["structured_response"]
        if hasattr(sr, "model_dump"):
            structured_response = sr.model_dump()
        elif isinstance(sr, dict):
            structured_response = sr

    # Human-in-the-loop interrupt detection
    interrupted = False
    interrupted_tool = None
    interrupted_args = None

    # LangGraph signals interrupts via special state
    if result.get("__interrupt__"):
        interrupted = True
        interrupt_info = result["__interrupt__"]
        if isinstance(interrupt_info, list) and interrupt_info:
            first = interrupt_info[0]
            interrupted_tool = getattr(first, "name", None) or (
                first.get("name") if isinstance(first, dict) else None
            )
            interrupted_args = getattr(first, "args", None) or (
                first.get("args") if isinstance(first, dict) else None
            )

    return {
        "thread_id": thread_id,
        "response": response_text,
        "tool_calls": [_serialize_tool_call(tc) for tc in tool_calls],
        "todos": todos if isinstance(todos, list) else [],
        "files": written_files,
        "structured_response": structured_response,
        "interrupted": interrupted,
        "interrupted_tool": interrupted_tool,
        "interrupted_args": interrupted_args,
    }


def _serialize_tool_call(tc: Any) -> dict[str, Any]:
    if isinstance(tc, dict):
        return tc
    return {
        "name": getattr(tc, "name", "unknown"),
        "args": getattr(tc, "args", {}),
        "id": getattr(tc, "id", ""),
    }
