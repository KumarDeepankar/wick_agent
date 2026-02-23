"""Local sandbox backend for skill script execution.

Runs commands directly on the host machine via ``subprocess``.
No Docker required — ideal for development and testing.

Extends ``BaseSandbox`` so that all file operations (ls_info, read, write,
edit, grep_raw, glob_info) are automatically implemented by generating shell
commands and calling ``self.execute()``.
"""

from __future__ import annotations

import logging
import subprocess
from pathlib import Path

from deepagents.backends.protocol import (
    ExecuteResponse,
    FileDownloadResponse,
    FileUploadResponse,
)
from deepagents.backends.sandbox import BaseSandbox

logger = logging.getLogger(__name__)


class LocalSandboxBackend(BaseSandbox):
    """Sandbox backend that executes commands directly on the host machine.

    Commands run via ``sh -c`` in the configured working directory.
    File uploads/downloads are simple filesystem reads/writes.
    """

    def __init__(
        self,
        workdir: str = "/workspace",
        timeout: float = 120.0,
        max_output_bytes: int = 100_000,
        username: str = "local",
    ) -> None:
        # Scope workdir per user to prevent cross-user file collisions
        self._workdir = str(Path(workdir) / username)
        self._timeout = timeout
        self._max_output_bytes = max_output_bytes
        self._username = username

        # Ensure workdir exists
        Path(self._workdir).mkdir(parents=True, exist_ok=True)
        logger.info("Local sandbox backend ready (workdir=%s, user=%s)", self._workdir, username)

    # ── Abstract: id ─────────────────────────────────────────────────────

    @property
    def id(self) -> str:
        return "local"

    # ── Abstract: execute ────────────────────────────────────────────────

    def execute(self, command: str) -> ExecuteResponse:
        """Execute a command on the host via ``sh -c``."""
        if not command or not isinstance(command, str):
            return ExecuteResponse(
                output="Error: Command must be a non-empty string.",
                exit_code=1,
                truncated=False,
            )

        try:
            result = subprocess.run(
                ["sh", "-c", command],
                capture_output=True,
                text=True,
                timeout=self._timeout,
                cwd=self._workdir,
                check=False,
            )

            output_parts = []
            if result.stdout:
                output_parts.append(result.stdout)
            if result.stderr:
                stderr_lines = result.stderr.strip().split("\n")
                output_parts.extend(f"[stderr] {line}" for line in stderr_lines)

            output = "\n".join(output_parts) if output_parts else "<no output>"

            truncated = False
            if len(output) > self._max_output_bytes:
                output = output[: self._max_output_bytes]
                output += f"\n\n... Output truncated at {self._max_output_bytes} bytes."
                truncated = True

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
                output=f"Error executing command: {e}",
                exit_code=1,
                truncated=False,
            )

    # ── Abstract: upload_files ───────────────────────────────────────────

    def upload_files(
        self, files: list[tuple[str, bytes]]
    ) -> list[FileUploadResponse]:
        """Write files directly to the host filesystem."""
        responses: list[FileUploadResponse] = []
        for dest_path, content in files:
            try:
                p = Path(dest_path)
                p.parent.mkdir(parents=True, exist_ok=True)
                p.write_bytes(content)
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
        """Read files directly from the host filesystem."""
        responses: list[FileDownloadResponse] = []
        for src_path in paths:
            try:
                p = Path(src_path)
                if not p.exists():
                    responses.append(
                        FileDownloadResponse(
                            path=src_path, content=None, error="file_not_found"
                        )
                    )
                    continue
                content = p.read_bytes()
                responses.append(
                    FileDownloadResponse(
                        path=src_path, content=content, error=None
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


__all__ = ["LocalSandboxBackend"]
