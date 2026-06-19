# CLAUDE.md — Strecken-km-Batch (OSRM Distanz-Tool)

> Projektnamen gern anpassen. Diese Datei ist der Kontext für Claude Code.

## Ziel
Offline-Berechnung von Straßen-Kilometern (und Fahrzeiten) für mehrere tausend
Origin→Destination-Paare in Deutschland. Eingabe: Excel mit Koordinaten je Paar.
Ausgabe: dieselbe Tabelle plus `distance_km`, `duration_min`, `status`.
**Keine externe Routing-API** (keine Limits, keine Kosten, keine Datenweitergabe).

## Harte Randbedingungen (nicht verhandelbar)
- **Kein Docker.** Alles als native Prozesse in einem venv.
- Muss auf **Windows-Firmenlaptop** laufen, Installation via **Scoop** + pip.
  Keine Admin-Rechte annehmen.
- Entwicklung auf **macOS** (Apple Silicon; ggf. auch x86_64 berücksichtigen).
- Optionaler Betrieb auf Linux-NAS.
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
- **Genutzt: `osrm-routed`** (`ENGINE=http`) als lokaler Subprozess (vom Backend verwaltet),
  Abfrage per HTTP (`httpx`) auf `/route/v1/driving/{lon},{lat};{lon},{lat}?overview=false`.
  Robust, gut dokumentiert, ohne Docker.
- **Nicht genutzt (Steckplatz):** in-process `osrm-bindings` (`ENGINE=bindings`). Das PyPI-Wheel
  ist archiviert und im Datenformat ~135 Commits hinter osrm-backend Stable → lädt einen mit
  aktuellem `osrm-backend` gebauten Graphen nicht (Fingerprint-Mismatch). Nur via versionsgleichem
  Source-Build nutzbar. Engine steckt hinter `RoutingEngine` (routing.py), bleibt also tauschbar.

## Datenfluss (Office.js-Add-in)
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
