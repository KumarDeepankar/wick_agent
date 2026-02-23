"""FastAPI routes for deep agent operations.

Exposes every customization knob from the deep-agents library:
  model, tools, system_prompt, middleware, subagents, backend,
  store, interrupt_on, skills, memory, response_format, cache,
  debug, name
"""

from __future__ import annotations

import asyncio
import json
import mimetypes
import shlex

from fastapi import APIRouter, Depends, HTTPException, Request, WebSocket, WebSocketDisconnect
from fastapi.responses import Response
from sse_starlette.sse import EventSourceResponse

from app.agents.config_loader import remove_agent_from_yaml, save_agent_to_yaml
from app.agents.deep_agent import (
    _TEMPLATE_REGISTRY,
    create_deep_agent_from_config,
    delete_agent,
    get_or_clone_agent,
    invoke_agent,
    list_agents,
    list_middleware,
    list_templates,
    list_tools,
    resume_agent,
    stream_agent,
    update_agent_backend,
    update_agent_tools,
)
from app.auth import get_allowed_tools, get_current_user
from app.config import settings
from app.models.schemas import (
    AgentCreateRequest,
    AgentInfo,
    FileUploadRequest,
    InvokeRequest,
    InvokeResponse,
    ResumeRequest,
    StreamRequest,
)

# Conditional auth: when gateway URL is configured, require auth on all routes
_deps: list = []
if settings.wick_gateway_url:
    _deps.append(Depends(get_current_user))

router = APIRouter(prefix="/agents", tags=["agents"], dependencies=_deps)


def _is_tool_allowed(tool_name: str, gateway_allowed: set[str]) -> bool:
    """Check if a tool name is permitted by the gateway.

    The gateway only knows about MCP downstream tools, so:
    - ``"*"`` in the allowed set → everything passes (local-dev shortcut)
    - MCP-prefixed tools (``mcp_<server>_<name>``) → strip prefix, check
      the bare name against the gateway list
    - Non-MCP (agent-local) tools → always allowed, because the gateway
      has no opinion on them
    """
    if "*" in gateway_allowed:
        return True
    # MCP prefixed tools: "mcp_gateway_add" -> check "add"
    if tool_name.startswith("mcp_"):
        parts = tool_name.split("_", 2)
        if len(parts) >= 3 and parts[2] in gateway_allowed:
            return True
        return False
    # Non-MCP local tools — gateway doesn't govern these
    return True


async def _resolve_user(request: Request) -> str:
    """Extract the username from the auth token (or 'local' when auth is off)."""
    user = await get_current_user(request)
    return user.username


# ═══════════════════════════════════════════════════════════════════════════
# Config-change SSE (relayed from gateway)
# ═══════════════════════════════════════════════════════════════════════════


@router.get("/events", tags=["events"])
async def agent_events(request: Request):
    """SSE stream of config change events relayed from the gateway.

    Events are filtered per-user: only events broadcast with this user's
    username (or unscoped events) are forwarded to the client.
    """
    import app.events as gateway_events

    username = await _resolve_user(request)
    q = await gateway_events.subscribe()

    async def event_gen():
        try:
            while True:
                if await request.is_disconnected():
                    break
                try:
                    raw = await asyncio.wait_for(q.get(), timeout=30)
                    # User-scoped events are encoded as "event_name:username"
                    if ":" in raw:
                        event_name, event_user = raw.rsplit(":", 1)
                        if event_user != username:
                            continue  # skip other users' events
                        yield {"event": event_name, "data": "{}"}
                    else:
                        # Unscoped events go to everyone
                        yield {"event": raw, "data": "{}"}
                except asyncio.TimeoutError:
                    # Send keep-alive comment to prevent proxy timeouts
                    yield {"comment": "keep-alive"}
        except asyncio.CancelledError:
            pass
        finally:
            await gateway_events.unsubscribe(q)

    return EventSourceResponse(event_gen())


# ═══════════════════════════════════════════════════════════════════════════
# Agent CRUD
# ═══════════════════════════════════════════════════════════════════════════


@router.post("/", response_model=AgentInfo, status_code=201)
async def create_new_agent(req: AgentCreateRequest, request: Request):
    """Create and register a new deep agent.

    Accepts all customization knobs:
    - **name**: human-readable label
    - **model**: LLM provider + model (`openai:gpt-4.1`, `claude-sonnet-4-5-20250929`, etc.)
    - **system_prompt**: custom instructions
    - **tools**: list of registered tool names
    - **middleware**: list of registered middleware names
    - **subagents**: subagent definitions with their own tools/model/middleware
    - **backend**: storage config (state | filesystem | store | composite)
    - **interrupt_on**: human-in-the-loop rules per tool
    - **skills**: skill directory paths for contextual knowledge
    - **memory**: persistent AGENTS.md file paths + initial content
    - **response_format**: structured output schema
    - **cache**: LLM response caching config
    - **debug**: verbose logging
    """
    username = await _resolve_user(request)
    try:
        subagents = [sa.model_dump() for sa in req.subagents] if req.subagents else None
        interrupt_on_cfg = None
        if req.interrupt_on:
            interrupt_on_cfg = {
                tool_name: rule.model_dump() if hasattr(rule, "model_dump") else rule
                for tool_name, rule in req.interrupt_on.items()
            }

        backend_cfg = req.backend.model_dump() if req.backend else None
        skills_cfg = req.skills.model_dump() if req.skills else None
        memory_cfg = req.memory.model_dump() if req.memory else None
        response_format_cfg = req.response_format.model_dump() if req.response_format else None
        cache_cfg = req.cache.model_dump() if req.cache else None

        meta = create_deep_agent_from_config(
            agent_id=req.agent_id,
            username=username,
            name=req.name,
            model=req.model,
            system_prompt=req.system_prompt,
            tool_names=req.tools,
            middleware_names=req.middleware,
            subagents=subagents,
            backend_cfg=backend_cfg,
            interrupt_on_cfg=interrupt_on_cfg,
            skills_cfg=skills_cfg,
            memory_cfg=memory_cfg,
            response_format_cfg=response_format_cfg,
            cache_cfg=cache_cfg,
            debug=req.debug,
        )

        # Persist to agents.yaml
        save_agent_to_yaml(
            agent_id=req.agent_id,
            name=req.name,
            model=req.model,
            system_prompt=req.system_prompt,
            tools=req.tools,
            middleware=req.middleware,
            subagents=subagents,
            backend_cfg=backend_cfg,
            interrupt_on_cfg=interrupt_on_cfg,
            skills_cfg=skills_cfg,
            memory_cfg=memory_cfg,
            response_format_cfg=response_format_cfg,
            cache_cfg=cache_cfg,
            debug=req.debug,
        )

        return _meta_to_info(meta)
    except KeyError as e:
        raise HTTPException(status_code=400, detail=str(e))


@router.get("/", response_model=list[AgentInfo])
async def list_all_agents(request: Request):
    """List all registered agents with their configuration summary."""
    username = await _resolve_user(request)
    return [AgentInfo(**a) for a in list_agents(username=username)]


@router.get("/{agent_id}", response_model=AgentInfo)
async def get_agent_info(agent_id: str, request: Request):
    """Get full configuration info for a specific agent."""
    username = await _resolve_user(request)
    try:
        return _meta_to_info(get_or_clone_agent(agent_id, username))
    except KeyError:
        raise HTTPException(status_code=404, detail=f"Agent '{agent_id}' not found")


@router.delete("/{agent_id}", status_code=204)
async def remove_agent(agent_id: str, request: Request):
    """Delete a registered agent instance for the current user.

    Only removes the user's scoped instance. If the agent came from a
    template, the template is preserved (other users are unaffected).
    """
    username = await _resolve_user(request)
    try:
        delete_agent(agent_id, username=username)
        # Only remove from YAML if this is NOT a template-based agent
        if agent_id not in _TEMPLATE_REGISTRY:
            remove_agent_from_yaml(agent_id)
    except KeyError:
        raise HTTPException(status_code=404, detail=f"Agent '{agent_id}' not found")


# ═══════════════════════════════════════════════════════════════════════════
# Invoke
# ═══════════════════════════════════════════════════════════════════════════


@router.post("/invoke", response_model=InvokeResponse)
@router.post("/{agent_id}/invoke", response_model=InvokeResponse)
async def invoke(req: InvokeRequest, request: Request, agent_id: str | None = None):
    """Invoke an agent and get a complete response.

    - Uses the **default** agent when no `agent_id` is given.
    - Pass `thread_id` to continue an existing conversation.
    - Pass `trace: true` (default) to include full execution trace
      with input prompts, tool calls, timing, and system prompt.
    - If the agent has `interrupt_on` rules and a tool triggers,
      the response will have `interrupted=true` with the tool info.
      Use the `/resume` endpoint to continue.
    """
    username = await _resolve_user(request)
    try:
        messages = [m.model_dump() for m in req.messages]
        result = invoke_agent(
            agent_id=agent_id,
            messages=messages,
            thread_id=req.thread_id,
            trace_enabled=req.trace,
            username=username,
        )
        return InvokeResponse(**result)
    except KeyError as e:
        raise HTTPException(status_code=404, detail=str(e))
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


# ═══════════════════════════════════════════════════════════════════════════
# Human-in-the-loop resume
# ═══════════════════════════════════════════════════════════════════════════


@router.post("/resume", response_model=InvokeResponse)
@router.post("/{agent_id}/resume", response_model=InvokeResponse)
async def resume(req: ResumeRequest, request: Request, agent_id: str | None = None):
    """Resume an interrupted agent after human-in-the-loop review.

    Use this after an invoke returned `interrupted=true`.

    Decisions:
    - **approve**: continue with the original tool call as-is
    - **reject**: cancel the tool call and let the agent re-plan
    - **edit**: modify the tool arguments (pass new args in `edit_args`)
    """
    username = await _resolve_user(request)
    try:
        result = resume_agent(
            agent_id=agent_id,
            thread_id=req.thread_id,
            decision=req.decision,
            edit_args=req.edit_args,
            username=username,
        )
        return InvokeResponse(**result)
    except KeyError as e:
        raise HTTPException(status_code=404, detail=str(e))
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


# ═══════════════════════════════════════════════════════════════════════════
# Stream (SSE)
# ═══════════════════════════════════════════════════════════════════════════


@router.post("/stream")
@router.post("/{agent_id}/stream")
async def stream(req: StreamRequest, request: Request, agent_id: str | None = None):
    """Stream agent responses via Server-Sent Events.

    Full-visibility events (in order):
    - `agent_start`: agent config (model, tools, system prompt, thread)
    - `input_prompt`: exact messages being sent to the agent
    - `files_seeded`: virtual FS files seeded (paths + sizes)
    - `llm_start`: LLM invocation with full input messages
    - `token`: incremental text chunk from LLM
    - `llm_end`: LLM response complete (duration, preview, tool calls requested)
    - `tool_call`: agent called a tool (name + input)
    - `tool_result`: tool returned (name + output + duration)
    - `node_start` / `node_end`: LangGraph node transitions
    - `done`: stream complete (thread_id + trace summary)
    - `error`: something went wrong
    """
    username = await _resolve_user(request)
    messages = [m.model_dump() for m in req.messages]

    async def event_generator():
        async for event in stream_agent(
            agent_id=agent_id,
            messages=messages,
            thread_id=req.thread_id,
            username=username,
        ):
            data = event["data"]
            yield {
                "event": event["event"],
                "data": json.dumps(data) if isinstance(data, (dict, list)) else str(data),
            }

    return EventSourceResponse(event_generator())


# ═══════════════════════════════════════════════════════════════════════════
# Registries (tools & middleware)
# ═══════════════════════════════════════════════════════════════════════════


@router.get("/tools/available", tags=["tools"])
async def get_available_tools(request: Request):
    """List all registered tools that can be assigned to agents."""
    all_tools = list_tools()
    if not settings.wick_gateway_url:
        return {"tools": all_tools}

    token = request.headers.get("authorization", "")[7:]  # strip "Bearer "
    allowed = await get_allowed_tools(token)
    filtered = [t for t in all_tools if _is_tool_allowed(t, allowed)]
    return {"tools": filtered}


@router.patch("/{agent_id}/tools", tags=["tools"])
async def patch_agent_tools(agent_id: str, body: dict, request: Request):
    """Update the active tools for an agent.

    Rebuilds the agent with the new tool set.  Thread state is preserved.
    Expects ``{"tools": ["tool_name_1", "tool_name_2"]}``.
    """
    tool_names = body.get("tools")
    if tool_names is None or not isinstance(tool_names, list):
        raise HTTPException(status_code=422, detail="Body must contain a 'tools' list")
    available = set(list_tools())
    invalid = [t for t in tool_names if t not in available]
    if invalid:
        raise HTTPException(status_code=400, detail=f"Unknown tools: {invalid}")

    # RBAC: reject tools the user's role doesn't permit
    if settings.wick_gateway_url:
        token = request.headers.get("authorization", "")[7:]
        allowed = await get_allowed_tools(token)
        forbidden = [t for t in tool_names if not _is_tool_allowed(t, allowed)]
        if forbidden:
            raise HTTPException(status_code=403, detail=f"Tools not permitted for your role: {forbidden}")

    username = await _resolve_user(request)
    try:
        meta = update_agent_tools(agent_id, tool_names, username=username)
        return {"agent_id": agent_id, "tools": meta["tools"]}
    except KeyError as e:
        raise HTTPException(status_code=404, detail=str(e))


@router.patch("/{agent_id}/backend", tags=["backend"])
async def patch_agent_backend(agent_id: str, body: dict, request: Request):
    """Update the sandbox backend config for an agent.

    Accepts:
      - {"mode": "local"}                          → switch to local execution
      - {"mode": "remote", "sandbox_url": "tcp://…"} → switch to remote Docker
      - {"sandbox_url": "tcp://…"}                  → update remote Docker URL
      - {"sandbox_url": null}                       → clear remote URL

    Rebuilds the agent with the new backend.
    """
    user = await get_current_user(request)

    mode = body.get("mode")
    sandbox_url = body.get("sandbox_url")
    backend_updates: dict = {}

    if mode == "local":
        backend_updates["type"] = "local"
        backend_updates["docker_host"] = None
    elif mode == "remote":
        backend_updates["type"] = "docker"
        backend_updates["docker_host"] = sandbox_url if sandbox_url else None
    else:
        # Legacy: just update sandbox_url without mode switch
        if sandbox_url is None or sandbox_url == "":
            backend_updates["docker_host"] = None
        else:
            backend_updates["docker_host"] = sandbox_url

    try:
        meta = update_agent_backend(agent_id, backend_updates, username=user.username)

        # Fire async container launch for docker backends
        backend = meta.get("_backend")
        if backend and hasattr(backend, "launch_container_async"):
            # Pre-set status so the response already shows "launching"
            backend._container_status = "launching"
            backend._container_error = None
            task = asyncio.create_task(backend.launch_container_async())
            backend._launch_task = task

        return {
            "agent_id": agent_id,
            "sandbox_url": meta.get("sandbox_url"),
            "backend_type": meta.get("backend_type", "state"),
            "container_status": getattr(backend, "container_status", None) if backend else None,
            "container_error": getattr(backend, "container_error", None) if backend else None,
        }
    except KeyError as e:
        raise HTTPException(status_code=404, detail=str(e))
    except RuntimeError as e:
        raise HTTPException(status_code=400, detail=str(e))


@router.get("/middleware/available", tags=["middleware"])
async def get_available_middleware():
    """List all registered middleware that can be assigned to agents."""
    return {"middleware": list_middleware()}


@router.get("/skills/available", tags=["skills"])
async def get_available_skills():
    """List all skills discovered from the skills/ directory.

    Parses YAML frontmatter from each SKILL.md to return name,
    description, sample prompts, and icon.
    """
    import re
    import yaml
    from pathlib import Path

    _FM_RE = re.compile(r"\A---\s*\n(.*?\n)---\s*\n", re.DOTALL)

    skills_dir = Path("skills")
    skills = []
    if skills_dir.is_dir():
        for skill_md in sorted(skills_dir.rglob("SKILL.md")):
            entry = {
                "name": skill_md.parent.name,
                "description": "",
                "sample_prompts": [],
                "icon": "",
            }
            try:
                text = skill_md.read_text(encoding="utf-8")
                m = _FM_RE.match(text)
                if m:
                    front = yaml.safe_load(m.group(1))
                    if isinstance(front, dict):
                        entry["name"] = front.get("name", entry["name"])
                        entry["description"] = front.get("description", "").strip()
                        entry["sample_prompts"] = front.get("sample-prompts", [])
                        entry["icon"] = front.get("icon", "")
            except Exception:
                pass
            skills.append(entry)
    return {"skills": skills}


# ═══════════════════════════════════════════════════════════════════════════
# File Browser (container filesystem listing)
# ═══════════════════════════════════════════════════════════════════════════


def _validate_container_path(path: str) -> str:
    """Validate and sanitize a container filesystem path."""
    if not path.startswith("/"):
        raise HTTPException(status_code=400, detail="Path must be absolute")
    if ".." in path.split("/"):
        raise HTTPException(status_code=400, detail="Path must not contain '..'")
    return path


@router.get("/{agent_id}/files/list", tags=["files"])
async def list_container_files(agent_id: str, request: Request, path: str = "/workspace"):
    """List files and directories inside the agent's Docker container.

    Returns a flat list of entries with name, type (file/dir), size, and path.
    Used by the file browser panel to navigate the container filesystem.
    """
    path = _validate_container_path(path)
    username = await _resolve_user(request)

    try:
        meta = get_or_clone_agent(agent_id, username)
    except KeyError:
        raise HTTPException(status_code=404, detail=f"Agent '{agent_id}' not found")

    backend = meta.get("_backend")
    if backend is None or not hasattr(backend, "execute"):
        raise HTTPException(status_code=400, detail="Agent has no executable backend")

    if getattr(backend, "container_status", None) != "launched":
        raise HTTPException(status_code=400, detail="Container not launched")

    safe_path = shlex.quote(path)
    result = backend.execute(
        f'stat -c "%n\t%F\t%s" {safe_path}/* {safe_path}/.* 2>/dev/null | '
        f'grep -v "/\\.$" | grep -v "/\\.\\.$"'
    )

    entries = []
    if result.exit_code == 0 and result.output.strip():
        for line in result.output.strip().split("\n"):
            parts = line.split("\t", 2)
            if len(parts) == 3:
                full_path, ftype, size = parts
                name = full_path.rsplit("/", 1)[-1] if "/" in full_path else full_path
                entries.append({
                    "name": name,
                    "path": full_path,
                    "type": "dir" if "directory" in ftype.lower() else "file",
                    "size": int(size) if size.isdigit() else 0,
                })

    # Sort: directories first, then alphabetically
    entries.sort(key=lambda e: (0 if e["type"] == "dir" else 1, e["name"].lower()))

    return {"path": path, "entries": entries}


@router.get("/{agent_id}/files/read", tags=["files"])
async def read_container_file(agent_id: str, request: Request, path: str = "/workspace"):
    """Read the content of a file inside the agent's Docker container.

    Returns the file content as text for the file browser preview.
    """
    path = _validate_container_path(path)
    username = await _resolve_user(request)

    try:
        meta = get_or_clone_agent(agent_id, username)
    except KeyError:
        raise HTTPException(status_code=404, detail=f"Agent '{agent_id}' not found")

    backend = meta.get("_backend")
    if backend is None or not hasattr(backend, "execute"):
        raise HTTPException(status_code=400, detail="Agent has no executable backend")

    if getattr(backend, "container_status", None) != "launched":
        raise HTTPException(status_code=400, detail="Container not launched")

    safe_path = shlex.quote(path)

    # Check file exists and isn't too large (limit to 1MB)
    size_result = backend.execute(f'stat -c "%s" {safe_path} 2>/dev/null')
    if size_result.exit_code != 0:
        raise HTTPException(status_code=404, detail=f"File not found: {path}")

    size_str = size_result.output.strip()
    if size_str.isdigit() and int(size_str) > 1_000_000:
        raise HTTPException(status_code=400, detail="File too large to preview (>1MB)")

    result = backend.execute(f'cat {safe_path}')
    if result.exit_code != 0:
        raise HTTPException(status_code=400, detail=f"Cannot read file: {result.output}")

    return {"path": path, "content": result.output}


# ═══════════════════════════════════════════════════════════════════════════
# File Download
# ═══════════════════════════════════════════════════════════════════════════


@router.get("/files/download")
async def download_workspace_file(request: Request, path: str = "/workspace", agent_id: str | None = None):
    """Download a file from the agent's workspace (Docker container).

    Used by the Canvas panel to fetch full file content or binary files
    that were written by the agent during a session.
    """
    username = await _resolve_user(request)
    resolved_agent_id = agent_id or "default"
    try:
        meta = get_or_clone_agent(resolved_agent_id, username)
    except KeyError:
        raise HTTPException(status_code=404, detail=f"Agent '{resolved_agent_id}' not found")

    backend = meta.get("_backend")
    if backend is None or not hasattr(backend, "download_files"):
        raise HTTPException(
            status_code=400,
            detail="Agent does not have a backend that supports file downloads",
        )

    try:
        results = backend.download_files([path])
        if not results:
            raise HTTPException(status_code=404, detail=f"File not found: {path}")

        # download_files returns a list of FileDownloadResponse objects
        result = results[0]
        if result.error or result.content is None:
            raise HTTPException(status_code=404, detail=f"File not found: {path}")

        content = result.content
        if isinstance(content, str):
            content = content.encode("utf-8")

        mime_type = mimetypes.guess_type(path)[0] or "application/octet-stream"
        filename = path.rsplit("/", 1)[-1] if "/" in path else path

        return Response(
            content=content,
            media_type=mime_type,
            headers={"Content-Disposition": f'attachment; filename="{filename}"'},
        )
    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Download failed: {e}")


# ═══════════════════════════════════════════════════════════════════════════
# File Upload
# ═══════════════════════════════════════════════════════════════════════════


@router.put("/files/upload")
async def upload_workspace_file(req: FileUploadRequest, request: Request):
    """Upload (create or overwrite) a file in the agent's workspace.

    Used by the Canvas panel's edit mode to save modified slide content
    back to the Docker sandbox.
    """
    if not req.path.startswith("/"):
        raise HTTPException(
            status_code=400,
            detail="Path must be an absolute path",
        )
    if ".." in req.path:
        raise HTTPException(
            status_code=400,
            detail="Path must not contain '..'",
        )

    username = await _resolve_user(request)
    resolved_agent_id = req.agent_id or "default"
    try:
        meta = get_or_clone_agent(resolved_agent_id, username)
    except KeyError:
        raise HTTPException(status_code=404, detail=f"Agent '{resolved_agent_id}' not found")

    backend = meta.get("_backend")
    if backend is None or not hasattr(backend, "upload_files"):
        raise HTTPException(
            status_code=400,
            detail="Agent does not have a backend that supports file uploads",
        )

    try:
        content_bytes = req.content.encode("utf-8")
        backend.upload_files([(req.path, content_bytes)])
        return {
            "status": "ok",
            "path": req.path,
            "size": len(content_bytes),
        }
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Upload failed: {e}")


# ═══════════════════════════════════════════════════════════════════════════
# Slides Export
# ═══════════════════════════════════════════════════════════════════════════


@router.get("/slides/export")
async def export_slides_pptx(request: Request, path: str = "/workspace", agent_id: str | None = None):
    """Export a markdown slide deck as a .pptx PowerPoint file.

    Runs md2pptx.py inside the Docker sandbox to convert the markdown
    file at `path` into an editable .pptx, then downloads and returns it.
    """
    username = await _resolve_user(request)
    resolved_agent_id = agent_id or "default"
    try:
        meta = get_or_clone_agent(resolved_agent_id, username)
    except KeyError:
        raise HTTPException(status_code=404, detail=f"Agent '{resolved_agent_id}' not found")

    backend = meta.get("_backend")
    if backend is None or not hasattr(backend, "execute"):
        raise HTTPException(
            status_code=400,
            detail="Agent does not have a backend that supports execution",
        )

    # Run the converter inside the container
    pptx_tmp = "/tmp/_export_slides.pptx"
    exec_result = backend.execute(
        f"python /scripts/md2pptx.py '{path}' -o {pptx_tmp}"
    )
    if exec_result.exit_code != 0:
        raise HTTPException(
            status_code=500,
            detail=f"PPTX conversion failed: {exec_result.output}",
        )

    # Download the generated file
    if not hasattr(backend, "download_files"):
        raise HTTPException(
            status_code=400,
            detail="Agent backend does not support file downloads",
        )

    results = backend.download_files([pptx_tmp])
    if not results or results[0].error or results[0].content is None:
        raise HTTPException(status_code=500, detail="Failed to download generated PPTX")

    content = results[0].content
    if isinstance(content, str):
        content = content.encode("utf-8")

    # Derive filename from the source path
    base = path.rsplit("/", 1)[-1] if "/" in path else path
    filename = base.rsplit(".", 1)[0] + ".pptx" if "." in base else base + ".pptx"

    return Response(
        content=content,
        media_type="application/vnd.openxmlformats-officedocument.presentationml.presentation",
        headers={"Content-Disposition": f'attachment; filename="{filename}"'},
    )


# ═══════════════════════════════════════════════════════════════════════════
# WebSocket Terminal (separate router — no router-level auth deps,
# because WebSocket can't inject Request for Depends(get_current_user))
# ═══════════════════════════════════════════════════════════════════════════

ws_router = APIRouter(prefix="/agents", tags=["terminal"])


@ws_router.websocket("/{agent_id}/terminal")
async def terminal_websocket(websocket: WebSocket, agent_id: str):
    """Interactive terminal into the agent's Docker container via WebSocket."""
    # ── Manual auth (WebSocket can't use router-level Depends) ────────
    ws_username = "local"
    if settings.wick_gateway_url:
        token = websocket.query_params.get("token")
        if not token:
            await websocket.close(code=4001, reason="Missing token query param")
            return
        import httpx
        try:
            async with httpx.AsyncClient(timeout=10) as client:
                resp = await client.get(
                    f"{settings.wick_gateway_url}/auth/me",
                    headers={"Authorization": f"Bearer {token}"},
                )
            if resp.status_code != 200:
                await websocket.close(code=4001, reason="Invalid token")
                return
            ws_username = resp.json().get("username", "local")
        except Exception:
            await websocket.close(code=4002, reason="Auth gateway unreachable")
            return

    await websocket.accept()

    try:
        meta = get_or_clone_agent(agent_id, ws_username)
    except KeyError:
        await websocket.close(code=4004, reason=f"Agent '{agent_id}' not found")
        return

    backend = meta.get("_backend")
    if not backend or not hasattr(backend, "_docker_cmd"):
        await websocket.close(code=4000, reason="Agent has no Docker backend")
        return

    if getattr(backend, "container_status", None) != "launched":
        await websocket.close(code=4000, reason="Container not launched")
        return

    # Spawn docker exec with a PTY allocated via script(1).
    # Plain `docker exec -i sh` over pipes has no PTY, so the shell
    # won't produce a prompt or handle arrow keys / tab completion.
    # `script -qfc "..." /dev/null` allocates a PTY inside the container.
    cmd = backend._docker_cmd(
        "exec", "-i",
        "-e", "TERM=xterm-256color",
        backend._container_name,
        "script", "-qfc", "/bin/sh", "/dev/null",
    )

    proc = await asyncio.subprocess.create_subprocess_exec(
        *cmd,
        stdin=asyncio.subprocess.PIPE,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.STDOUT,
    )

    async def _read_stdout():
        """Read from process stdout and send to WebSocket."""
        try:
            while True:
                data = await proc.stdout.read(4096)
                if not data:
                    break
                await websocket.send_bytes(data)
        except (WebSocketDisconnect, ConnectionError):
            pass

    async def _read_ws():
        """Read from WebSocket and write to process stdin."""
        try:
            while True:
                data = await websocket.receive_bytes()
                if proc.stdin:
                    proc.stdin.write(data)
                    await proc.stdin.drain()
        except (WebSocketDisconnect, ConnectionError):
            pass

    read_task = asyncio.create_task(_read_stdout())
    write_task = asyncio.create_task(_read_ws())

    try:
        done, pending = await asyncio.wait(
            [read_task, write_task],
            return_when=asyncio.FIRST_COMPLETED,
        )
        for t in pending:
            t.cancel()
    finally:
        if proc.returncode is None:
            proc.kill()
        try:
            await websocket.close()
        except Exception:
            pass


# ═══════════════════════════════════════════════════════════════════════════
# Helpers
# ═══════════════════════════════════════════════════════════════════════════


def _meta_to_info(meta: dict) -> AgentInfo:
    backend = meta.get("_backend")
    return AgentInfo(
        agent_id=meta["agent_id"],
        name=meta.get("name"),
        model=meta["model"],
        system_prompt=meta["system_prompt"],
        tools=meta["tools"],
        subagents=meta.get("subagents", []),
        middleware=meta.get("middleware", []),
        backend_type=meta.get("backend_type", "filesystem"),
        sandbox_url=meta.get("sandbox_url"),
        has_interrupt_on=meta.get("has_interrupt_on", False),
        skills=meta.get("skills", []),
        loaded_skills=[],
        memory=meta.get("memory_paths", []),
        has_response_format=meta.get("has_response_format", False),
        cache_enabled=meta.get("cache_enabled", False),
        debug=meta.get("debug", False),
        container_status=getattr(backend, "container_status", None) if backend else None,
        container_error=getattr(backend, "container_error", None) if backend else None,
    )
