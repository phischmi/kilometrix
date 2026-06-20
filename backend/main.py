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
from pydantic import BaseModel

from backend.config import get_settings
from backend.osrm_process import OsrmRoutedProcess
from backend.routing import get_engine, route_pairs
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
            )
            proc.start()
            app.state.osrm_proc = proc
        app.state.engine = get_engine(settings)
        app.state.engine_error = None
    except Exception as exc:
        app.state.engine = None
        app.state.engine_error = str(exc)
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
    id: str | int | None = None
    origin_lat: float
    origin_lon: float
    dest_lat: float
    dest_lon: float


class RouteBatchRequest(BaseModel):
    pairs: list[RoutePair]


@app.get("/health")
def health() -> dict:
    return {
        "status": "ok",
        "engine_ready": app.state.engine is not None,
        "engine_error": app.state.engine_error,
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

    coords = [((p.origin_lat, p.origin_lon), (p.dest_lat, p.dest_lon)) for p in req.pairs]
    results = route_pairs(app.state.engine, coords, settings.workers)
    return {
        "results": [
            {
                "id": p.id,
                "distance_km": r.distance_km,
                "duration_min": r.duration_min,
                "status": r.status,
                "snap_m": r.snap_m,
                "message": r.message,
            }
            for p, r in zip(req.pairs, results)
        ]
    }
