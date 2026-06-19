"""Routing-Abstraktion.

Schmales Interface mit genau einer Implementierung: in-process über das offizielle
`osrm-bindings`-Paket. Das Interface ist bewusst klein gehalten, damit später ein
HTTP-Fallback (`osrm-routed` + httpx) ohne Umbau eingesteckt werden kann.

Verifizierte API von osrm-bindings 0.3.0 (gegen die installierten Stubs geprüft):
    osrm.OSRM(algorithm='MLD', storage_config='data/germany.osrm')
    params = osrm.RouteParameters(coordinates=[(lon, lat), (lon, lat)], overview='false')
    res = inst.Route(params)        # Standard-OSRM-Route-JSON
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


class OsrmBindingsEngine:
    """In-process-Routing über osrm-bindings. Eine geteilte, read-only OSRM-Instanz;
    OSRM ist für nebenläufige Abfragen ausgelegt, daher thread-safe nutzbar."""

    def __init__(self, graph_path: Path, algorithm: str, snap_limit_m: float) -> None:
        import osrm  # lazy, damit Tests/Import ohne installiertes Wheel möglich bleiben

        if not _graph_exists(graph_path):
            raise FileNotFoundError(
                f"OSRM-Graph nicht gefunden: {graph_path}.* — bitte erst mit "
                f"scripts/build_graph.sh bauen und nach data/ kopieren."
            )

        # use_shared_memory=False -> direkt aus den Dateien laden (kein osrm-datastore nötig).
        self._osrm = osrm.OSRM(
            algorithm=algorithm,
            storage_config=str(graph_path),
            use_shared_memory=False,
        )
        self._RouteParameters = osrm.RouteParameters
        self._snap_limit_m = snap_limit_m

    def route(self, origin: Coord, dest: Coord) -> RouteResult:
        o_lat, o_lon = origin
        d_lat, d_lon = dest
        try:
            params = self._RouteParameters(
                coordinates=[(o_lon, o_lat), (d_lon, d_lat)],
                overview="false",
                steps=False,
            )
            res = self._osrm.Route(params)
        except Exception as exc:  # eine kaputte Zeile darf den Job nicht killen
            return RouteResult(None, None, ERROR, message=str(exc))

        return self._parse(res)

    def _parse(self, res: object) -> RouteResult:
        try:
            code = res.get("code") if hasattr(res, "get") else res["code"]
            routes = res["routes"] if "routes" in res else []
            if code != "Ok" or not routes:
                return RouteResult(None, None, NO_ROUTE, message=str(code))

            route = routes[0]
            distance_km = round(float(route["distance"]) / 1000.0, 2)
            duration_min = round(float(route["duration"]) / 60.0, 2)

            snap_m = _max_snap(res.get("waypoints", []) if hasattr(res, "get") else res["waypoints"])
            status = SNAPPED_FAR if snap_m is not None and snap_m > self._snap_limit_m else OK
            return RouteResult(distance_km, duration_min, status, snap_m=snap_m)
        except Exception as exc:
            return RouteResult(None, None, ERROR, message=str(exc))


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
    # osrm-bindings lädt mehrere Dateien anhand des Basis-Pfads (graph.osrm.*).
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
    if settings.engine == "http":
        return HttpEngine(base_url=settings.routed_base_url, snap_limit_m=settings.snap_limit_m)
    if settings.engine == "bindings":
        return OsrmBindingsEngine(
            graph_path=settings.osrm_graph_path,
            algorithm=settings.osrm_algorithm,
            snap_limit_m=settings.snap_limit_m,
        )
    raise ValueError(f"Unbekannte ENGINE: {settings.engine!r} (erlaubt: 'http', 'bindings').")
