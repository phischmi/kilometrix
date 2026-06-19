"""FastAPI-Jobengine: Upload, Job-Start, Fortschritt, Download.

Das Backend dient ausschließlich der km-Berechnung. Der OSRM-Graph wird separat
mit scripts/build_graph.sh gebaut und nach data/ kopiert.
"""

from __future__ import annotations

import threading
import uuid
from contextlib import asynccontextmanager
from pathlib import Path

import pandas as pd
from fastapi import FastAPI, HTTPException, UploadFile
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import FileResponse, RedirectResponse
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel

from backend.config import get_settings
from backend.io_excel import ColumnMapping, read_workbook, suggest_mapping
from backend.jobs import JobStore, run_job
from backend.osrm_process import OsrmRoutedProcess
from backend.routing import get_engine, route_pairs


@asynccontextmanager
async def lifespan(app: FastAPI):
    settings = get_settings()
    settings.ensure_dirs()
    app.state.settings = settings
    app.state.store = JobStore()
    app.state.osrm_proc = None
    # osrm-routed bei Bedarf starten, dann Engine laden. Fehlt der Graph/das Binary,
    # startet das Backend trotzdem; Routen-Endpunkte melden dann 503 mit Hinweis.
    try:
        if settings.engine == "http" and settings.manage_osrm_routed:
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

# Lokales Tool: CORS offen, damit auch browserbasierte Aufrufer (z. B. Office.js-Add-in)
# das Backend erreichen. VBA/WinHttp braucht das nicht.
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"],
)

# Office.js-Add-in: FastAPI liefert die statischen Dateien selbst aus (same-origin),
# damit das Add-in offline läuft und keine CORS-/Mixed-Content-Probleme entstehen.
_ADDIN_DIR = Path(__file__).resolve().parent.parent / "addin"
if _ADDIN_DIR.is_dir():
    app.mount("/addin", StaticFiles(directory=_ADDIN_DIR, html=True), name="addin")


@app.get("/", include_in_schema=False)
def root():
    return RedirectResponse(url="/addin/taskpane.html")


class StartJobRequest(BaseModel):
    file_token: str
    origin_lat: str
    origin_lon: str
    dest_lat: str
    dest_lon: str
    id_columns: list[str] = []


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
    }


@app.post("/route-batch")
def route_batch(req: RouteBatchRequest) -> dict:
    """JSON rein → JSON raus. Synchron, parallel über die Engine. Für Add-ins/Skripte.
    Sehr große Mengen (> max_sync_batch) bitte über den Datei-Job-Flow (/upload, /jobs)."""
    settings = app.state.settings
    if app.state.engine is None:
        raise HTTPException(status_code=503, detail=f"Routing-Engine nicht bereit: {app.state.engine_error}")
    if len(req.pairs) > settings.max_sync_batch:
        raise HTTPException(
            status_code=413,
            detail=f"{len(req.pairs)} Paare > Limit {settings.max_sync_batch}. "
            f"Für so große Mengen den Datei-Job-Flow (/upload + /jobs) nutzen.",
        )

    coords = [
        ((p.origin_lat, p.origin_lon), (p.dest_lat, p.dest_lon)) for p in req.pairs
    ]
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


@app.post("/upload")
async def upload(file: UploadFile) -> dict:
    settings = app.state.settings
    token = uuid.uuid4().hex
    suffix = Path(file.filename or "upload.xlsx").suffix or ".xlsx"
    dest = settings.upload_dir / f"{token}{suffix}"
    dest.write_bytes(await file.read())

    try:
        df, columns = read_workbook(dest)
    except Exception as exc:
        dest.unlink(missing_ok=True)
        raise HTTPException(status_code=400, detail=f"Excel konnte nicht gelesen werden: {exc}")

    preview = df.head(5).fillna("").astype(str).to_dict(orient="records")
    return {
        "file_token": token,
        "filename": dest.name,
        "rows": len(df),
        "columns": columns,
        "suggested_mapping": suggest_mapping(columns),
        "preview": preview,
    }


@app.post("/jobs")
def start_job(req: StartJobRequest) -> dict:
    settings = app.state.settings
    if app.state.engine is None:
        raise HTTPException(status_code=503, detail=f"Routing-Engine nicht bereit: {app.state.engine_error}")

    matches = list(settings.upload_dir.glob(f"{req.file_token}.*"))
    if not matches:
        raise HTTPException(status_code=404, detail="Unbekanntes file_token — bitte erneut hochladen.")

    df, _ = read_workbook(matches[0])
    mapping = ColumnMapping(
        origin_lat=req.origin_lat,
        origin_lon=req.origin_lon,
        dest_lat=req.dest_lat,
        dest_lon=req.dest_lon,
        id_columns=req.id_columns,
    )
    missing = [c for c in mapping.coord_columns if c not in df.columns]
    if missing:
        raise HTTPException(status_code=400, detail=f"Spalten fehlen in der Tabelle: {missing}")

    job = app.state.store.create()
    thread = threading.Thread(
        target=run_job,
        args=(job, app.state.store, df, mapping, app.state.engine, settings),
        daemon=True,
    )
    thread.start()
    return {"job_id": job.id}


@app.get("/jobs/{job_id}")
def job_status(job_id: str) -> dict:
    job = app.state.store.get(job_id)
    if job is None:
        raise HTTPException(status_code=404, detail="Job nicht gefunden.")
    return job.public()


@app.get("/jobs/{job_id}/download")
def job_download(job_id: str):
    job = app.state.store.get(job_id)
    if job is None:
        raise HTTPException(status_code=404, detail="Job nicht gefunden.")
    if not job.result_path or not Path(job.result_path).exists():
        raise HTTPException(status_code=409, detail="Ergebnis noch nicht verfügbar.")
    return FileResponse(
        job.result_path,
        media_type="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
        filename=f"kilometrix_{job_id}.xlsx",
    )
