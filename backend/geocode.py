"""Offline-Geocoding: PLZ-Zentroide aus LKZ/PLZ-Kombinationen.

Lädt eine kompakte CSV (`country,plz,lat,lon`) einmal in den Speicher und löst
(LKZ, PLZ) → (lat, lon) auf. Zentroid je PLZ genügt. Die CSV wird separat mit
scripts/build_geocode.sh aus dem GeoNames-Postal-Datensatz (CC BY 4.0) erzeugt.
"""

from __future__ import annotations

import csv
from pathlib import Path

from backend.config import Settings

Coord = tuple[float, float]  # (lat, lon)


def norm_country(lkz: str) -> str:
    return str(lkz).strip().upper()


def norm_plz(country: str, plz: str) -> str:
    """PLZ normalisieren. Excel liefert PLZ oft als Zahl → führende Nullen fehlen;
    deutsche PLZ sind fünfstellig, daher auffüllen."""
    p = str(plz).strip()
    if country == "DE" and p.isdigit():
        p = p.zfill(5)
    return p


class Geocoder:
    """In-Memory-Lookup (LKZ, PLZ) → Zentroid-Koordinate."""

    def __init__(self, table: dict[tuple[str, str], Coord]) -> None:
        self._table = table

    def resolve(self, lkz: str, plz: str) -> Coord | None:
        country = norm_country(lkz)
        return self._table.get((country, norm_plz(country, plz)))

    def __len__(self) -> int:
        return len(self._table)


def load_table(csv_path: Path) -> dict[tuple[str, str], Coord]:
    table: dict[tuple[str, str], Coord] = {}
    with csv_path.open(encoding="utf-8", newline="") as fh:
        for row in csv.DictReader(fh):
            country = norm_country(row["country"])
            plz = norm_plz(country, row["plz"])
            try:
                table[(country, plz)] = (float(row["lat"]), float(row["lon"]))
            except (TypeError, ValueError, KeyError):
                continue
    return table


def load_geocoder(settings: Settings) -> Geocoder | None:
    """Geocoder laden, oder None wenn die CSV fehlt (Feature dann deaktiviert)."""
    path = settings.geocode_path
    if not path.exists():
        return None
    return Geocoder(load_table(path))
