"""Routing-Abstraktion.

Schmales Interface (`RoutingEngine`) mit einer Implementierung: `HttpEngine` fragt einen
lokalen `osrm-routed` per HTTP ab. OSRM-Route-JSON:
    res['code'] == 'Ok'
    res['routes'][0]['distance']    # Meter
    res['routes'][0]['duration']    # Sekunden
    res['waypoints'][i]['distance'] # Snap-Distanz in Metern
Achtung: OSRM erwartet Koordinaten als (lon, lat).
"""

from __future__ import annotations

from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from pathlib import Path
from typing import Protocol, runtime_checkable

from backend.config import Settings

Coord = tuple[float, float]  # (lat, lon) — menschenfreundliche, interne Reihenfolge

OK = "ok"
SNAPPED_FAR = "snapped_far"
NO_ROUTE = "no_route"
ERROR = "error"
PLZ_NOT_FOUND = "plz_not_found"  # Geocoding: keine Koordinate für die LKZ/PLZ-Kombination


@dataclass(frozen=True)
class RouteResult:
    distance_km: float | None
    duration_min: float | None
    status: str
    snap_m: float | None = None
    message: str | None = None


@runtime_checkable
class RoutingEngine(Protocol):
    def route(self, origin: Coord, dest: Coord) -> RouteResult:
        """Kürzeste Fahrstrecke zwischen origin und dest, jeweils als (lat, lon)."""
        ...


class HttpEngine:
    """Routing über einen lokalen osrm-routed via HTTP. httpx.Client ist thread-safe
    und wird über alle Worker-Threads geteilt."""

    def __init__(self, base_url: str, snap_limit_m: float, timeout: float = 30.0) -> None:
        import httpx

        self._base_url = base_url.rstrip("/")
        self._snap_limit_m = snap_limit_m
        self._client = httpx.Client(timeout=timeout)

    def route(self, origin: Coord, dest: Coord) -> RouteResult:
        o_lat, o_lon = origin
        d_lat, d_lon = dest
        url = f"{self._base_url}/route/v1/driving/{o_lon},{o_lat};{d_lon},{d_lat}"
        try:
            resp = self._client.get(url, params={"overview": "false"})
            data = resp.json()
        except Exception as exc:  # Netzwerk-/JSON-Fehler je Zeile abfangen
            return RouteResult(None, None, ERROR, message=str(exc))
        return self._parse(data)

    def _parse(self, res: dict) -> RouteResult:
        try:
            code = res.get("code")
            routes = res.get("routes") or []
            if code != "Ok" or not routes:
                return RouteResult(None, None, NO_ROUTE, message=str(code))

            route = routes[0]
            distance_km = round(float(route["distance"]) / 1000.0, 2)
            duration_min = round(float(route["duration"]) / 60.0, 2)
            snap_m = _max_snap(res.get("waypoints", []))
            status = SNAPPED_FAR if snap_m is not None and snap_m > self._snap_limit_m else OK
            return RouteResult(distance_km, duration_min, status, snap_m=snap_m)
        except Exception as exc:
            return RouteResult(None, None, ERROR, message=str(exc))

    def close(self) -> None:
        self._client.close()


def _max_snap(waypoints: object) -> float | None:
    distances = []
    for wp in waypoints:
        d = wp.get("distance") if hasattr(wp, "get") else wp["distance"]
        if d is not None:
            distances.append(float(d))
    return max(distances) if distances else None


def _graph_exists(graph_path: Path) -> bool:
    # osrm-routed lädt mehrere Dateien anhand des Basis-Pfads (graph.osrm.*).
    return graph_path.exists() or any(graph_path.parent.glob(graph_path.name + ".*"))


def route_pairs(
    engine: RoutingEngine,
    pairs: list[tuple[Coord, Coord]],
    workers: int,
) -> list[RouteResult]:
    """Berechnet mehrere (origin, dest)-Paare parallel; Reihenfolge bleibt erhalten.
    engine.route fängt Fehler je Paar ab, daher wirft map() nicht."""
    if not pairs:
        return []
    with ThreadPoolExecutor(max_workers=workers) as pool:
        return list(pool.map(lambda od: engine.route(*od), pairs))


def get_engine(settings: Settings) -> RoutingEngine:
    return HttpEngine(base_url=settings.routed_base_url, snap_limit_m=settings.snap_limit_m)
