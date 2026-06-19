"""Zentrale Konfiguration via .env (keine harten Pfade)."""

from __future__ import annotations

from functools import lru_cache
from pathlib import Path

from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", env_file_encoding="utf-8", extra="ignore")

    # Routing-Engine: "http" (osrm-routed Subprozess) oder "bindings" (in-process, optional)
    engine: str = "http"
    osrm_graph_path: Path = Path("data/germany.osrm")
    osrm_algorithm: str = "MLD"

    # HTTP-Engine: osrm-routed als lokaler Subprozess
    osrm_routed_bin: str = "osrm-routed"
    osrm_routed_host: str = "127.0.0.1"
    osrm_routed_port: int = 5001  # 5000 ist auf macOS vom AirPlay-Receiver belegt
    manage_osrm_routed: bool = True  # False = bereits laufenden osrm-routed nutzen
    osrm_routed_url: str | None = None  # expliziter Override; sonst host:port

    @property
    def routed_base_url(self) -> str:
        return self.osrm_routed_url or f"http://{self.osrm_routed_host}:{self.osrm_routed_port}"

    # Verarbeitung
    workers: int = 8
    snap_limit_m: float = 50.0
    checkpoint_every: int = 2000
    max_sync_batch: int = 20000  # Obergrenze für POST /route-batch (sonst Job-Flow nutzen)

    # Verzeichnisse
    data_dir: Path = Path("data")
    checkpoint_dir: Path = Path("data/checkpoints")

    # Backend / Frontend
    api_host: str = "127.0.0.1"
    api_port: int = 8000
    backend_url: str = "http://127.0.0.1:8000"

    # Office.js-Add-in (HTTPS, von scripts/serve_addin.sh genutzt)
    addin_host: str = "127.0.0.1"
    addin_port: int = 8443

    @property
    def upload_dir(self) -> Path:
        return self.data_dir / "uploads"

    @property
    def result_dir(self) -> Path:
        return self.data_dir / "results"

    def ensure_dirs(self) -> None:
        for d in (self.data_dir, self.checkpoint_dir, self.upload_dir, self.result_dir):
            d.mkdir(parents=True, exist_ok=True)


@lru_cache
def get_settings() -> Settings:
    return Settings()
