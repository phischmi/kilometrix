"""Zentrale Konfiguration via .env (keine harten Pfade)."""

from __future__ import annotations

from functools import lru_cache
from pathlib import Path

from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", env_file_encoding="utf-8", extra="ignore")

    # OSRM-Graph (vom osrm-routed geladen)
    osrm_graph_path: Path = Path("data/germany.osrm")
    osrm_algorithm: str = "MLD"

    # osrm-routed als lokaler Subprozess
    osrm_routed_bin: str = "osrm-routed"
    osrm_routed_host: str = "127.0.0.1"
    osrm_routed_port: int = 5001  # 5000 ist auf macOS vom AirPlay-Receiver belegt
    manage_osrm_routed: bool = True  # False = bereits laufenden osrm-routed nutzen
    osrm_routed_url: str | None = None  # expliziter Override; sonst host:port
    # WARNING unterdrückt die INFO-Request-Logs (die Koordinaten enthalten).
    osrm_routed_verbosity: str = "WARNING"
    # mmap: Graphdaten von der Platte mappen statt komplett ins RAM laden. Senkt den
    # Leerlauf-Speicher von osrm-routed deutlich (gut für RAM-knappe NAS); die erste
    # „kalte" Abfrage ist minimal langsamer, auf SSD vernachlässigbar.
    osrm_routed_mmap: bool = True

    @property
    def routed_base_url(self) -> str:
        return self.osrm_routed_url or f"http://{self.osrm_routed_host}:{self.osrm_routed_port}"

    # Geocoding (LKZ/PLZ → Zentroid). CSV separat via scripts/build_geocode.sh erzeugt.
    geocode_path: Path = Path("data/plz_centroids.csv")

    # Verarbeitung
    workers: int = 8
    snap_limit_m: float = 100.0
    max_sync_batch: int = 20000  # Obergrenze pro POST /route-batch

    # Auth (für zentralen Betrieb hinter Reverse Proxy). Lokal aus = offen.
    auth_enabled: bool = False
    auth_secret: str = ""  # HMAC-Secret für signierte Tokens (siehe backend/tokens.py)

    # Backend / Office.js-Add-in (HTTPS, von scripts/serve_addin.sh genutzt)
    addin_host: str = "127.0.0.1"
    addin_port: int = 8443


@lru_cache
def get_settings() -> Settings:
    return Settings()
