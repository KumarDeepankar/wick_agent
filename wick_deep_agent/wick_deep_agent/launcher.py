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

    server.start()
    server.wait_ready()
    server.register_agents()
    server.register_tools()
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
import sys
import time
from pathlib import Path
from typing import Any

from .model import ModelDef


STATE_DIR = Path.home() / ".wick_deep_agent"
DEFAULT_PID_FILE = STATE_DIR / "wick_go.pid"
DEFAULT_LOG_FILE = STATE_DIR / "wick_go.log"

_PACKAGE_DIR = Path(__file__).resolve().parent
_BIN_NAME = "wick_go.exe" if sys.platform == "win32" else "wick_go"
_BUNDLED_BINARY = _PACKAGE_DIR / "bin" / _BIN_NAME


class WickServer:
    """Manages the wick_go server process.

    The Go binary starts with zero agents. All agent configuration is
    registered via the REST API after startup (``register_agents()``).
    """

    def __init__(
        self,
        binary: str | None = None,
        agents: dict[str, Any] | None = None,
        defaults: dict[str, Any] | None = None,
        port: int = 8000,
        host: str = "0.0.0.0",
        env: dict[str, str] | None = None,
        log_file: str | None = None,
        tools: list | None = None,
    ) -> None:
        self.port = port
        self.host = host
        self.extra_env = env or {}
        self.log_path = Path(log_file) if log_file else DEFAULT_LOG_FILE

        self._agents = agents
        self._defaults = defaults
        self.binary = self._resolve_binary(binary)

        self._log_fh: Any = None
        self._tools = tools
        self._tool_server: Any = None  # Optional[ToolServer]

    # -- Resolution ----------------------------------------------------------

    @staticmethod
    def _resolve_binary(explicit: str | None) -> str:
        if explicit:
            return explicit
        from_env = os.environ.get("WICK_GO_BINARY")
        if from_env:
            return from_env
        # Bundled binary (pip install)
        if _BUNDLED_BINARY.exists() and os.access(str(_BUNDLED_BINARY), os.X_OK):
            return str(_BUNDLED_BINARY)
        # Dev mode — adjacent server/ dir
        dev_binary = _PACKAGE_DIR.parent / "server" / _BIN_NAME
        if dev_binary.exists():
            return str(dev_binary)
        raise FileNotFoundError(
            "wick_go binary not found. Install a platform wheel, "
            "set WICK_GO_BINARY, or pass binary= explicitly."
        )

    @staticmethod
    def _resolve_model_spec(model: Any) -> Any:
        """Convert a model value to a self-contained dict for the Go server.

        - ``"ollama:llama3.1:8b"`` → ``{"provider":"ollama","model":"llama3.1:8b","base_url":"http://localhost:11434/v1"}``
        - ``"openai:gpt-4"`` → ``{"provider":"openai","model":"gpt-4","api_key":"..."}``
        - ``"anthropic:claude-3"`` → ``{"provider":"anthropic","model":"claude-3","api_key":"..."}``
        - ``ModelDef`` → ``{"provider":"proxy","model":name,"callback_url":"..."}``  (callback_url filled by caller)
        - ``dict`` → pass through
        """
        if isinstance(model, dict):
            return model

        if isinstance(model, ModelDef):
            # Placeholder — callback_url is filled by _start_tool_server
            return {"provider": "proxy", "model": model.name}

        if not isinstance(model, str):
            return model

        parts = model.split(":", 1)
        provider = parts[0]
        model_name = parts[1] if len(parts) > 1 else ""

        if provider == "ollama":
            base_url = os.environ.get("OLLAMA_BASE_URL", "http://localhost:11434")
            return {"provider": "ollama", "model": model_name, "base_url": base_url + "/v1"}
        elif provider == "openai":
            api_key = os.environ.get("OPENAI_API_KEY", "")
            spec: dict[str, Any] = {"provider": "openai", "model": model_name, "api_key": api_key}
            base_url_env = os.environ.get("OPENAI_BASE_URL")
            if base_url_env:
                spec["base_url"] = base_url_env
            return spec
        elif provider == "anthropic":
            return {"provider": "anthropic", "model": model_name, "api_key": os.environ.get("ANTHROPIC_API_KEY", "")}
        elif provider == "gateway":
            return {
                "provider": "gateway",
                "model": model_name,
                "base_url": os.environ.get("GATEWAY_BASE_URL", "http://localhost:4000") + "/v1",
                "api_key": os.environ.get("GATEWAY_API_KEY", ""),
            }
        else:
            # Assume Ollama model (e.g. "llama3.1:8b" without prefix)
            base_url = os.environ.get("OLLAMA_BASE_URL", "http://localhost:11434")
            return {"provider": "ollama", "model": model, "base_url": base_url + "/v1"}

    # -- Build ---------------------------------------------------------------

    @staticmethod
    def build(source_dir: str | None = None) -> None:
        """Compile the Go server binary (dev mode).

        Builds into the source dir, then copies to the bundled bin/ location
        so that _resolve_binary() picks up the fresh build.
        """
        src = Path(source_dir) if source_dir else (_PACKAGE_DIR.parent / "server")
        if not (src / "main.go").exists():
            raise FileNotFoundError(f"main.go not found in {src}")

        result = subprocess.run(
            ["go", "build", "-o", _BIN_NAME, "."],
            cwd=str(src),
            capture_output=True,
            text=True,
        )
        if result.returncode != 0:
            raise RuntimeError(f"go build failed:\n{result.stderr}")

        # Copy to bundled bin/ so the launcher resolves the fresh binary
        built = src / _BIN_NAME
        if built.exists() and _BUNDLED_BINARY.parent.exists():
            import shutil
            shutil.copy2(str(built), str(_BUNDLED_BINARY))

    # -- Start / Stop --------------------------------------------------------

    def _collect_models_from_agents(self) -> list[ModelDef]:
        """Scan agent configs for ModelDef instances in the 'model' field."""
        models: list[ModelDef] = []
        if not self._agents:
            return models
        seen: set[str] = set()
        for agent_cfg in self._agents.values():
            m = agent_cfg.get("model")
            if isinstance(m, ModelDef) and m.name not in seen:
                models.append(m)
                seen.add(m.name)
        return models

    def start(self) -> int:
        """Start the server in the background. Returns the PID."""
        # Collect custom model handlers from agent configs
        models = self._collect_models_from_agents()

        # Start the tool/model sidecar server before the Go binary
        if (self._tools or models) and self._tool_server is None:
            from .tool import ToolServer

            self._tool_server = ToolServer(
                tools=self._tools or [],
                models=models,
            )
            self._tool_server.start()

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
            [self.binary],
            env=env,
            stdout=self._log_fh,
            stderr=subprocess.STDOUT,
        )
        # Close parent's copy — the child inherited the fd.
        self._log_fh.close()
        self._log_fh = None

        DEFAULT_PID_FILE.write_text(str(proc.pid))
        return proc.pid

    def register_agents(self) -> None:
        """Register agent templates with the running Go server via API."""
        if not self._agents:
            return

        import requests as _requests

        callback_url = ""
        if self._tool_server and self._tool_server.is_alive:
            callback_url = self._tool_server.callback_url

        url = f"http://localhost:{self.port}/agents/"
        for agent_id, agent_cfg in self._agents.items():
            # Merge defaults
            merged: dict[str, Any] = {**(self._defaults or {}), **agent_cfg}

            # Resolve model spec to self-contained dict
            raw_model = merged.get("model")
            resolved = self._resolve_model_spec(raw_model)

            # Inject callback_url for proxy models
            if isinstance(resolved, dict) and resolved.get("provider") == "proxy" and callback_url:
                resolved["callback_url"] = callback_url
            merged["model"] = resolved

            # Inject Tavily key from env if available
            tavily_key = os.environ.get("TAVILY_API_KEY", "")
            if tavily_key:
                merged.setdefault("builtin_config", {})["tavily_api_key"] = tavily_key

            payload = {"agent_id": agent_id, **merged}
            resp = _requests.post(url, json=payload, timeout=10)
            resp.raise_for_status()

    def register_tools(self) -> None:
        """Register external tools with the running Go server."""
        if not self._tool_server or not self._tool_server.is_alive:
            return

        import requests as _requests

        url = f"http://localhost:{self.port}/agents/tools/register"
        for td in self._tool_server.tool_defs:
            schema = td.to_schema(self._tool_server.callback_url)
            resp = _requests.post(url, json=schema, timeout=5)
            resp.raise_for_status()

    def stop(self) -> None:
        """Stop the server and tool sidecar."""
        # Stop tool server first
        if self._tool_server is not None:
            self._tool_server.stop()
            self._tool_server = None

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

    def restart(self, build: bool = True, timeout: int = 10) -> int:
        """Stop, optionally rebuild, and start the server. Returns the new PID."""
        self.stop()
        if build:
            self.build()
        pid = self.start()
        if not self.wait_ready(timeout=timeout):
            raise RuntimeError(
                f"Server did not become ready after restart on port {self.port}. "
                f"Check logs: {self.log_path}"
            )
        self.register_agents()
        self.register_tools()
        return pid

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
        self.register_agents()
        self.register_tools()
        return self

    def __exit__(self, *exc: object) -> None:
        self.stop()
