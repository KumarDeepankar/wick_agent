"""Docker sandbox backend for skill script execution.

Runs commands inside an always-running Docker container via ``docker exec``.
Extends ``BaseSandbox`` so that all file operations (ls_info, read, write,
edit, grep_raw, glob_info) are automatically implemented by generating shell
commands and calling ``self.execute()``.

Only four abstract members need implementing:
  - execute()        → docker exec <container> sh -c '<command>'
  - id               → container_name
  - upload_files()   → docker cp host → container
  - download_files() → docker cp container → host
"""

from __future__ import annotations

import logging
import os
import subprocess
import tempfile
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

    The container must already be running (e.g. via ``docker compose up -d``).
    A health check at construction time ensures the container is reachable.
    """

    def __init__(
        self,
        container_name: str = "wick-skills-sandbox",
        workdir: str = "/workspace",
        timeout: float = 120.0,
        max_output_bytes: int = 100_000,
    ) -> None:
        self._container_name = container_name
        self._workdir = workdir
        self._timeout = timeout
        self._max_output_bytes = max_output_bytes

        self._check_container_health()

    # ── Health check ──────────────────────────────────────────────────────

    def _check_container_health(self) -> None:
        """Verify the Docker container exists and is running."""
        try:
            result = subprocess.run(
                [
                    "docker", "inspect",
                    "--format", "{{.State.Running}}",
                    self._container_name,
                ],
                capture_output=True,
                text=True,
                timeout=10,
                check=False,
            )
            if result.returncode != 0 or "true" not in result.stdout.lower():
                raise RuntimeError(
                    f"Docker container '{self._container_name}' is not running. "
                    f"Start it with: docker compose up -d skills-sandbox"
                )
            logger.info(
                "Docker sandbox container '%s' is running", self._container_name
            )
        except FileNotFoundError:
            raise RuntimeError(
                "Docker CLI not found. Install Docker to use the docker sandbox backend."
            ) from None
        except subprocess.TimeoutExpired:
            raise RuntimeError(
                f"Timed out checking Docker container '{self._container_name}'."
            ) from None

    # ── Abstract: id ──────────────────────────────────────────────────────

    @property
    def id(self) -> str:
        return self._container_name

    # ── Abstract: execute ─────────────────────────────────────────────────

    def execute(self, command: str) -> ExecuteResponse:
        """Execute a command inside the Docker container via ``docker exec``."""
        if not command or not isinstance(command, str):
            return ExecuteResponse(
                output="Error: Command must be a non-empty string.",
                exit_code=1,
                truncated=False,
            )

        try:
            result = subprocess.run(
                [
                    "docker", "exec",
                    "-w", self._workdir,
                    self._container_name,
                    "sh", "-c", command,
                ],
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

    # ── Abstract: upload_files ────────────────────────────────────────────

    def upload_files(
        self, files: list[tuple[str, bytes]]
    ) -> list[FileUploadResponse]:
        """Upload files to the container via ``docker cp``."""
        responses: list[FileUploadResponse] = []
        for dest_path, content in files:
            try:
                # Ensure parent directory exists inside container
                parent = str(Path(dest_path).parent)
                subprocess.run(
                    [
                        "docker", "exec",
                        self._container_name,
                        "mkdir", "-p", parent,
                    ],
                    capture_output=True,
                    timeout=10,
                    check=True,
                )

                # Write content to temp file, then docker cp into container
                with tempfile.NamedTemporaryFile(delete=False) as tmp:
                    tmp.write(content)
                    tmp_path = tmp.name

                try:
                    subprocess.run(
                        [
                            "docker", "cp",
                            tmp_path,
                            f"{self._container_name}:{dest_path}",
                        ],
                        capture_output=True,
                        timeout=30,
                        check=True,
                    )
                    responses.append(FileUploadResponse(path=dest_path, error=None))
                finally:
                    os.unlink(tmp_path)

            except Exception as e:  # noqa: BLE001
                logger.warning("upload_files failed for %s: %s", dest_path, e)
                responses.append(
                    FileUploadResponse(path=dest_path, error="permission_denied")
                )
        return responses

    # ── Abstract: download_files ──────────────────────────────────────────

    def download_files(
        self, paths: list[str]
    ) -> list[FileDownloadResponse]:
        """Download files from the container via ``docker cp``."""
        responses: list[FileDownloadResponse] = []
        for src_path in paths:
            try:
                with tempfile.NamedTemporaryFile(delete=False) as tmp:
                    tmp_path = tmp.name

                try:
                    subprocess.run(
                        [
                            "docker", "cp",
                            f"{self._container_name}:{src_path}",
                            tmp_path,
                        ],
                        capture_output=True,
                        timeout=30,
                        check=True,
                    )
                    content = Path(tmp_path).read_bytes()
                    responses.append(
                        FileDownloadResponse(
                            path=src_path, content=content, error=None
                        )
                    )
                finally:
                    os.unlink(tmp_path)

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
