"""Streamlit-Frontend — ausschließlich km-Berechnung: Upload, Mapping, Fortschritt, Download.

Kein Graph-Build hier. Spricht via HTTP mit dem FastAPI-Backend.
"""

from __future__ import annotations

import os
import time

import httpx
import streamlit as st

BACKEND_URL = os.environ.get("BACKEND_URL", "http://127.0.0.1:8000")
COORD_KEYS = ["origin_lat", "origin_lon", "dest_lat", "dest_lon"]

st.set_page_config(page_title="Kilometrix", layout="centered")
st.title("Kilometrix — Straßen-Kilometer berechnen")


def _select_index(columns: list[str], suggested: str | None) -> int:
    if suggested and suggested in columns:
        return columns.index(suggested)
    return 0


# --- Backend-Status ---
try:
    health = httpx.get(f"{BACKEND_URL}/health", timeout=5).json()
except Exception as exc:
    st.error(f"Backend nicht erreichbar unter {BACKEND_URL}: {exc}")
    st.stop()

if not health.get("engine_ready"):
    st.warning(f"Routing-Engine nicht bereit: {health.get('engine_error')}")

# --- 1. Upload ---
uploaded = st.file_uploader("Excel mit Koordinaten hochladen", type=["xlsx", "xls"])
if uploaded is None:
    st.stop()

if st.session_state.get("uploaded_name") != uploaded.name:
    with st.spinner("Lade Datei hoch …"):
        resp = httpx.post(
            f"{BACKEND_URL}/upload",
            files={"file": (uploaded.name, uploaded.getvalue())},
            timeout=120,
        )
    if resp.status_code != 200:
        st.error(f"Upload fehlgeschlagen: {resp.text}")
        st.stop()
    st.session_state["upload"] = resp.json()
    st.session_state["uploaded_name"] = uploaded.name
    st.session_state.pop("job_id", None)

info = st.session_state["upload"]
st.success(f"{info['rows']} Zeilen geladen.")
st.dataframe(info["preview"], use_container_width=True)

# --- 2. Spalten-Mapping ---
st.subheader("Spalten zuordnen")
columns = info["columns"]
suggested = info.get("suggested_mapping", {})
labels = {
    "origin_lat": "Start — Breite (lat)",
    "origin_lon": "Start — Länge (lon)",
    "dest_lat": "Ziel — Breite (lat)",
    "dest_lon": "Ziel — Länge (lon)",
}
mapping = {}
cols = st.columns(2)
for i, key in enumerate(COORD_KEYS):
    with cols[i % 2]:
        mapping[key] = st.selectbox(
            labels[key], columns, index=_select_index(columns, suggested.get(key)), key=f"map_{key}"
        )

# --- 3. Start ---
if st.button("Berechnung starten", type="primary", disabled=not health.get("engine_ready")):
    resp = httpx.post(
        f"{BACKEND_URL}/jobs",
        json={"file_token": info["file_token"], **mapping},
        timeout=30,
    )
    if resp.status_code != 200:
        st.error(f"Start fehlgeschlagen: {resp.text}")
    else:
        st.session_state["job_id"] = resp.json()["job_id"]

# --- 4. Fortschritt + Download ---
job_id = st.session_state.get("job_id")
if job_id:
    st.subheader("Fortschritt")
    bar = st.progress(0.0)
    status_box = st.empty()
    while True:
        status = httpx.get(f"{BACKEND_URL}/jobs/{job_id}", timeout=10).json()
        bar.progress(min(status["progress"], 1.0))
        status_box.write(
            f"Status: **{status['status']}** — {status['done']}/{status['total']} "
            f"(ok: {status['ok']}, Fehler: {status['errors']})"
        )
        if status["status"] in ("done", "error"):
            break
        time.sleep(1.0)

    if status["status"] == "done":
        dl = httpx.get(f"{BACKEND_URL}/jobs/{job_id}/download", timeout=60)
        st.download_button(
            "Ergebnis herunterladen (xlsx)",
            data=dl.content,
            file_name=f"kilometrix_{job_id}.xlsx",
            mime="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
        )
    else:
        st.error(f"Job fehlgeschlagen: {status.get('message')}")
