#!/usr/bin/env python3
"""Cross-compile wick_go and build platform-specific wheels.

Usage:
    python build_wheels.py            # build all 4 platform wheels
    python build_wheels.py --current  # build only for current platform

Output: dist/*.whl (one wheel per platform)
"""

from __future__ import annotations

import argparse
import os
import platform
import shutil
import stat
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent
SERVER_DIR = ROOT / "server"
BIN_DIR = ROOT / "wick_deep_agent" / "bin"
DIST_DIR = ROOT / "dist"

TARGETS = [
    {
        "goos": "linux",
        "goarch": "amd64",
        "bin_name": "wick_go",
        "plat_tag": "manylinux2014_x86_64",
    },
    {
        "goos": "linux",
        "goarch": "arm64",
        "bin_name": "wick_go",
        "plat_tag": "manylinux2014_aarch64",
    },
    {
        "goos": "darwin",
        "goarch": "arm64",
        "bin_name": "wick_go",
        "plat_tag": "macosx_11_0_arm64",
    },
    {
        "goos": "windows",
        "goarch": "amd64",
        "bin_name": "wick_go.exe",
        "plat_tag": "win_amd64",
    },
]


def _current_target() -> dict[str, str]:
    """Return the target dict matching the current machine."""
    system = platform.system().lower()
    machine = platform.machine().lower()

    if system == "darwin" and machine in ("arm64", "aarch64"):
        return TARGETS[2]
    if system == "linux" and machine in ("x86_64", "amd64"):
        return TARGETS[0]
    if system == "linux" and machine in ("arm64", "aarch64"):
        return TARGETS[1]
    if system == "windows" and machine in ("amd64", "x86_64", "amd64"):
        return TARGETS[3]
    raise RuntimeError(f"Unsupported platform: {system}/{machine}")


def go_build(target: dict[str, str]) -> Path:
    """Cross-compile the Go binary for the given target."""
    out_path = BIN_DIR / target["bin_name"]

    env = os.environ.copy()
    env["CGO_ENABLED"] = "0"
    env["GOOS"] = target["goos"]
    env["GOARCH"] = target["goarch"]

    cmd = [
        "go", "build",
        "-ldflags=-s -w",
        "-o", str(out_path),
        ".",
    ]
    print(f"  go build → {target['goos']}/{target['goarch']}")
    result = subprocess.run(cmd, cwd=str(SERVER_DIR), env=env, capture_output=True, text=True)
    if result.returncode != 0:
        raise RuntimeError(f"go build failed for {target['goos']}/{target['goarch']}:\n{result.stderr}")

    # Set executable bit (no-op on Windows targets built from Unix)
    if target["goos"] != "windows":
        out_path.chmod(out_path.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)

    return out_path


def build_wheel(plat_tag: str) -> Path:
    """Build a wheel tagged for the given platform."""
    # Clean previous builds
    build_dir = ROOT / "build"
    if build_dir.exists():
        shutil.rmtree(build_dir)

    cmd = [
        sys.executable, "-m", "pip", "wheel",
        "--no-deps",
        "--wheel-dir", str(DIST_DIR),
        str(ROOT),
    ]
    print(f"  building wheel ...")
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        raise RuntimeError(f"pip wheel failed:\n{result.stderr}")

    # Find the wheel that was just built (it has the generic tag)
    wheels = sorted(DIST_DIR.glob("wick_deep_agent-*.whl"), key=lambda p: p.stat().st_mtime)
    if not wheels:
        raise RuntimeError("No wheel found after build")
    generic_whl = wheels[-1]

    # Retag the wheel with the correct platform tag
    cmd_retag = [
        sys.executable, "-m", "wheel", "tags",
        "--platform-tag", plat_tag,
        "--remove",
        str(generic_whl),
    ]
    result = subprocess.run(cmd_retag, capture_output=True, text=True)
    if result.returncode != 0:
        raise RuntimeError(f"wheel tags failed:\n{result.stderr}")

    # Find the retagged wheel
    tagged_wheels = sorted(DIST_DIR.glob(f"wick_deep_agent-*{plat_tag}*.whl"))
    if not tagged_wheels:
        raise RuntimeError(f"No retagged wheel found for {plat_tag}")

    return tagged_wheels[-1]


def clean_bin() -> None:
    """Remove any binary from the bin dir (keep .gitkeep)."""
    for f in BIN_DIR.iterdir():
        if f.name != ".gitkeep":
            f.unlink()


def main() -> None:
    parser = argparse.ArgumentParser(description="Build platform-specific wheels for wick-deep-agent")
    parser.add_argument("--current", action="store_true", help="Build only for the current platform")
    args = parser.parse_args()

    if not SERVER_DIR.exists() or not (SERVER_DIR / "main.go").exists():
        print(f"Error: Go source not found at {SERVER_DIR}", file=sys.stderr)
        sys.exit(1)

    DIST_DIR.mkdir(parents=True, exist_ok=True)
    BIN_DIR.mkdir(parents=True, exist_ok=True)

    targets = [_current_target()] if args.current else TARGETS

    for target in targets:
        tag = target["plat_tag"]
        print(f"\n[{tag}]")

        clean_bin()
        binary = go_build(target)
        print(f"  binary: {binary} ({binary.stat().st_size / 1024 / 1024:.1f} MB)")

        whl = build_wheel(tag)
        print(f"  wheel:  {whl.name}")

    clean_bin()
    print(f"\nDone. Wheels in {DIST_DIR}/")


if __name__ == "__main__":
    main()
