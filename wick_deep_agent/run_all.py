#!/usr/bin/env python3
"""
Build wick_go, start the server, run all tests from wick_client/, stop.

Usage:
    python run_all.py                    # build + start + test + stop
    python run_all.py --no-server        # tests only (server already running)
    python run_all.py --port 8003        # custom port
    WICK_URL=http://host:port python run_all.py --no-server
"""

from __future__ import annotations

import argparse
import os
import signal
import subprocess
import sys
import time

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
ROOT_DIR = os.path.dirname(SCRIPT_DIR)
WICK_GO_DIR = os.path.join(SCRIPT_DIR, "server")
AGENTS_YAML = os.path.join(ROOT_DIR, "wick", "agents.yaml")
TESTS_DIR = os.path.join(ROOT_DIR, "wick_client")
PORT = 8000


def wait_for_server(url: str, timeout: int = 10) -> bool:
    """Poll until the server responds or timeout."""
    import requests

    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            r = requests.get(f"{url}/health", timeout=2)
            if r.status_code == 200:
                return True
        except Exception:
            pass
        time.sleep(0.5)
    return False


def main() -> None:
    parser = argparse.ArgumentParser(description="Build, start, test, and stop wick_go")
    parser.add_argument(
        "--no-server",
        action="store_true",
        help="Skip starting wick_go (assume already running)",
    )
    parser.add_argument(
        "--port",
        type=int,
        default=PORT,
        help=f"Port for wick_go (default: {PORT})",
    )
    args = parser.parse_args()

    base_url = os.environ.get("WICK_URL", f"http://localhost:{args.port}")
    os.environ["WICK_URL"] = base_url

    proc = None

    if not args.no_server:
        # Build
        print("Building wick_go...")
        build = subprocess.run(
            ["go", "build", "-o", "wick_go", "."],
            cwd=WICK_GO_DIR,
            capture_output=True,
            text=True,
        )
        if build.returncode != 0:
            print(f"FAIL: go build failed:\n{build.stderr}")
            sys.exit(1)
        print("  build OK")

        # Start
        env = os.environ.copy()
        env["PORT"] = str(args.port)
        print(f"Starting wick_go on port {args.port}...")
        proc = subprocess.Popen(
            [os.path.join(WICK_GO_DIR, "wick_go"), "-config", AGENTS_YAML],
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
        )

        if not wait_for_server(base_url):
            proc.terminate()
            out, _ = proc.communicate(timeout=5)
            print(f"FAIL: wick_go did not start:\n{out.decode()}")
            sys.exit(1)
        print(f"  wick_go running (pid={proc.pid})")

    # Run tests
    tests = [
        "test_health",
        "test_agents_crud",
        "test_messages",
        "test_invoke",
        "test_stream",
    ]

    passed = 0
    failed = 0

    for name in tests:
        print(f"\n--- {name} ---")
        result = subprocess.run(
            [sys.executable, f"{name}.py"],
            cwd=TESTS_DIR,
            env=os.environ,
        )
        if result.returncode == 0:
            passed += 1
        else:
            failed += 1
            print(f"FAIL: {name}")

    # Stop server
    if proc:
        print(f"\nStopping wick_go (pid={proc.pid})...")
        proc.send_signal(signal.SIGTERM)
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()
        print("  stopped")

    # Summary
    total = passed + failed
    print(f"\n{'=' * 40}")
    print(f"Results: {passed}/{total} passed, {failed} failed")
    print(f"{'=' * 40}")

    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
