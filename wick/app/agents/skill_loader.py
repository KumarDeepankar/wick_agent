"""Skill discovery and loading from the skills/ directory.

Scans skill directories for SKILL.md files and their supporting assets
(scripts, templates, docs), reads their content, and provides them as
file dicts ready for agent invocation.

Deep agents use "progressive disclosure" for skills:
  1. Match  — agent sees skill descriptions (from SKILL.md frontmatter)
  2. Read   — agent accesses full SKILL.md content
  3. Execute — agent follows instructions, runs bundled scripts

With StateBackend (the default), all skill files must be injected via
the `files` parameter on every invoke call.  This module handles
discovery and loading of SKILL.md + supporting files automatically.
"""

from __future__ import annotations

import logging
from pathlib import Path
from typing import Any

logger = logging.getLogger(__name__)

PROJECT_ROOT = Path(__file__).resolve().parent.parent.parent
DEFAULT_SKILLS_DIR = PROJECT_ROOT / "skills"

# Max SKILL.md size per the deep-agents spec (10 MB)
_MAX_SKILL_SIZE = 10 * 1024 * 1024

# Max size for supporting files (scripts, docs, etc.)
_MAX_ASSET_SIZE = 5 * 1024 * 1024

# File extensions treated as text and loaded into virtual FS
_TEXT_EXTENSIONS = {
    ".md", ".py", ".js", ".ts", ".sh", ".bash",
    ".txt", ".csv", ".json", ".yaml", ".yml",
    ".toml", ".cfg", ".ini", ".env",
    ".html", ".css", ".xml", ".sql",
    ".r", ".jl", ".rb", ".go", ".rs",
}


def discover_skills(
    skills_dirs: list[str] | None = None,
) -> dict[str, dict[str, Any]]:
    """Discover all skills from the given directories.

    Args:
        skills_dirs: List of directory paths to scan.
                     Defaults to the project's `skills/` directory.

    Returns:
        Dict of skill_name -> {
            "path":       virtual POSIX path to SKILL.md,
            "disk_path":  absolute disk path to SKILL.md,
            "content":    raw SKILL.md content,
            "assets":     list of {path, disk_path, content} for supporting files,
        }
    """
    dirs = []
    if skills_dirs:
        for d in skills_dirs:
            p = Path(d)
            if not p.is_absolute():
                p = PROJECT_ROOT / p
            dirs.append(p)
    else:
        dirs.append(DEFAULT_SKILLS_DIR)

    skills: dict[str, dict[str, Any]] = {}

    for skills_dir in dirs:
        if not skills_dir.is_dir():
            logger.warning("Skills directory not found: %s", skills_dir)
            continue

        for skill_md in sorted(skills_dir.rglob("SKILL.md")):
            skill_name = skill_md.parent.name

            if skill_md.stat().st_size > _MAX_SKILL_SIZE:
                logger.warning(
                    "Skipping skill '%s' — SKILL.md exceeds 10 MB limit",
                    skill_name,
                )
                continue

            content = skill_md.read_text(encoding="utf-8")
            relative = skill_md.relative_to(skills_dir)
            virtual_path = f"/skills/{relative.as_posix()}"

            # Discover supporting files in the same directory
            assets = _discover_assets(skill_md.parent, skills_dir)

            # Later dirs override earlier ones (last-wins per docs)
            skills[skill_name] = {
                "path": virtual_path,
                "disk_path": str(skill_md),
                "content": content,
                "assets": assets,
            }
            logger.debug(
                "Discovered skill: %s → %s (%d asset(s))",
                skill_name, virtual_path, len(assets),
            )

    logger.info(
        "Discovered %d skill(s): %s",
        len(skills),
        ", ".join(skills.keys()) or "(none)",
    )
    return skills


def _discover_assets(
    skill_dir: Path,
    skills_root: Path,
) -> list[dict[str, str]]:
    """Find all supporting files in a skill directory (excluding SKILL.md).

    Returns list of {path, disk_path, content} for each text asset.
    """
    assets: list[dict[str, str]] = []

    for file_path in sorted(skill_dir.rglob("*")):
        if not file_path.is_file():
            continue
        if file_path.name == "SKILL.md":
            continue
        if file_path.suffix.lower() not in _TEXT_EXTENSIONS:
            logger.debug("Skipping non-text asset: %s", file_path)
            continue
        if file_path.stat().st_size > _MAX_ASSET_SIZE:
            logger.warning("Skipping large asset: %s (>5 MB)", file_path)
            continue

        try:
            content = file_path.read_text(encoding="utf-8")
        except UnicodeDecodeError:
            logger.warning("Skipping non-UTF-8 asset: %s", file_path)
            continue

        relative = file_path.relative_to(skills_root)
        virtual_path = f"/skills/{relative.as_posix()}"

        assets.append({
            "path": virtual_path,
            "disk_path": str(file_path),
            "content": content,
        })

    return assets


def skills_to_files(
    skills: dict[str, dict[str, Any]],
) -> dict[str, str]:
    """Convert discovered skills into a files dict for agent invocation.

    Includes both SKILL.md files AND all supporting assets (scripts, docs).

    Returns:
        Dict of virtual_path -> content, ready to pass as the `files`
        parameter to invoke_agent / stream_agent.
    """
    files: dict[str, str] = {}
    for info in skills.values():
        # SKILL.md itself
        files[info["path"]] = info["content"]
        # Supporting assets (scripts, templates, docs, etc.)
        for asset in info.get("assets", []):
            files[asset["path"]] = asset["content"]
    return files


