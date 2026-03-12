#!/usr/bin/env python3
"""Platform-independent startup script for wick_py.

Builds the Docker image and runs the container with Docker socket
access so that "Remote Docker" sandbox mode works on any OS.

Usage:
    python start.py                          # default (Ollama + Claude agents)
    python start.py --example simple_agent   # run a specific example
    python start.py --port 9000              # custom host port
    python start.py --build-only             # just build, don't run
    python start.py --no-build               # skip build, just run
    python start.py --no-sandbox             # skip Docker socket mount
    python start.py --env OPENAI_API_KEY=sk-... --env MY_VAR=val
"""

from __future__ import annotations

import argparse
import os
import platform
import shutil
import subprocess
import sys

IMAGE_NAME = "wick-py"
CONTAINER_NAME = "wick-py"
DOCKERFILE = "wick_py/Dockerfile"
GO_PORT = 8000
SIDECAR_PORT = 9100


def find_repo_root() -> str:
    """Walk up from this script to find the repo root (contains wick_py/ and wick_deep_agent/)."""
    d = os.path.dirname(os.path.abspath(__file__))
    for _ in range(5):
        if os.path.isdir(os.path.join(d, "wick_deep_agent")) and os.path.isdir(
            os.path.join(d, "wick_py")
        ):
            return d
        d = os.path.dirname(d)
    # Fallback: assume script is in wick_py/
    return os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def check_docker():
    if not shutil.which("docker"):
        print("Error: Docker is not installed or not on PATH.", file=sys.stderr)
        sys.exit(1)
    ret = subprocess.run(
        ["docker", "info"], capture_output=True, text=True
    )
    if ret.returncode != 0:
        print("Error: Docker daemon is not running.", file=sys.stderr)
        sys.exit(1)


def docker_socket_mount() -> list[str]:
    """Return the -v flag to mount the host Docker socket into the container.

    Works on:
      - macOS:   /var/run/docker.sock (Docker Desktop)
      - Linux:   /var/run/docker.sock (native Docker)
      - Windows: //./pipe/docker_engine (Docker Desktop with WSL2/Hyper-V)
                 Mounted as /var/run/docker.sock inside the Linux container.
    """
    system = platform.system().lower()

    if system in ("darwin", "linux"):
        sock = "/var/run/docker.sock"
        if os.path.exists(sock):
            return ["-v", f"{sock}:/var/run/docker.sock"]
        print(f"Warning: Docker socket not found at {sock}", file=sys.stderr)
        return []

    if system == "windows":
        # Docker Desktop on Windows exposes a named pipe.
        # Docker CLI translates this mount into the Linux VM automatically.
        return ["-v", "//./pipe/docker_engine:/var/run/docker.sock"]

    print(f"Warning: Unknown OS '{system}', skipping Docker socket mount.", file=sys.stderr)
    return []


def build(repo_root: str):
    print(f"Building image '{IMAGE_NAME}'...")
    cmd = [
        "docker", "build",
        "-f", DOCKERFILE,
        "-t", IMAGE_NAME,
        ".",
    ]
    ret = subprocess.run(cmd, cwd=repo_root)
    if ret.returncode != 0:
        print("Build failed.", file=sys.stderr)
        sys.exit(1)
    print(f"Image '{IMAGE_NAME}' built successfully.\n")


def stop_existing():
    """Stop and remove any existing container with the same name."""
    subprocess.run(
        ["docker", "rm", "-f", CONTAINER_NAME],
        capture_output=True,
    )


def run(
    port: int,
    example: str | None,
    env_vars: list[str],
    detach: bool,
    sandbox: bool,
):
    stop_existing()

    cmd = [
        "docker", "run",
        "--name", CONTAINER_NAME,
        "-p", f"{port}:{GO_PORT}",
        "-p", f"{SIDECAR_PORT}:{SIDECAR_PORT}",
    ]

    # Mount Docker socket so the Go server can launch sandbox containers.
    # The Go DockerBackend runs `docker run/exec` — when dockerHost is empty
    # (sandbox_url=null in the UI), it uses the default socket.
    if sandbox:
        mount = docker_socket_mount()
        if mount:
            cmd.extend(mount)
            print("  Docker socket mounted (Remote Docker sandbox enabled)")
        else:
            print("  Warning: Could not mount Docker socket — Remote Docker will not work")

    # Pass through common API key env vars if set locally
    auto_env_keys = [
        "ANTHROPIC_API_KEY",
        "OPENAI_API_KEY",
        "OLLAMA_HOST",
    ]
    for key in auto_env_keys:
        val = os.environ.get(key)
        if val:
            cmd.extend(["-e", f"{key}={val}"])

    # Pass explicit --env flags
    for ev in env_vars:
        cmd.extend(["-e", ev])

    if detach:
        cmd.append("-d")
    else:
        cmd.append("--rm")

    cmd.append(IMAGE_NAME)

    # Override CMD if a specific example is requested
    if example:
        if not example.endswith(".py"):
            example += ".py"
        cmd.append("python")
        cmd.append(f"/app/examples/{example}")

    print(f"\nStarting container '{CONTAINER_NAME}'...")
    print(f"  Go server:  http://localhost:{port}")
    print(f"  Sidecar:    http://localhost:{SIDECAR_PORT}")
    if sandbox:
        print(f"\n  Remote Docker: select in UI settings with sandbox_url = empty")
        print(f"  The Go server uses the mounted socket — no TCP daemon needed.")
    print()

    try:
        ret = subprocess.run(cmd)
        sys.exit(ret.returncode)
    except KeyboardInterrupt:
        print("\nStopping...")
        subprocess.run(["docker", "stop", CONTAINER_NAME], capture_output=True)


def main():
    parser = argparse.ArgumentParser(description="Build and run wick_py in Docker")
    parser.add_argument(
        "--port", type=int, default=GO_PORT,
        help=f"Host port for the Go server (default: {GO_PORT})",
    )
    parser.add_argument(
        "--example", type=str, default=None,
        help="Example to run (e.g. 'simple_agent' or 'custom_llm'). Default: custom_llm",
    )
    parser.add_argument(
        "--env", "-e", action="append", default=[],
        help="Extra environment variables (KEY=VALUE), can be repeated",
    )
    parser.add_argument(
        "--build-only", action="store_true",
        help="Only build the image, don't run",
    )
    parser.add_argument(
        "--no-build", action="store_true",
        help="Skip building, just run the existing image",
    )
    parser.add_argument(
        "--no-sandbox", action="store_true",
        help="Don't mount the Docker socket (disables Remote Docker mode)",
    )
    parser.add_argument(
        "--detach", "-d", action="store_true",
        help="Run container in background",
    )

    args = parser.parse_args()
    repo_root = find_repo_root()

    check_docker()

    if not args.no_build:
        build(repo_root)

    if args.build_only:
        return

    run(
        port=args.port,
        example=args.example,
        env_vars=args.env,
        detach=args.detach,
        sandbox=not args.no_sandbox,
    )


if __name__ == "__main__":
    main()
