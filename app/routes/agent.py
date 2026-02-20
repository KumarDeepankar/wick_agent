"""FastAPI routes for deep agent operations.

Exposes every customization knob from the deep-agents library:
  model, tools, system_prompt, middleware, subagents, backend,
  store, interrupt_on, skills, memory, response_format, cache,
  debug, name
"""

from __future__ import annotations

import json
import mimetypes

from fastapi import APIRouter, HTTPException
from fastapi.responses import Response
from sse_starlette.sse import EventSourceResponse

from app.agents.config_loader import remove_agent_from_yaml, save_agent_to_yaml
from app.agents.deep_agent import (
    create_deep_agent_from_config,
    delete_agent,
    get_agent,
    invoke_agent,
    list_agents,
    list_middleware,
    list_tools,
    resume_agent,
    stream_agent,
)
from app.models.schemas import (
    AgentCreateRequest,
    AgentInfo,
    FileUploadRequest,
    InvokeRequest,
    InvokeResponse,
    ResumeRequest,
    StreamRequest,
)

router = APIRouter(prefix="/agents", tags=["agents"])


# ═══════════════════════════════════════════════════════════════════════════
# Agent CRUD
# ═══════════════════════════════════════════════════════════════════════════


@router.post("/", response_model=AgentInfo, status_code=201)
async def create_new_agent(req: AgentCreateRequest):
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
async def list_all_agents():
    """List all registered agents with their configuration summary."""
    return [AgentInfo(**a) for a in list_agents()]


@router.get("/{agent_id}", response_model=AgentInfo)
async def get_agent_info(agent_id: str):
    """Get full configuration info for a specific agent."""
    try:
        return _meta_to_info(get_agent(agent_id))
    except KeyError:
        raise HTTPException(status_code=404, detail=f"Agent '{agent_id}' not found")


@router.delete("/{agent_id}", status_code=204)
async def remove_agent(agent_id: str):
    """Delete a registered agent and remove from agents.yaml."""
    try:
        delete_agent(agent_id)
        remove_agent_from_yaml(agent_id)
    except KeyError:
        raise HTTPException(status_code=404, detail=f"Agent '{agent_id}' not found")


# ═══════════════════════════════════════════════════════════════════════════
# Invoke
# ═══════════════════════════════════════════════════════════════════════════


@router.post("/invoke", response_model=InvokeResponse)
@router.post("/{agent_id}/invoke", response_model=InvokeResponse)
async def invoke(req: InvokeRequest, agent_id: str | None = None):
    """Invoke an agent and get a complete response.

    - Uses the **default** agent when no `agent_id` is given.
    - Pass `thread_id` to continue an existing conversation.
    - Pass `trace: true` (default) to include full execution trace
      with input prompts, tool calls, timing, and system prompt.
    - If the agent has `interrupt_on` rules and a tool triggers,
      the response will have `interrupted=true` with the tool info.
      Use the `/resume` endpoint to continue.
    """
    try:
        messages = [m.model_dump() for m in req.messages]
        result = invoke_agent(
            agent_id=agent_id,
            messages=messages,
            thread_id=req.thread_id,
            trace_enabled=req.trace,
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
async def resume(req: ResumeRequest, agent_id: str | None = None):
    """Resume an interrupted agent after human-in-the-loop review.

    Use this after an invoke returned `interrupted=true`.

    Decisions:
    - **approve**: continue with the original tool call as-is
    - **reject**: cancel the tool call and let the agent re-plan
    - **edit**: modify the tool arguments (pass new args in `edit_args`)
    """
    try:
        result = resume_agent(
            agent_id=agent_id,
            thread_id=req.thread_id,
            decision=req.decision,
            edit_args=req.edit_args,
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
async def stream(req: StreamRequest, agent_id: str | None = None):
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
    messages = [m.model_dump() for m in req.messages]

    async def event_generator():
        async for event in stream_agent(
            agent_id=agent_id,
            messages=messages,
            thread_id=req.thread_id,
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
async def get_available_tools():
    """List all registered tools that can be assigned to agents."""
    return {"tools": list_tools()}


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
# File Download
# ═══════════════════════════════════════════════════════════════════════════


@router.get("/files/download")
async def download_workspace_file(path: str, agent_id: str | None = None):
    """Download a file from the agent's workspace (Docker container).

    Used by the Canvas panel to fetch full file content or binary files
    that were written by the agent during a session.
    """
    resolved_agent_id = agent_id or "default"
    try:
        meta = get_agent(resolved_agent_id)
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
async def upload_workspace_file(req: FileUploadRequest):
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

    resolved_agent_id = req.agent_id or "default"
    try:
        meta = get_agent(resolved_agent_id)
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
async def export_slides_pptx(path: str, agent_id: str | None = None):
    """Export a markdown slide deck as a .pptx PowerPoint file.

    Runs md2pptx.py inside the Docker sandbox to convert the markdown
    file at `path` into an editable .pptx, then downloads and returns it.
    """
    resolved_agent_id = agent_id or "default"
    try:
        meta = get_agent(resolved_agent_id)
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
# Helpers
# ═══════════════════════════════════════════════════════════════════════════


def _meta_to_info(meta: dict) -> AgentInfo:
    return AgentInfo(
        agent_id=meta["agent_id"],
        name=meta.get("name"),
        model=meta["model"],
        system_prompt=meta["system_prompt"],
        tools=meta["tools"],
        subagents=meta.get("subagents", []),
        middleware=meta.get("middleware", []),
        backend_type=meta.get("backend_type", "filesystem"),
        has_interrupt_on=meta.get("has_interrupt_on", False),
        skills=meta.get("skills", []),
        loaded_skills=[],
        memory=meta.get("memory_paths", []),
        has_response_format=meta.get("has_response_format", False),
        cache_enabled=meta.get("cache_enabled", False),
        debug=meta.get("debug", False),
    )
