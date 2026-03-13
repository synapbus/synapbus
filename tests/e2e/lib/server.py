"""SynapBus server lifecycle management (start/stop/health)."""
from __future__ import annotations

import os
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time

import httpx


def find_free_port() -> int:
    """Find an available TCP port."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("", 0))
        return s.getsockname()[1]


def find_binary() -> str:
    """Locate the synapbus binary."""
    # Try project root
    repo_root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))))
    candidate = os.path.join(repo_root, "synapbus")
    if os.path.isfile(candidate) and os.access(candidate, os.X_OK):
        return candidate
    # Try PATH
    from shutil import which
    found = which("synapbus")
    if found:
        return found
    print("ERROR: synapbus binary not found. Run 'make build' first.")
    sys.exit(1)


def start_server(port: int) -> tuple:
    """Start SynapBus server, return (process, data_dir, port)."""
    binary = find_binary()
    data_dir = tempfile.mkdtemp(prefix="synapbus-e2e-")

    proc = subprocess.Popen(
        [binary, "serve", "--port", str(port), "--data", data_dir],
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
    )

    # Wait for server to be healthy
    for _ in range(30):
        try:
            resp = httpx.get("http://localhost:{}/health".format(port), timeout=2)
            if resp.status_code == 200:
                return proc, data_dir, port
        except httpx.ConnectError:
            pass
        time.sleep(0.5)

    proc.terminate()
    shutil.rmtree(data_dir, ignore_errors=True)
    print("ERROR: Server failed to start within 15 seconds")
    sys.exit(1)


def stop_server(proc: subprocess.Popen, data_dir: str) -> None:
    """Stop server and clean up."""
    try:
        proc.send_signal(signal.SIGTERM)
        proc.wait(timeout=10)
    except Exception:
        proc.kill()
    shutil.rmtree(data_dir, ignore_errors=True)


def wait_for_server(base_url: str, timeout: int = 15) -> bool:
    """Wait for an already-running server to become healthy."""
    for _ in range(timeout * 2):
        try:
            resp = httpx.get("{}/health".format(base_url), timeout=2)
            if resp.status_code == 200:
                return True
        except httpx.ConnectError:
            pass
        time.sleep(0.5)
    return False
