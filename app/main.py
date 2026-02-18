"""Wick Agent – FastAPI trigger for LangChain Deep Agents.

Agent configurations are persisted in agents.yaml and loaded at startup.
"""

from __future__ import annotations

import logging
from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import FileResponse
from fastapi.staticfiles import StaticFiles

from app.agents.config_loader import CONFIG_PATH, load_agents_from_yaml
from app.agents.deep_agent import list_agents
import app.agents.tools  # noqa: F401 – registers custom tools/middleware on import
from app.models.schemas import HealthResponse
from app.routes.agent import router as agent_router

logger = logging.getLogger(__name__)


@asynccontextmanager
async def lifespan(app: FastAPI):
    # Startup: load all agents from agents.yaml
    logging.basicConfig(level=logging.INFO, format="%(levelname)s | %(name)s | %(message)s")
    logger.info("Loading agent configs from %s", CONFIG_PATH)
    count = load_agents_from_yaml()
    logger.info("Loaded %d agent(s) from agents.yaml", count)
    yield


app = FastAPI(
    title="Wick Agent",
    description=(
        "FastAPI service for triggering and managing LangChain Deep Agents. "
        "Agent configurations are persisted in agents.yaml. "
        "Supports all deep-agents customization knobs: model, tools, "
        "system_prompt, middleware, subagents, backend, interrupt_on, "
        "skills, memory, response_format, cache, debug, name."
    ),
    version="0.2.0",
    lifespan=lifespan,
)

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

app.include_router(agent_router)


@app.get("/health", response_model=HealthResponse, tags=["system"])
async def health_check():
    return HealthResponse(
        status="ok",
        agents_loaded=len(list_agents()),
    )


# ── Serve built UI (production: single-container deployment) ─────────────
# Looks for static/ directory (copied from ui/dist/ in Docker build).
# In dev, the Vite dev server proxies API calls instead.
_STATIC_DIR = Path(__file__).resolve().parent.parent / "static"
if _STATIC_DIR.is_dir():
    # Serve all static files (JS, CSS, images, logo, etc.)
    app.mount("/assets", StaticFiles(directory=_STATIC_DIR / "assets"), name="static-assets")

    # Serve root-level static files (logo.png, favicon, etc.)
    @app.get("/{filename:path}", include_in_schema=False)
    async def spa_fallback(filename: str):
        # Serve static file if it exists, otherwise SPA fallback
        file_path = _STATIC_DIR / filename
        if file_path.is_file():
            return FileResponse(file_path)
        return FileResponse(_STATIC_DIR / "index.html")
