#!/usr/bin/env bash
# Startet Backend (FastAPI, startet osrm-routed selbst) + Frontend (Streamlit) via honcho.
# Liest .env automatisch ein. Beenden mit Ctrl-C (stoppt beide Prozesse + osrm-routed).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

# venv auf den PATH legen, falls nicht bereits aktiv — damit honcho-Kindprozesse
# (uvicorn, streamlit) im /bin/sh die Tools finden.
if [[ -z "${VIRTUAL_ENV:-}" && -d "${ROOT}/venv" ]]; then
  export PATH="${ROOT}/venv/bin:${PATH}"
fi

command -v honcho >/dev/null 2>&1 || {
  echo "FEHLER: 'honcho' nicht gefunden. Installieren: pip install -e '.[dev]'" >&2
  exit 1
}
exec honcho start "$@"
