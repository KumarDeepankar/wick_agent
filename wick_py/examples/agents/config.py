"""Shared configuration for the main agents.

Keeping this as a dataclass (rather than a plain dict) gives us type hints
and a single place to evolve the shape as new fields are added.
"""

from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from wick import SkillsConfig

from . import prompts

REPO_ROOT = Path(__file__).resolve().parent.parent.parent.parent
DEFAULT_SKILLS_DIR = REPO_ROOT / "wick_py" / "skills"

MAIN_SYSTEM_PROMPT = prompts.load("main")


@dataclass(frozen=True)
class SharedConfig:
    """Configuration shared by every supervisor (top-level) agent.

    Passed to `build_<supervisor>_agent(cfg)` builders; each builder reads
    `cfg.backend`, `cfg.skills`, `cfg.debug` explicitly so that every argument
    to `Agent(...)` is visible at the call site.
    """

    backend: dict[str, Any]
    skills: SkillsConfig
    debug: bool


def load_shared_config() -> SharedConfig:
    """Build the SharedConfig from environment variables with sane defaults."""
    skills_dir = os.environ.get("WICK_SKILLS_DIR") or str(DEFAULT_SKILLS_DIR)
    return SharedConfig(
        backend={"type": "local", "workdir": "/workspace"},
        skills=SkillsConfig(paths=[skills_dir], exclude=["slides", "report-generator"]),
        debug=True,
    )
