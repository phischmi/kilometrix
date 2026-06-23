#!/usr/bin/env bash
# Einmaliges Geocoding-Setup: GeoNames-Postal-Daten → data/plz_centroids.csv (Zentroid je PLZ).
# Danach offline nutzbar. Standard: Deutschland (GEONAMES_COUNTRY=DE).
# Datenquelle: GeoNames (CC BY 4.0), https://download.geonames.org/export/zip/
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DATA_DIR="${ROOT}/data"
COUNTRY="${GEONAMES_COUNTRY:-DE}"
ZIP_URL="${ZIP_URL:-https://download.geonames.org/export/zip/${COUNTRY}.zip}"
OUT="${DATA_DIR}/plz_centroids.csv"
TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

mkdir -p "${DATA_DIR}"

echo "Lade GeoNames Postal-Daten: ${ZIP_URL}"
curl -L --fail -o "${TMP}/geonames.zip" "${ZIP_URL}"
unzip -o "${TMP}/geonames.zip" "${COUNTRY}.txt" -d "${TMP}"

echo "==> Zentroide aggregieren"
python3 "${ROOT}/scripts/_geonames_centroids.py" "${TMP}/${COUNTRY}.txt" "${OUT}"

echo "Fertig. Geocoding-Tabelle liegt in ${OUT} — portabel nach Windows/NAS kopierbar."
