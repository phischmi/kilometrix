"""Excel lesen/schreiben und Spalten-Mapping."""

from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path

import pandas as pd

from backend.routing import RouteResult

# Erwartete Standard-Spaltennamen (getrennte Koordinatenspalten).
DEFAULT_COLUMNS = {
    "origin_lat": "origin_lat",
    "origin_lon": "origin_lon",
    "dest_lat": "dest_lat",
    "dest_lon": "dest_lon",
}


@dataclass
class ColumnMapping:
    origin_lat: str
    origin_lon: str
    dest_lat: str
    dest_lon: str
    id_columns: list[str] = field(default_factory=list)

    @property
    def coord_columns(self) -> list[str]:
        return [self.origin_lat, self.origin_lon, self.dest_lat, self.dest_lon]


def read_workbook(path: str | Path, sheet: str | int = 0) -> tuple[pd.DataFrame, list[str]]:
    """Liest ein Blatt und liefert (DataFrame, Spaltennamen) — Spalten fürs UI-Mapping."""
    df = pd.read_excel(path, sheet_name=sheet, engine="openpyxl")
    return df, list(df.columns.astype(str))


def suggest_mapping(columns: list[str]) -> dict[str, str | None]:
    """Schlägt anhand der Standardnamen ein Mapping vor (case-insensitive)."""
    lower = {c.lower(): c for c in columns}
    return {key: lower.get(default.lower()) for key, default in DEFAULT_COLUMNS.items()}


def write_results(
    df: pd.DataFrame,
    results: list[RouteResult],
    out_path: str | Path,
) -> Path:
    """Hängt distance_km, duration_min, status (+ snap_m) an die Originaltabelle und schreibt xlsx."""
    if len(results) != len(df):
        raise ValueError(f"Ergebnisanzahl ({len(results)}) != Zeilenanzahl ({len(df)}).")

    out = df.copy()
    out["distance_km"] = [r.distance_km for r in results]
    out["duration_min"] = [r.duration_min for r in results]
    out["status"] = [r.status for r in results]
    out["snap_m"] = [r.snap_m for r in results]

    out_path = Path(out_path)
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out.to_excel(out_path, index=False, engine="openpyxl")
    return out_path
