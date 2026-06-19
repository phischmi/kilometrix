"""Job-State, Fortschritt, parallele Verarbeitung mit Checkpointing."""

from __future__ import annotations

import threading
import uuid
from concurrent.futures import ThreadPoolExecutor, as_completed
from dataclasses import asdict, dataclass, field
from datetime import datetime, timezone
from pathlib import Path

import pandas as pd

from backend.config import Settings
from backend.io_excel import ColumnMapping, write_results
from backend.routing import ERROR, OK, RouteResult, RoutingEngine

PENDING = "pending"
RUNNING = "running"
DONE = "done"
FAILED = "error"


@dataclass
class Job:
    id: str
    status: str = PENDING
    total: int = 0
    done: int = 0
    ok: int = 0
    errors: int = 0
    message: str | None = None
    result_path: str | None = None
    created_at: str = field(default_factory=lambda: datetime.now(timezone.utc).isoformat())

    def public(self) -> dict:
        d = asdict(self)
        d["progress"] = round(self.done / self.total, 4) if self.total else 0.0
        return d


class JobStore:
    def __init__(self) -> None:
        self._jobs: dict[str, Job] = {}
        self._lock = threading.Lock()

    def create(self) -> Job:
        job = Job(id=uuid.uuid4().hex)
        with self._lock:
            self._jobs[job.id] = job
        return job

    def get(self, job_id: str) -> Job | None:
        with self._lock:
            return self._jobs.get(job_id)

    def update(self, job_id: str, **changes) -> None:
        with self._lock:
            job = self._jobs[job_id]
            for k, v in changes.items():
                setattr(job, k, v)


def _checkpoint_path(settings: Settings, job_id: str) -> Path:
    return settings.checkpoint_dir / f"{job_id}.parquet"


def _load_checkpoint(path: Path) -> dict[int, RouteResult]:
    """Liest einen Checkpoint zurück (resumable). Key = Zeilenindex."""
    if not path.exists():
        return {}
    cp = pd.read_parquet(path)
    out: dict[int, RouteResult] = {}
    for _, row in cp.iterrows():
        out[int(row["row"])] = RouteResult(
            distance_km=_none_if_nan(row["distance_km"]),
            duration_min=_none_if_nan(row["duration_min"]),
            status=str(row["status"]),
            snap_m=_none_if_nan(row["snap_m"]),
        )
    return out


def _write_checkpoint(path: Path, results: dict[int, RouteResult]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    rows = [
        {
            "row": idx,
            "distance_km": r.distance_km,
            "duration_min": r.duration_min,
            "status": r.status,
            "snap_m": r.snap_m,
        }
        for idx, r in results.items()
    ]
    tmp = path.with_suffix(".parquet.tmp")
    pd.DataFrame(rows).to_parquet(tmp, index=False)
    tmp.replace(path)


def _none_if_nan(v):
    return None if v is None or (isinstance(v, float) and pd.isna(v)) else v


def run_job(
    job: Job,
    store: JobStore,
    df: pd.DataFrame,
    mapping: ColumnMapping,
    engine: RoutingEngine,
    settings: Settings,
) -> None:
    """Verarbeitet alle Zeilen parallel. Robust pro Zeile, resumable via Checkpoint."""
    total = len(df)
    store.update(job.id, status=RUNNING, total=total)

    cp_path = _checkpoint_path(settings, job.id)
    results: dict[int, RouteResult] = _load_checkpoint(cp_path)
    cp_lock = threading.Lock()

    done = len(results)
    ok = sum(1 for r in results.values() if r.status != ERROR)
    errors = sum(1 for r in results.values() if r.status == ERROR)
    store.update(job.id, done=done, ok=ok, errors=errors)

    todo = [i for i in range(total) if i not in results]

    def work(idx: int) -> tuple[int, RouteResult]:
        row = df.iloc[idx]
        try:
            origin = (float(row[mapping.origin_lat]), float(row[mapping.origin_lon]))
            dest = (float(row[mapping.dest_lat]), float(row[mapping.dest_lon]))
        except (TypeError, ValueError) as exc:
            return idx, RouteResult(None, None, ERROR, message=f"ungültige Koordinaten: {exc}")
        return idx, engine.route(origin, dest)

    try:
        with ThreadPoolExecutor(max_workers=settings.workers) as pool:
            futures = [pool.submit(work, idx) for idx in todo]
            for fut in as_completed(futures):
                idx, result = fut.result()
                results[idx] = result
                done += 1
                if result.status == ERROR:
                    errors += 1
                else:
                    ok += 1
                store.update(job.id, done=done, ok=ok, errors=errors)

                if done % settings.checkpoint_every == 0:
                    with cp_lock:
                        _write_checkpoint(cp_path, results)

        _write_checkpoint(cp_path, results)

        ordered = [results[i] for i in range(total)]
        out_path = settings.result_dir / f"{job.id}.xlsx"
        write_results(df, ordered, out_path)
        store.update(job.id, status=DONE, result_path=str(out_path))
    except Exception as exc:  # globaler Schutz: Job sauber als fehlgeschlagen markieren
        store.update(job.id, status=FAILED, message=str(exc))
