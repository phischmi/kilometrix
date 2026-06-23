"""FastAPI-Backend für das Kilometrix Office.js-Add-in.

Liefert das Add-in über HTTPS aus (same-origin), stellt /route-batch bereit und
verwaltet osrm-routed. Der OSRM-Graph wird separat mit scripts/build_graph.sh gebaut.
"""

from __future__ import annotations

from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import Depends, FastAPI, Header, HTTPException
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import RedirectResponse
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel, model_validator

from backend.config import get_settings
from backend.geocode import Coord, Geocoder, load_geocoder
from backend.osrm_process import OsrmRoutedProcess
from backend.routing import PLZ_NOT_FOUND, RouteResult, get_engine, route_pairs
from backend.tokens import TokenError, verify


def require_token(authorization: str | None = Header(default=None)) -> dict | None:
    """Bearer-Token prüfen, falls Auth aktiv. Lokal (auth_enabled=False) ein No-op."""
    settings = get_settings()
    if not settings.auth_enabled:
        return None
    if not authorization or not authorization.lower().startswith("bearer "):
        raise HTTPException(status_code=401, detail="Zugangstoken fehlt.")
    try:
        return verify(settings.auth_secret, authorization.split(" ", 1)[1].strip())
    except TokenError as exc:
        raise HTTPException(status_code=401, detail=f"Token ungültig: {exc}")


@asynccontextmanager
async def lifespan(app: FastAPI):
    settings = get_settings()
    app.state.settings = settings
    app.state.osrm_proc = None
    # osrm-routed bei Bedarf starten, dann Engine laden. Fehlt der Graph/das Binary,
    # startet das Backend trotzdem; /route-batch meldet dann 503 mit Hinweis.
    try:
        if settings.manage_osrm_routed:
            proc = OsrmRoutedProcess(
                binary=settings.osrm_routed_bin,
                graph_path=settings.osrm_graph_path,
                algorithm=settings.osrm_algorithm,
                host=settings.osrm_routed_host,
                port=settings.osrm_routed_port,
                verbosity=settings.osrm_routed_verbosity,
                mmap=settings.osrm_routed_mmap,
            )
            proc.start()
            app.state.osrm_proc = proc
        app.state.engine = get_engine(settings)
        app.state.engine_error = None
    except Exception as exc:
        app.state.engine = None
        app.state.engine_error = str(exc)
    # Geocoder unabhängig laden (fehlende CSV deaktiviert nur das Geocoding, nicht das Routing).
    try:
        app.state.geocoder = load_geocoder(settings)
    except Exception:
        app.state.geocoder = None
    try:
        yield
    finally:
        if app.state.osrm_proc is not None:
            app.state.osrm_proc.stop()


app = FastAPI(title="Kilometrix — OSRM Distanz-Tool", lifespan=lifespan)

# Lokales Tool: CORS offen, damit das Office.js-Add-in das Backend erreicht.
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"],
)

# FastAPI liefert das Add-in selbst aus (same-origin) — offline, ohne CORS-/Mixed-Content-Probleme.
_ADDIN_DIR = Path(__file__).resolve().parent.parent / "addin"
if _ADDIN_DIR.is_dir():
    app.mount("/addin", StaticFiles(directory=_ADDIN_DIR, html=True), name="addin")


@app.get("/", include_in_schema=False)
def root():
    return RedirectResponse(url="/addin/taskpane.html")


class RoutePair(BaseModel):
    """Ein Origin→Destination-Paar. Jeder Endpunkt wird ENTWEDER über Koordinaten
    (lat/lon) ODER über LKZ+PLZ angegeben; LKZ/PLZ werden serverseitig zum Zentroid
    aufgelöst."""

    id: str | int | None = None
    origin_lat: float | None = None
    origin_lon: float | None = None
    dest_lat: float | None = None
    dest_lon: float | None = None
    origin_lkz: str | None = None
    origin_plz: str | None = None
    dest_lkz: str | None = None
    dest_plz: str | None = None

    @model_validator(mode="after")
    def _check_endpoints(self) -> "RoutePair":
        def ok(lat: float | None, lon: float | None, lkz: str | None, plz: str | None) -> bool:
            return (lat is not None and lon is not None) or (bool(lkz) and bool(plz))

        if not ok(self.origin_lat, self.origin_lon, self.origin_lkz, self.origin_plz):
            raise ValueError("origin: Koordinaten (lat/lon) ODER LKZ+PLZ angeben")
        if not ok(self.dest_lat, self.dest_lon, self.dest_lkz, self.dest_plz):
            raise ValueError("dest: Koordinaten (lat/lon) ODER LKZ+PLZ angeben")
        return self


class RouteBatchRequest(BaseModel):
    pairs: list[RoutePair]


def _resolve_endpoint(
    lat: float | None,
    lon: float | None,
    lkz: str | None,
    plz: str | None,
    geocoder: Geocoder | None,
    cache: dict[tuple[str, str], Coord | None],
) -> Coord | None:
    """Endpunkt zu (lat, lon) auflösen. Koordinaten haben Vorrang; sonst LKZ/PLZ über den
    Geocoder. None = keine Koordinate (PLZ nicht gefunden). Dedupe über cache."""
    if lat is not None and lon is not None:
        return (lat, lon)
    key = (str(lkz), str(plz))
    if key not in cache:
        cache[key] = geocoder.resolve(lkz, plz) if geocoder else None
    return cache[key]


@app.get("/health")
def health() -> dict:
    return {
        "status": "ok",
        "engine_ready": app.state.engine is not None,
        "engine_error": app.state.engine_error,
        "geocode_ready": app.state.geocoder is not None,
        "auth_required": get_settings().auth_enabled,
    }


@app.get("/auth/check")
def auth_check(claims: dict | None = Depends(require_token)) -> dict:
    """Vom Add-in genutzt, um ein Token zu validieren (gibt Name + Ablauf zurück)."""
    if claims is None:
        return {"auth_required": False}
    return {"auth_required": True, "name": claims.get("sub"), "exp": claims.get("exp")}


@app.post("/route-batch")
def route_batch(req: RouteBatchRequest, claims: dict | None = Depends(require_token)) -> dict:
    """JSON rein → JSON raus, synchron und parallel über die Engine.
    Das Add-in ruft diesen Endpoint blockweise auf (Reihenfolge bleibt erhalten)."""
    settings = app.state.settings
    if app.state.engine is None:
        raise HTTPException(status_code=503, detail=f"Routing-Engine nicht bereit: {app.state.engine_error}")
    if len(req.pairs) > settings.max_sync_batch:
        raise HTTPException(
            status_code=413,
            detail=f"{len(req.pairs)} Paare > Limit {settings.max_sync_batch}. "
            f"Bitte in kleineren Blöcken senden (das Add-in tut das automatisch).",
        )

    geocoder = app.state.geocoder
    needs_geo = any(
        p.origin_lat is None or p.origin_lon is None or p.dest_lat is None or p.dest_lon is None
        for p in req.pairs
    )
    if needs_geo and geocoder is None:
        raise HTTPException(
            status_code=503,
            detail="Geocoding nicht verfügbar: data/plz_centroids.csv fehlt. "
            "Bitte scripts/build_geocode.sh ausführen.",
        )

    # Endpunkte auflösen (Koordinaten direkt ODER LKZ/PLZ → Zentroid). Dedupe je Request.
    cache: dict[tuple[str, str], Coord | None] = {}
    resolved: list[tuple[Coord | None, Coord | None]] = [
        (
            _resolve_endpoint(p.origin_lat, p.origin_lon, p.origin_lkz, p.origin_plz, geocoder, cache),
            _resolve_endpoint(p.dest_lat, p.dest_lon, p.dest_lkz, p.dest_plz, geocoder, cache),
        )
        for p in req.pairs
    ]

    # Nur vollständig aufgelöste Paare routen; nicht auflösbare bekommen plz_not_found
    # (spart osrm-Aufrufe). route_results-Reihenfolge folgt to_route.
    to_route = [(o, d) for o, d in resolved if o is not None and d is not None]
    route_results = iter(route_pairs(app.state.engine, to_route, settings.workers))

    out = []
    for p, (o, d) in zip(req.pairs, resolved):
        r = next(route_results) if o is not None and d is not None else RouteResult(None, None, PLZ_NOT_FOUND)
        out.append(
            {
                "id": p.id,
                "distance_km": r.distance_km,
                "duration_min": r.duration_min,
                "status": r.status,
                "snap_m": r.snap_m,
                "message": r.message,
                # verwendete/hergeleitete Koordinaten zurückgeben → Add-in schreibt sie sichtbar
                "origin_lat": o[0] if o else None,
                "origin_lon": o[1] if o else None,
                "dest_lat": d[0] if d else None,
                "dest_lon": d[1] if d else None,
            }
        )
    return {"results": out}
