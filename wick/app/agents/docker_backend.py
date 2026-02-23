"""Docker sandbox backend for skill script execution.

Runs commands inside a Docker container via ``docker exec``.
Extends ``BaseSandbox`` so that all file operations (ls_info, read, write,
edit, grep_raw, glob_info) are automatically implemented by generating shell
commands and calling ``self.execute()``.

Supports both **local** and **remote** Docker daemons:
  - Local mode (default): container must be pre-started via docker compose.
  - Remote mode (``docker_host`` set): container is launched on-demand on the
    remote daemon and all CLI commands are prefixed with ``-H <url>``.

Only four abstract members need implementing:
  - execute()        → docker exec <container> sh -c '<command>'
  - id               → container_name
  - upload_files()   → docker exec + base64 stdin → container
  - download_files() → docker exec + base64 stdout → host
"""

from __future__ import annotations

import asyncio
import base64
import logging
import os
import subprocess
import time
from pathlib import Path

from deepagents.backends.protocol import (
    ExecuteResponse,
    FileDownloadResponse,
    FileUploadResponse,
)
from deepagents.backends.sandbox import BaseSandbox

logger = logging.getLogger(__name__)


class DockerSandboxBackend(BaseSandbox):
    """Sandbox backend that executes commands inside a Docker container.

    Local mode (``docker_host=None``):
        The container must already be running (e.g. via ``docker compose up -d``).
        A health check at construction time ensures the container is reachable.

    Remote mode (``docker_host`` set, e.g. ``tcp://192.168.1.50:2375``):
        A container is launched on-demand on the remote daemon. All Docker CLI
        commands are prefixed with ``-H <url>``.
    """

    def __init__(
        self,
        container_name: str = "wick-skills-sandbox",
        workdir: str = "/workspace",
        timeout: float = 120.0,
        max_output_bytes: int = 100_000,
        docker_host: str | None = None,
        image: str = "python:3.11-slim",
        username: str = "local",
    ) -> None:
        self._container_name = container_name
        self._workdir = workdir
        self._timeout = timeout
        self._max_output_bytes = max_output_bytes
        self._docker_host = docker_host
        self._image = image
        self._username = username

        # Container status tracking (lazy launch — no _ensure_container() here)
        self._container_status: str = "idle"   # idle | launching | launched | error
        self._container_error: str | None = None
        self._launch_task: asyncio.Task | None = None  # tracked for cancellation
        self._launch_lock: asyncio.Lock | None = None  # created lazily in async context

    # ── Container status properties ───────────────────────────────────────

    @property
    def container_status(self) -> str:
        return self._container_status

    @property
    def container_error(self) -> str | None:
        return self._container_error

    def cancel_launch(self) -> None:
        """Cancel any in-flight launch task (called before teardown)."""
        if self._launch_task and not self._launch_task.done():
            self._launch_task.cancel()
            self._launch_task = None

    async def launch_container_async(self) -> None:
        """Launch container in background thread, broadcast status via SSE."""
        import asyncio
        import app.events as events

        # Lazy-init lock (must be created inside a running event loop)
        if self._launch_lock is None:
            self._launch_lock = asyncio.Lock()

        async with self._launch_lock:
            if self._container_status == "launched":
                return  # already running

            self._container_status = "launching"
            self._container_error = None
            await events._broadcast("container_status", username=self._username)

            try:
                await asyncio.to_thread(self._ensure_container)
                self._container_status = "launched"
            except asyncio.CancelledError:
                self._container_status = "idle"
                return
            except Exception as e:
                self._container_status = "error"
                self._container_error = str(e)

            await events._broadcast("container_status", username=self._username)

    # ── Docker CLI helper ────────────────────────────────────────────────

    def _docker_cmd(self, *args: str) -> list[str]:
        """Build a docker CLI command, optionally targeting a remote host."""
        cmd = ["docker"]
        if self._docker_host:
            cmd.extend(["-H", self._docker_host])
        cmd.extend(args)
        return cmd

    # ── Container lifecycle ──────────────────────────────────────────────

    def _ensure_container(self) -> None:
        """Ensure the sandbox container exists and is running.

        Checks if the container is already running. If not, launches it
        on-demand (on the local daemon or remote host if docker_host is set).
        """
        max_retries = int(os.environ.get("SANDBOX_HEALTH_RETRIES", "1"))
        delay = float(os.environ.get("SANDBOX_HEALTH_DELAY", "2"))

        for attempt in range(1, max_retries + 1):
            try:
                result = subprocess.run(
                    self._docker_cmd(
                        "inspect", "--format", "{{.State.Running}}", self._container_name,
                    ),
                    capture_output=True,
                    text=True,
                    timeout=10,
                    check=False,
                )
                if result.returncode == 0 and "true" in result.stdout.lower():
                    logger.info(
                        "Docker sandbox container '%s' is running (attempt %d/%d)",
                        self._container_name, attempt, max_retries,
                    )
                    return
                msg = (
                    f"Docker container '{self._container_name}' is not running "
                    f"(attempt {attempt}/{max_retries})."
                )
            except FileNotFoundError:
                raise RuntimeError(
                    "Docker CLI not found. Install Docker to use the docker sandbox backend."
                ) from None
            except subprocess.TimeoutExpired:
                msg = (
                    f"Timed out checking Docker container '{self._container_name}' "
                    f"(attempt {attempt}/{max_retries})."
                )

            if attempt < max_retries:
                logger.info("%s Retrying in %gs…", msg, delay)
                time.sleep(delay)
            else:
                break

        # Container is not running — launch on-demand
        target = self._docker_host or "local daemon"
        logger.info(
            "Launching sandbox container '%s' on %s...",
            self._container_name, target,
        )

        # Remove stale container if exists (stopped/exited)
        subprocess.run(
            self._docker_cmd("rm", "-f", self._container_name),
            capture_output=True, timeout=10, check=False,
        )

        # Create and start
        subprocess.run(
            self._docker_cmd(
                "run", "-d",
                "--name", self._container_name,
                "-w", self._workdir,
                self._image,
                "sleep", "infinity",
            ),
            capture_output=True, text=True, timeout=60, check=True,
        )
        logger.info(
            "Sandbox container '%s' launched on %s",
            self._container_name, target,
        )

    def stop_container(self) -> None:
        """Stop and remove the on-demand container."""
        subprocess.run(
            self._docker_cmd("rm", "-f", self._container_name),
            capture_output=True, timeout=10, check=False,
        )
        self._container_status = "idle"
        self._container_error = None

    # ── Config property ──────────────────────────────────────────────────

    @property
    def sandbox_url(self) -> str | None:
        return self._docker_host

    # ── Abstract: id ─────────────────────────────────────────────────────

    @property
    def id(self) -> str:
        return self._container_name

    # ── Abstract: execute ────────────────────────────────────────────────

    def execute(self, command: str) -> ExecuteResponse:
        """Execute a command inside the Docker container via ``docker exec``."""
        if not command or not isinstance(command, str):
            return ExecuteResponse(
                output="Error: Command must be a non-empty string.",
                exit_code=1,
                truncated=False,
            )

        # Ensure container is ready (waits for "launching", lazy-launches from "idle")
        try:
            self._wait_for_container()
        except RuntimeError as e:
            return ExecuteResponse(
                output=f"Error: {e}",
                exit_code=1,
                truncated=False,
            )

        try:
            result = subprocess.run(
                self._docker_cmd(
                    "exec", "-w", self._workdir,
                    self._container_name,
                    "sh", "-c", command,
                ),
                capture_output=True,
                text=True,
                timeout=self._timeout,
                check=False,
            )

            # Combine stdout and stderr (same pattern as LocalShellBackend)
            output_parts = []
            if result.stdout:
                output_parts.append(result.stdout)
            if result.stderr:
                stderr_lines = result.stderr.strip().split("\n")
                output_parts.extend(f"[stderr] {line}" for line in stderr_lines)

            output = "\n".join(output_parts) if output_parts else "<no output>"

            # Truncate if needed
            truncated = False
            if len(output) > self._max_output_bytes:
                output = output[: self._max_output_bytes]
                output += f"\n\n... Output truncated at {self._max_output_bytes} bytes."
                truncated = True

            # Append exit code info if non-zero
            if result.returncode != 0:
                output = f"{output.rstrip()}\n\nExit code: {result.returncode}"

            return ExecuteResponse(
                output=output,
                exit_code=result.returncode,
                truncated=truncated,
            )

        except subprocess.TimeoutExpired:
            return ExecuteResponse(
                output=f"Error: Command timed out after {self._timeout:.1f} seconds.",
                exit_code=124,
                truncated=False,
            )
        except Exception as e:  # noqa: BLE001
            return ExecuteResponse(
                output=f"Error executing command in container: {e}",
                exit_code=1,
                truncated=False,
            )

    # ── Abstract: upload_files ───────────────────────────────────────────

    def _wait_for_container(self) -> None:
        """Block until the container is launched, with a timeout.

        Handles the case where the agent calls execute/upload/download
        while the container is still launching.
        """
        if self._container_status == "launched":
            return
        if self._container_status == "idle":
            self._ensure_container()
            self._container_status = "launched"
            return
        if self._container_status == "launching":
            # Poll until launched or timeout (max 60s)
            for _ in range(120):
                time.sleep(0.5)
                if self._container_status == "launched":
                    return
                if self._container_status in ("error", "idle"):
                    break
        raise RuntimeError(
            f"Container not available (status: {self._container_status}). "
            f"{self._container_error or ''}"
        )

    def upload_files(
        self, files: list[tuple[str, bytes]]
    ) -> list[FileUploadResponse]:
        """Upload files to the container via ``docker exec`` + base64.

        Uses ``docker exec`` instead of ``docker cp`` so that it works even
        when wick-agent itself runs inside a container with a mounted socket
        (docker cp reads paths from the host filesystem, not the caller).
        """
        self._wait_for_container()
        responses: list[FileUploadResponse] = []
        for dest_path, content in files:
            try:
                # Ensure parent directory exists inside container
                parent = str(Path(dest_path).parent)
                subprocess.run(
                    self._docker_cmd(
                        "exec", self._container_name,
                        "mkdir", "-p", parent,
                    ),
                    capture_output=True,
                    timeout=10,
                    check=True,
                )

                # Pipe base64-encoded content via stdin → decode inside container
                content_b64 = base64.b64encode(content).decode("ascii")
                subprocess.run(
                    self._docker_cmd(
                        "exec", "-i", self._container_name,
                        "sh", "-c", f"base64 -d > '{dest_path}'",
                    ),
                    input=content_b64,
                    capture_output=True,
                    text=True,
                    timeout=30,
                    check=True,
                )
                responses.append(FileUploadResponse(path=dest_path, error=None))

            except Exception as e:  # noqa: BLE001
                logger.warning("upload_files failed for %s: %s", dest_path, e)
                responses.append(
                    FileUploadResponse(path=dest_path, error="permission_denied")
                )
        return responses

    # ── Abstract: download_files ─────────────────────────────────────────

    def download_files(
        self, paths: list[str]
    ) -> list[FileDownloadResponse]:
        """Download files from the container via ``docker exec`` + base64.

        Uses ``docker exec`` instead of ``docker cp`` so that it works even
        when wick-agent itself runs inside a container with a mounted socket.
        """
        self._wait_for_container()
        responses: list[FileDownloadResponse] = []
        for src_path in paths:
            try:
                result = subprocess.run(
                    self._docker_cmd(
                        "exec", self._container_name,
                        "sh", "-c", f"base64 '{src_path}'",
                    ),
                    capture_output=True,
                    text=True,
                    timeout=30,
                    check=True,
                )
                content = base64.b64decode(result.stdout.strip())
                responses.append(
                    FileDownloadResponse(
                        path=src_path, content=content, error=None
                    )
                )

            except subprocess.CalledProcessError:
                responses.append(
                    FileDownloadResponse(
                        path=src_path, content=None, error="file_not_found"
                    )
                )
            except Exception as e:  # noqa: BLE001
                logger.warning("download_files failed for %s: %s", src_path, e)
                responses.append(
                    FileDownloadResponse(
                        path=src_path, content=None, error="permission_denied"
                    )
                )
        return responses


__all__ = ["DockerSandboxBackend"]
