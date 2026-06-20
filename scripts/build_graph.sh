#!/usr/bin/env bash
# Einmaliges OSRM-Preprocessing (MLD-Pipeline) — SEPARAT vom Tool, manuell auf dem Mac.
# Ergebnis: data/germany.osrm.* — danach nach Windows/NAS kopierbar (portable Daten).
#
# Voraussetzung: OSRM-CLI-Binaries (osrm-extract/-partition/-customize) aus den
# GitHub-Releases von Project-OSRM/osrm-backend. NICHT das Python-Wheel — das ist
# read-only und kann keinen Graph bauen. Kein Docker.
#
# RAM-Hinweis: Car-Profil Deutschland passt knapp in 16 GB. Andere Programme schließen.
# Bei `std::bad_alloc` ein Swapfile anlegen oder auf der Maschine mit dem meisten RAM bauen.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DATA_DIR="${ROOT}/data"
PBF_URL="${PBF_URL:-https://download.geofabrik.de/europe/germany-latest.osm.pbf}"
# WICHTIG: Der Ausgabe-Basisname von osrm-extract leitet sich aus dem PBF-Dateinamen ab.
# Datei daher als germany.osm.pbf speichern, damit extract germany.osrm.* erzeugt (= BASE).
PBF="${DATA_DIR}/germany.osm.pbf"
PROFILE="${OSRM_PROFILE:-$(command -v osrm-extract >/dev/null 2>&1 && dirname "$(command -v osrm-extract)")/../share/osrm/profiles/car.lua}"
BASE="${DATA_DIR}/germany.osrm"

mkdir -p "${DATA_DIR}"

for bin in osrm-extract osrm-partition osrm-customize; do
  command -v "${bin}" >/dev/null 2>&1 || {
    echo "FEHLER: '${bin}' nicht im PATH. Auf macOS: 'brew install osrm-backend'." >&2
    exit 1
  }
done

if [[ ! -f "${PBF}" ]]; then
  echo "Lade OSM-Daten: ${PBF_URL}"
  curl -L --fail -o "${PBF}" "${PBF_URL}"
fi

# Standard-Profil: LKW (profiles/truck.lua). Es wird neben das mitgelieferte car.lua
# kopiert, damit dessen `lib/` gefunden wird. Anderes Profil: OSRM_PROFILE=<pfad> setzen.
if [[ -z "${OSRM_PROFILE:-}" ]]; then
  PROFILE_FILE="${PROFILE_FILE:-${ROOT}/profiles/truck.lua}"
  [[ "${PROFILE_FILE}" = /* ]] || PROFILE_FILE="${ROOT}/${PROFILE_FILE}"
  cp "${PROFILE_FILE}" "$(dirname "${PROFILE}")/"
  PROFILE="$(dirname "${PROFILE}")/$(basename "${PROFILE_FILE}")"
fi

echo "==> osrm-extract (Profil: ${PROFILE})"
osrm-extract -p "${PROFILE}" "${PBF}"

echo "==> osrm-partition"
osrm-partition "${BASE}"

echo "==> osrm-customize"
osrm-customize "${BASE}"

echo "Fertig. Graph liegt in ${BASE}.* — OSRM_ALGORITHM=MLD in .env setzen."
