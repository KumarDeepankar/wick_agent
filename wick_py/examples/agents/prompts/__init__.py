"""Prompt loader — read system prompts from markdown files in this directory."""

from __future__ import annotations

from functools import lru_cache
from pathlib import Path

_DIR = Path(__file__).parent


@lru_cache(maxsize=None)
def load(name: str) -> str:
    """Return the system prompt stored at prompts/<name>.md.

    Prompts are cached after first read. If you edit a prompt file during a
    long-running session, call load.cache_clear() to pick up the change.
    """
    path = _DIR / f"{name}.md"
    if not path.exists():
        raise FileNotFoundError(f"prompt not found: {path}")
    return path.read_text().strip()
