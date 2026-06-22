"""Verwaltet osrm-routed als lokalen Subprozess (Start, Readiness, Stop)."""

from __future__ import annotations

import shutil
import subprocess
import time
from pathlib import Path

import httpx

from backend.routing import _graph_exists


class OsrmRoutedProcess:
    def __init__(
        self,
        binary: str,
        graph_path: Path,
        algorithm: str,
        host: str,
        port: int,
        verbosity: str = "WARNING",
        mmap: bool = False,
    ) -> None:
        self._binary = binary
        self._graph_path = graph_path
        self._algorithm = algorithm.lower()
        self._host = host
        self._port = port
        self._verbosity = verbosity
        self._mmap = mmap
        self._proc: subprocess.Popen | None = None

    @property
    def base_url(self) -> str:
        return f"http://{self._host}:{self._port}"

    def start(self, ready_timeout: float = 60.0) -> None:
        if shutil.which(self._binary) is None and not Path(self._binary).exists():
            raise FileNotFoundError(
                f"'{self._binary}' nicht gefunden. Auf macOS: 'brew install osrm-backend'."
            )
        if not _graph_exists(self._graph_path):
            raise FileNotFoundError(
                f"OSRM-Graph nicht gefunden: {self._graph_path}.* — erst mit "
                f"scripts/build_graph.sh bauen."
            )

        args = [
            self._binary,
            "--algorithm",
            self._algorithm,
            "--verbosity",
            self._verbosity,  # WARNING: keine Koordinaten-Logs
            "--ip",
            self._host,
            "--port",
            str(self._port),
        ]
        if self._mmap:
            args.append("--mmap")  # Graph von der Platte mappen statt ins RAM laden
        args.append(str(self._graph_path))  # Basis-Pfad muss das letzte Argument bleiben
        self._proc = subprocess.Popen(
            args,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
        )
        self._wait_ready(ready_timeout)

    def _wait_ready(self, timeout: float) -> None:
        deadline = time.monotonic() + timeout
        probe = f"{self.base_url}/route/v1/driving/13.4,52.5;13.4,52.5?overview=false"
        while time.monotonic() < deadline:
            if self._proc is not None and self._proc.poll() is not None:
                out = self._proc.stdout.read().decode(errors="replace") if self._proc.stdout else ""
                raise RuntimeError(f"osrm-routed beendete sich beim Start:\n{out[-2000:]}")
            try:
                if httpx.get(probe, timeout=2).status_code == 200:
                    return
            except httpx.HTTPError:
                pass
            time.sleep(0.5)
        self.stop()
        raise TimeoutError(f"osrm-routed wurde nicht innerhalb von {timeout}s bereit.")

    def stop(self) -> None:
        if self._proc is None:
            return
        self._proc.terminate()
        try:
            self._proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            self._proc.kill()
        self._proc = None
