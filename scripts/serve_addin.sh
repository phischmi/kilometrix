#!/usr/bin/env bash
# Startet das Backend über HTTPS (für das Office.js-Add-in) auf Port 8443.
# Liefert Add-in-Oberfläche + /route-batch + startet osrm-routed — alles same-origin, offline.
# Erzeugt bei Bedarf ein localhost-Zertifikat (mkcert bevorzugt = vertrauenswürdig,
# sonst openssl-selbstsigniert; letzteres muss einmalig als vertrauenswürdig markiert werden).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

HOST="${ADDIN_HOST:-127.0.0.1}"
PORT="${ADDIN_PORT:-8443}"
CERT_DIR="${ROOT}/certs"
CERT="${CERT_DIR}/localhost.pem"
KEY="${CERT_DIR}/localhost-key.pem"
mkdir -p "${CERT_DIR}"

# venv auf den PATH
if [[ -z "${VIRTUAL_ENV:-}" && -d "${ROOT}/venv" ]]; then
  export PATH="${ROOT}/venv/bin:${PATH}"
fi

if [[ ! -f "${CERT}" || ! -f "${KEY}" ]]; then
  if command -v mkcert >/dev/null 2>&1; then
    echo "==> Zertifikat via mkcert (vertrauenswürdig)…"
    mkcert -install >/dev/null 2>&1 || true
    mkcert -cert-file "${CERT}" -key-file "${KEY}" 127.0.0.1 localhost ::1 >/dev/null
  else
    echo "==> mkcert nicht gefunden — erzeuge selbstsigniertes Zertifikat via openssl."
    echo "    (Einmalig im System als vertrauenswürdig markieren, sonst lädt Excel das Pane nicht."
    echo "     Komfortabler: 'brew install mkcert' und dieses Skript erneut ausführen.)"
    openssl req -x509 -newkey rsa:2048 -nodes -days 825 \
      -keyout "${KEY}" -out "${CERT}" \
      -subj "/CN=localhost/O=Kilometrix" \
      -addext "subjectAltName=DNS:localhost,IP:127.0.0.1,IP:::1" >/dev/null 2>&1
  fi
  echo "    Zertifikat: ${CERT}"
fi

echo "==> Backend (HTTPS) auf https://${HOST}:${PORT}  (Add-in: https://${HOST}:${PORT}/addin/taskpane.html)"
exec uvicorn backend.main:app --host "${HOST}" --port "${PORT}" \
  --ssl-keyfile "${KEY}" --ssl-certfile "${CERT}"
