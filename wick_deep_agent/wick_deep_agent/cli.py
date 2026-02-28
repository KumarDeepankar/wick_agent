"""wick-agent CLI — build, start, stop, status, logs, systemd.

Usage:
    wick-agent build   [--source ./server]
    wick-agent start   [--port 8000] [--config path] [--binary path]
    wick-agent stop
    wick-agent status
    wick-agent logs    [-n 50]
    wick-agent systemd [--binary path] [--config path] [--port 8000]
"""

from __future__ import annotations

import argparse
import sys

from .launcher import DEFAULT_LOG_FILE, DEFAULT_PID_FILE, WickServer


def cmd_build(args: argparse.Namespace) -> None:
    print("Building wick_server...")
    WickServer.build(source_dir=args.source)
    print("  build OK")


def cmd_start(args: argparse.Namespace) -> None:
    srv = WickServer(binary=args.binary, config=args.config, port=args.port)
    pid = srv.start()
    if not srv.wait_ready():
        print(f"WARN: server started (pid={pid}) but /health not responding", file=sys.stderr)
        sys.exit(1)
    print(f"wick_server running (pid={pid}, port={args.port})")


def cmd_stop(_args: argparse.Namespace) -> None:
    if not DEFAULT_PID_FILE.exists():
        print("not running")
        return

    try:
        pid = int(DEFAULT_PID_FILE.read_text().strip())
    except (ValueError, OSError):
        DEFAULT_PID_FILE.unlink(missing_ok=True)
        print("stopped (stale pid file removed)")
        return

    import os
    import signal
    import time

    try:
        os.kill(pid, signal.SIGTERM)
        for _ in range(50):
            os.kill(pid, 0)
            time.sleep(0.1)
        os.kill(pid, signal.SIGKILL)
    except (ProcessLookupError, PermissionError):
        pass
    finally:
        DEFAULT_PID_FILE.unlink(missing_ok=True)

    print(f"stopped (pid={pid})")


def cmd_status(_args: argparse.Namespace) -> None:
    if not DEFAULT_PID_FILE.exists():
        print("not running")
        return

    try:
        pid = int(DEFAULT_PID_FILE.read_text().strip())
    except (ValueError, OSError):
        DEFAULT_PID_FILE.unlink(missing_ok=True)
        print("not running (stale pid file removed)")
        return

    import os

    try:
        os.kill(pid, 0)
        print(f"running  pid={pid}")
    except (ProcessLookupError, PermissionError):
        DEFAULT_PID_FILE.unlink(missing_ok=True)
        print("not running (stale pid file removed)")


def cmd_logs(args: argparse.Namespace) -> None:
    if not DEFAULT_LOG_FILE.exists():
        print("(no log file)")
        return
    lines = DEFAULT_LOG_FILE.read_text().splitlines()
    print("\n".join(lines[-args.n:]))


def cmd_systemd(args: argparse.Namespace) -> None:
    binary = args.binary or "wick_server"
    config = args.config or "/etc/wick/agents.yaml"
    port = args.port

    unit = f"""\
[Unit]
Description=Wick Agent Server
After=network.target

[Service]
Type=simple
ExecStart={binary} -config {config}
Environment=PORT={port}
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
"""
    print(unit)


def main() -> None:
    parser = argparse.ArgumentParser(
        prog="wick-agent",
        description="Manage the wick_server agent server",
    )
    sub = parser.add_subparsers(dest="command")

    # build
    p_build = sub.add_parser("build", help="Compile the Go server binary")
    p_build.add_argument("--source", default=None, help="Path to Go source directory")

    # start
    p_start = sub.add_parser("start", help="Start the server")
    p_start.add_argument("--port", type=int, default=8000)
    p_start.add_argument("--config", default=None)
    p_start.add_argument("--binary", default=None)

    # stop
    sub.add_parser("stop", help="Stop the server")

    # status
    sub.add_parser("status", help="Show server status")

    # logs
    p_logs = sub.add_parser("logs", help="Show server logs")
    p_logs.add_argument("-n", type=int, default=50, help="Number of lines")

    # systemd
    p_sys = sub.add_parser("systemd", help="Print a systemd unit file")
    p_sys.add_argument("--binary", default=None)
    p_sys.add_argument("--config", default=None)
    p_sys.add_argument("--port", type=int, default=8000)

    args = parser.parse_args()

    if not args.command:
        parser.print_help()
        sys.exit(1)

    dispatch = {
        "build": cmd_build,
        "start": cmd_start,
        "stop": cmd_stop,
        "status": cmd_status,
        "logs": cmd_logs,
        "systemd": cmd_systemd,
    }
    dispatch[args.command](args)


if __name__ == "__main__":
    main()
