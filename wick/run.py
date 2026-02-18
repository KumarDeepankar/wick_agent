"""Entry point to run the Wick Agent server.

Automatically ensures the Docker skills-sandbox container is built and running
before starting the FastAPI server. Safe to run on any machine with Docker installed.
"""

import subprocess
import sys

import uvicorn

from app.config import settings

CONTAINER_NAME = "wick-skills-sandbox"


def _docker_available() -> bool:
    """Check if Docker CLI is installed and the daemon is reachable."""
    try:
        result = subprocess.run(
            ["docker", "info"],
            capture_output=True,
            timeout=10,
        )
        return result.returncode == 0
    except (FileNotFoundError, subprocess.TimeoutExpired):
        return False


def _container_running(name: str) -> bool:
    """Check if a container with the given name is running."""
    result = subprocess.run(
        ["docker", "inspect", "--format", "{{.State.Running}}", name],
        capture_output=True,
        text=True,
        timeout=10,
        check=False,
    )
    return result.returncode == 0 and "true" in result.stdout.lower()


def _container_exists(name: str) -> bool:
    """Check if a container with the given name exists (running or stopped)."""
    result = subprocess.run(
        ["docker", "inspect", name],
        capture_output=True,
        timeout=10,
        check=False,
    )
    return result.returncode == 0


def ensure_sandbox():
    """Build (if needed) and start the skills-sandbox container."""
    if not _docker_available():
        print("[sandbox] Docker not available — skipping sandbox setup.")
        print("[sandbox] Install Docker to enable skill script execution.")
        return False

    if _container_running(CONTAINER_NAME):
        print(f"[sandbox] Container '{CONTAINER_NAME}' is already running.")
        return True

    # Container exists but stopped — just start it
    if _container_exists(CONTAINER_NAME):
        print(f"[sandbox] Starting stopped container '{CONTAINER_NAME}'...")
        result = subprocess.run(
            ["docker", "start", CONTAINER_NAME],
            capture_output=True,
            text=True,
            timeout=30,
        )
        if result.returncode == 0:
            print(f"[sandbox] Container '{CONTAINER_NAME}' started.")
            return True
        print(f"[sandbox] Failed to start: {result.stderr.strip()}")
        return False

    # Build and start via docker compose
    print(f"[sandbox] Building and starting '{CONTAINER_NAME}'...")
    result = subprocess.run(
        ["docker", "compose", "up", "-d", "--build", "skills-sandbox"],
        capture_output=True,
        text=True,
        timeout=300,
    )
    if result.returncode == 0:
        print(f"[sandbox] Container '{CONTAINER_NAME}' is running.")
        return True

    # Fallback: try docker-compose (older installations)
    result = subprocess.run(
        ["docker-compose", "up", "-d", "--build", "skills-sandbox"],
        capture_output=True,
        text=True,
        timeout=300,
        check=False,
    )
    if result.returncode == 0:
        print(f"[sandbox] Container '{CONTAINER_NAME}' is running.")
        return True

    print(f"[sandbox] Failed to start container:")
    print(f"  {result.stderr.strip()}")
    return False


if __name__ == "__main__":
    sandbox_ok = ensure_sandbox()
    if not sandbox_ok:
        print("[sandbox] WARNING: Sandbox not running. 'execute' tool will fail.")
        print("[sandbox] The server will start anyway for non-sandbox features.\n")

    uvicorn.run(
        "app.main:app",
        host=settings.host,
        port=settings.port,
        reload=True,
    )
