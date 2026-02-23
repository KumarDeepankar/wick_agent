"""WickServer — lifecycle manager for the wick_go agent server.

    from wick_deep_agent import WickServer

    # Zero-config start (no agents pre-loaded, add via API):
    server = WickServer(port=8003)

    # Inline agent config (no YAML file needed):
    server = WickServer(port=8003, agents={
        "default": {
            "name": "Ollama Local",
            "model": "ollama:llama3.1:8b",
            "system_prompt": "You are a helpful assistant.",
        }
    })

    server.build()
    server.start()
    server.wait_ready()
    server.stop()

    # Or as a context manager:
    with WickServer(port=8003) as srv:
        print(srv.status())
"""

from __future__ import annotations

import json
import os
import signal
import subprocess
import time
from pathlib import Path
from typing import Any


STATE_DIR = Path.home() / ".wick_deep_agent"
DEFAULT_PID_FILE = STATE_DIR / "wick_go.pid"
DEFAULT_LOG_FILE = STATE_DIR / "wick_go.log"

_PACKAGE_DIR = Path(__file__).resolve().parent
SERVER_DIR = _PACKAGE_DIR.parent / "server"


class WickServer:
    """Manages the wick_go server process.

    Config resolution (first match wins):
        1. ``config="/path/to/agents.yaml"`` — explicit file path
        2. ``agents={...}`` — Python dict, auto-written to a temp file
        3. ``WICK_CONFIG`` env var
        4. No config — server starts with zero agents (add via REST API)
    """

    def __init__(
        self,
        binary: str | None = None,
        config: str | None = None,
        agents: dict[str, Any] | None = None,
        defaults: dict[str, Any] | None = None,
        port: int = 8000,
        host: str = "0.0.0.0",
        env: dict[str, str] | None = None,
        log_file: str | None = None,
    ) -> None:
        self.port = port
        self.host = host
        self.extra_env = env or {}
        self.log_path = Path(log_file) if log_file else DEFAULT_LOG_FILE

        self.binary = self._resolve_binary(binary)
        self.config = self._resolve_config(config, agents, defaults)

        self._log_fh: Any = None
        self._owns_config = config is None and agents is not None

    # -- Resolution ----------------------------------------------------------

    @staticmethod
    def _resolve_binary(explicit: str | None) -> str:
        if explicit:
            return explicit
        from_env = os.environ.get("WICK_GO_BINARY")
        if from_env:
            return from_env
        built = SERVER_DIR / "wick_go"
        if built.exists():
            return str(built)
        raise FileNotFoundError(
            "wick_go binary not found. Run WickServer.build() first, "
            "set WICK_GO_BINARY, or pass binary= explicitly."
        )

    @staticmethod
    def _resolve_config(
        explicit: str | None,
        agents: dict[str, Any] | None,
        defaults: dict[str, Any] | None,
    ) -> str:
        # 1. Explicit file path
        if explicit:
            return explicit

        # 2. Inline agents dict → write temp config (JSON is valid YAML)
        if agents is not None:
            STATE_DIR.mkdir(parents=True, exist_ok=True)
            cfg_path = STATE_DIR / "agents.json"
            config_data: dict[str, Any] = {"agents": agents}
            if defaults:
                config_data["defaults"] = defaults
            cfg_path.write_text(json.dumps(config_data, indent=2))
            return str(cfg_path)

        # 3. Environment variable
        from_env = os.environ.get("WICK_CONFIG")
        if from_env:
            return from_env

        # 4. No config — write an empty config so server starts clean
        STATE_DIR.mkdir(parents=True, exist_ok=True)
        cfg_path = STATE_DIR / "agents.json"
        cfg_path.write_text(json.dumps({"agents": {}}, indent=2))
        return str(cfg_path)

    # -- Build ---------------------------------------------------------------

    @staticmethod
    def build(source_dir: str | None = None) -> None:
        """Compile the Go server binary."""
        src = Path(source_dir) if source_dir else SERVER_DIR
        if not (src / "main.go").exists():
            raise FileNotFoundError(f"main.go not found in {src}")

        result = subprocess.run(
            ["go", "build", "-o", "wick_go", "."],
            cwd=str(src),
            capture_output=True,
            text=True,
        )
        if result.returncode != 0:
            raise RuntimeError(f"go build failed:\n{result.stderr}")

    # -- Start / Stop --------------------------------------------------------

    def start(self) -> int:
        """Start the server in the background. Returns the PID."""
        info = self.status()
        if info["running"]:
            return info["pid"]

        STATE_DIR.mkdir(parents=True, exist_ok=True)

        env = os.environ.copy()
        env["PORT"] = str(self.port)
        env["HOST"] = self.host
        env.update(self.extra_env)

        self._log_fh = open(self.log_path, "a")  # noqa: SIM115
        proc = subprocess.Popen(
            [self.binary, "-config", self.config],
            env=env,
            stdout=self._log_fh,
            stderr=subprocess.STDOUT,
        )
        # Close parent's copy — the child inherited the fd.
        self._log_fh.close()
        self._log_fh = None

        DEFAULT_PID_FILE.write_text(str(proc.pid))
        return proc.pid

    def stop(self) -> None:
        """Stop the server via PID file."""
        if not DEFAULT_PID_FILE.exists():
            return

        try:
            pid = int(DEFAULT_PID_FILE.read_text().strip())
        except (ValueError, OSError):
            DEFAULT_PID_FILE.unlink(missing_ok=True)
            return

        try:
            os.kill(pid, signal.SIGTERM)
            for _ in range(50):
                os.kill(pid, 0)
                time.sleep(0.1)
            # Still alive after 5s — force kill
            os.kill(pid, signal.SIGKILL)
        except (ProcessLookupError, PermissionError):
            pass  # already dead or not ours
        finally:
            DEFAULT_PID_FILE.unlink(missing_ok=True)

    def status(self) -> dict[str, Any]:
        """Return {running, pid, url}."""
        pid: int | None = None
        running = False

        if DEFAULT_PID_FILE.exists():
            try:
                pid = int(DEFAULT_PID_FILE.read_text().strip())
                os.kill(pid, 0)
                running = True
            except (ValueError, ProcessLookupError, PermissionError):
                DEFAULT_PID_FILE.unlink(missing_ok=True)
                pid = None

        return {
            "running": running,
            "pid": pid,
            "url": f"http://{self.host}:{self.port}" if running else None,
        }

    # -- Health check --------------------------------------------------------

    def wait_ready(self, timeout: int = 10) -> bool:
        """Poll /health until the server responds or timeout."""
        import requests as _requests

        url = f"http://localhost:{self.port}/health"
        deadline = time.time() + timeout
        while time.time() < deadline:
            try:
                r = _requests.get(url, timeout=2)
                if r.status_code == 200:
                    return True
            except Exception:
                pass
            time.sleep(0.5)
        return False

    # -- Logs ----------------------------------------------------------------

    def logs(self, n: int = 50) -> str:
        """Return the last n lines of the log file."""
        if not self.log_path.exists():
            return ""
        lines = self.log_path.read_text().splitlines()
        return "\n".join(lines[-n:])

    # -- Context manager -----------------------------------------------------

    def __enter__(self) -> WickServer:
        self.start()
        if not self.wait_ready():
            self.stop()
            raise RuntimeError(
                f"Server did not become ready on port {self.port}. "
                f"Check logs: {self.log_path}"
            )
        return self

    def __exit__(self, *exc: object) -> None:
        self.stop()
