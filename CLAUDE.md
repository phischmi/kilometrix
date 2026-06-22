# CLAUDE.md — Strecken-km-Batch (OSRM Distanz-Tool)

> Projektnamen gern anpassen. Diese Datei ist der Kontext für Claude Code.

## Ziel
Offline-Berechnung von Straßen-Kilometern (und Fahrzeiten) für mehrere tausend
Origin→Destination-Paare in Deutschland. Eingabe: Excel mit Koordinaten je Paar.
Ausgabe: dieselbe Tabelle plus `distance_km`, `duration_min`, `status`.
**Keine externe Routing-API** (keine Limits, keine Kosten, keine Datenweitergabe).

## Harte Randbedingungen (nicht verhandelbar)
- **Client/Entwicklung ohne Docker:** Auf dem **Windows-Firmenlaptop** (Installation via
  **Scoop** + pip, keine Admin-Rechte annehmen) und auf **macOS** (Apple Silicon; ggf.
  x86_64) laufen Backend + Tools als native Prozesse im venv.
- **Zentraler Betrieb auf dem Linux-NAS läuft via Docker** (Compose hinter Traefik):
  `osrm` + `app` als Container — siehe `docker-compose*.yml`. Docker gilt also nur für die
  NAS, nicht für den Firmenlaptop.
- **Max. 16 GB RAM** auf jeder Zielmaschine.
- Offline-fähig nach einmaligem Daten-Setup.

## Stack
- Python 3.12+ (Voraussetzung für die OSRM-Wheels)
- FastAPI — schlankes Backend: liefert das Office.js-Add-in aus (same-origin HTTPS),
  stellt `/route-batch` (JSON) bereit, verwaltet osrm-routed. README ist die Quelle der Wahrheit.
- **Office.js-Excel-Add-in** — die Bedienoberfläche (Task Pane, liest/schreibt das Blatt
  direkt). Das frühere Streamlit-Frontend und der Datei-Job-Flow wurden entfernt.
- OSRM fürs Routing über **osrm-routed** (lokaler HTTP-Subprozess) — siehe "OSRM-Setup".
- uvicorn, httpx, pydantic-settings. Kein pandas/openpyxl/Streamlit mehr.

## OSRM-Setup (hier steckt die Komplexität)
OSRM ist C++. Geprüfte Fakten (Stand 2026):
- **Offizielle vorgebaute Binaries** für Windows x86_64, macOS (arm64 + x86_64)
  und Linux liegen in den GitHub-Releases (monatliche v26.x-Releases).
- **Python-Bindings** (`pip install osrm-bindings`, abi3-Wheels für CPython 3.12+):
  ⚠️ in der Praxis **nicht nutzbar** — das Wheel ist archiviert und im Datenformat hinter
  osrm-backend Stable, lädt den Graphen nicht (siehe Routing-Strategie). Wir nutzen osrm-routed.
- macOS: `brew install osrm-backend` liefert CLI-Tools (`osrm-extract/-partition/-customize`)
  **und** `osrm-routed` + `car.lua`. Windows: Binaries aus den GitHub-Releases (oneTBB, BZip2).
- **Kein sauberes Scoop-Paket für OSRM** — Scoop installiert nur Python (und optional git).
- macOS-Binaries v6+ setzen macOS 15 (Sequoia)+ voraus.

### Graph einmal bauen, dann verteilen
Das **Preprocessing** (osrm-extract → osrm-partition → osrm-customize, MLD-Pipeline)
braucht die CLI-Binaries und ist der speicherhungrige Teil:
- `germany-latest.osm.pbf` von Geofabrik (~4 GB).
- Car-Profil Deutschland **passt in 16 GB RAM, aber knapp** — beim Build andere
  Programme schließen; bei `std::bad_alloc` ein Swapfile anlegen.
- Die erzeugten `.osrm.*`-Dateien sind **portable Daten**: einmal bauen (Maschine
  mit dem meisten freien RAM), dann auf Mac/Windows/NAS kopieren und nur abfragen.

### Routing-Strategie
- **`osrm-routed`**, Abfrage per HTTP (`httpx`) auf
  `/route/v1/driving/{lon},{lat};{lon},{lat}?overview=false`. Hinter dem schmalen
  Interface `RoutingEngine` (routing.py) → `HttpEngine`.
- **Zwei Betriebsarten:** lokal startet das Backend `osrm-routed` selbst als Subprozess
  (`MANAGE_OSRM_ROUTED=true`, siehe osrm_process.py); auf dem NAS läuft `osrm-routed` als
  eigener Container und das Backend zeigt nur darauf (`MANAGE_OSRM_ROUTED=false`,
  `OSRM_ROUTED_URL`).
- **`--mmap` / `OSRM_ROUTED_MMAP` (Default an):** Graph von der Platte mappen statt komplett
  ins RAM laden → deutlich weniger Leerlauf-Speicher (wichtig auf der RAM-knappen NAS), erste
  Abfrage minimal langsamer. Lokal als Flag im Subprozess gesetzt, im Compose im osrm-Command.
- Die in-process `osrm-bindings` wurden entfernt (archiviertes Wheel, Fingerprint-Mismatch zum
  aktuell gebauten Graphen — unbrauchbar).
- Graph standardmäßig mit **LKW-Profil** `profiles/truck.lua` gebaut (von car.lua abgeleitet).

## Datenfluss (Office.js-Add-in)
0. Add-in nur in Excel funktionsfähig: wird die Seite außerhalb von Excel (z. B. direkt
   im Browser) geöffnet, zeigt das Add-in einen Hinweis statt des funktionslosen UIs
   (Erkennung über `Office.onReady`-Host + Timeout-Fallback).
1. Task Pane öffnen; Bereich (ganzes Blatt / Markierung) + Spalten-Mapping wählen
   (`origin_lat`, `origin_lon`, `dest_lat`, `dest_lon`).
2. „Strecken berechnen" → das Add-in liest die Koordinaten **blockweise** (2000 Zeilen).
3. Pro Block: `POST /route-batch` (JSON) → FastAPI rechnet parallel über die Engine.
4. Ergebnis je Block sofort in die Nachbarspalten geschrieben: `distance_km`,
   `duration_min`, `status` (ok / snapped_far / no_route / error), `snap_m`.
   Fortschrittsbalken läuft mit.

## Verarbeitung — Details
- Pro Paar: kürzeste Fahrstrecke, Distanz m → km (2 Nachkommastellen), Dauer in Minuten.
- Concurrency: Thread-Pool (`WORKERS`, Default 8 = gemessener Sweet Spot) pro /route-batch.
- **Snapping-Plausi:** OSRM snappt auf die nächste routbare Kante. Snap-Distanz prüfen;
  wenn > Schwelle (`SNAP_LIMIT_M`), in `status` als `snapped_far` markieren.
- **Große Blätter:** Statt serverseitigem Checkpointing schreibt das Add-in streamend
  blockweise zurück — Teilergebnisse stehen sofort im Blatt, Speicher bleibt konstant.
- Robuste Fehlerbehandlung pro Zeile — eine kaputte Zeile (z. B. leere Koordinate) wird
  als `error` markiert, der Lauf läuft weiter.
- Nur Deutschland-Extract: rein innerdeutsche Strecken ok. Grenznahe Routen, die kürzer
  durchs Ausland gingen, werden leicht zu lang — bei Bedarf DACH+-Extract.

## Projektstruktur
```
.
├── CLAUDE.md  ·  README.md (Quelle der Wahrheit)  ·  pyproject.toml  ·  .env.example
├── docker-compose.yml       # NAS-Betrieb (Traefik): osrm + app, App-Image lokal gebaut
├── docker-compose.prod.yml  # NAS-Betrieb: App-Image aus GHCR (CI-Build) ziehen
├── data/                    # germany.osrm.* (gitignored, groß)
├── backend/
│   ├── main.py              # FastAPI: Add-in-Auslieferung, /health, /route-batch
│   ├── routing.py           # Engine-Interface: HttpEngine (osrm-routed) | OsrmBindingsEngine
│   ├── osrm_process.py      # osrm-routed als Subprozess starten/stoppen
│   └── config.py            # Settings via .env
├── addin/                   # Office.js-Add-in (manifest.xml, taskpane.html, styles.css, app.js)
└── scripts/
    ├── build_graph.(sh|ps1) # einmaliges Preprocessing
    └── serve_addin.sh       # HTTPS-Backend (Cert + :8443) für das Add-in
```

## Commands
```bash
# Setup (Mac/Linux)
python -m venv .venv && source .venv/bin/activate
pip install -e .

# Setup (Windows, Scoop)
scoop install python
python -m venv .venv && .venv\Scripts\activate
pip install -e .

# Add-in-Backend starten (HTTPS, startet osrm-routed selbst)
./scripts/serve_addin.sh        # https://127.0.0.1:8443/addin/taskpane.html

# Tests
pytest

# NAS (Docker, zentraler Betrieb hinter Traefik)
docker compose -f docker-compose.prod.yml pull   # App-Image aus GHCR
docker compose -f docker-compose.prod.yml up -d   # osrm + app als Container
```

## Konventionen
- Type Hints überall, kleine fokussierte Funktionen, sprechende Namen.
- Routing-Engine hinter einem schmalen Interface (`RoutingEngine` in routing.py).
- Keine harten Pfade; Konfiguration (Graph-Pfad, Worker-Zahl, Snap-Limit) via .env/Settings.
- Kommentare/Antworten auf Deutsch sind ok.

## Geklärte Punkte (Stand: erledigt)
1. ✅ Bindings-API verifiziert → Wheel inkompatibel, daher osrm-routed (HTTP) genutzt.
2. ✅ Excel-Schema: getrennte Spalten `origin_lat/lon, dest_lat/lon`, eine Route pro Zeile.
3. ✅ Graph-Build auf M4 (16 GB): Peak ~7,7 GB beim Customize — passt.
4. Bedienung erfolgt über das Office.js-Add-in (kein Streamlit/Datei-Flow mehr).
5. ✅ Produktiv-Betrieb läuft via Docker-Compose auf der NAS (osrm + app hinter Traefik).
6. ✅ `osrm-routed` mit `--mmap` (`OSRM_ROUTED_MMAP`, Default an) → weniger Leerlauf-RAM,
   sowohl im lokalen Subprozess als auch im Compose-Command gesetzt.
7. ✅ Add-in zeigt außerhalb von Excel einen Hinweis statt funktionslosem UI.
