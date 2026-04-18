"""Go binary subprocess manager.

Starts the wick_server binary, waits for it to be healthy, and cleans up on exit.
The binary is discovered via:
  1. Explicit path passed to GoRuntime
  2. WICK_SERVER_BINARY env var
  3. "wick_server" on PATH
  4. Known relative paths in the repo
"""

from __future__ import annotations

import atexit
import logging
import os
import shutil
import signal
import subprocess
import sys
import time

import httpx

logger = logging.getLogger("wick.runtime")

# Relative paths to check when running from the repo
_KNOWN_BINARIES = [
    "wick_deep_agent/server/bin/wick_server",
    "../wick_deep_agent/server/bin/wick_server",
]


class GoRuntime:
    """Manages the wick_server Go binary as a subprocess."""

    def __init__(
        self,
        binary: str | None = None,
        port: int = 8000,
        host: str = "127.0.0.1",
        env: dict[str, str] | None = None,
        cwd: str | None = None,
    ) -> None:
        self._binary = binary or self._find_binary()
        self._port = port
        self._host = host
        self._env = env
        self._cwd = cwd
        self._process: subprocess.Popen | None = None

    @property
    def port(self) -> int:
        return self._port

    @property
    def base_url(self) -> str:
        return f"http://{self._host}:{self._port}"

    def start(self) -> None:
        """Start the Go binary subprocess."""
        if self._process is not None:
            raise RuntimeError("Go runtime already started")

        cmd = [
            self._binary,
            f"--port={self._port}",
            f"--host={self._host}",
        ]

        env = {**os.environ, **(self._env or {})}
        logger.info("Starting Go binary: %s", " ".join(cmd))

        self._process = subprocess.Popen(
            cmd,
            env=env,
            cwd=self._cwd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        atexit.register(self.stop)

    def wait_ready(self, timeout: float = 15.0) -> None:
        """Poll /health until the Go server is ready."""
        url = f"{self.base_url}/health"
        deadline = time.monotonic() + timeout
        last_err = None

        while time.monotonic() < deadline:
            # Check if process died
            if self._process and self._process.poll() is not None:
                stderr = self._process.stderr.read().decode() if self._process.stderr else ""
                raise RuntimeError(
                    f"Go binary exited with code {self._process.returncode}: {stderr}"
                )

            try:
                resp = httpx.get(url, timeout=2.0)
                if resp.status_code == 200:
                    logger.info("Go server ready at %s", self.base_url)
                    return
            except (httpx.ConnectError, httpx.ReadTimeout) as e:
                last_err = e

            time.sleep(0.3)

        raise TimeoutError(
            f"Go server not ready after {timeout}s at {url}: {last_err}"
        )

    def stop(self) -> None:
        """Stop the Go binary subprocess."""
        if self._process is None:
            return

        logger.info("Stopping Go binary (pid=%d)", self._process.pid)
        try:
            self._process.terminate()
            self._process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            logger.warning("Go binary didn't stop, killing")
            self._process.kill()
            self._process.wait(timeout=2)
        finally:
            self._process = None

    def __enter__(self) -> GoRuntime:
        self.start()
        self.wait_ready()
        return self

    def __exit__(self, *exc: object) -> None:
        self.stop()

    @staticmethod
    def _find_binary() -> str:
        # 1. Environment variable
        env_bin = os.environ.get("WICK_SERVER_BINARY")
        if env_bin and os.path.isfile(env_bin):
            return env_bin

        # 2. On PATH
        on_path = shutil.which("wick_server")
        if on_path:
            return on_path

        # 3. Known relative paths
        for rel in _KNOWN_BINARIES:
            abspath = os.path.abspath(rel)
            if os.path.isfile(abspath):
                return abspath

        raise FileNotFoundError(
            "Could not find wick_server binary. Set WICK_SERVER_BINARY env var, "
            "place it on PATH, or pass binary= to Agent.run()."
        )
