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
- FastAPI — Job-Engine (Upload, Verarbeitung, Fortschritt, Download)
- Streamlit — Frontend, spricht via HTTP mit FastAPI
- OSRM fürs Routing — siehe "OSRM-Setup"
- pandas + openpyxl (Excel), uvicorn; httpx nur falls HTTP-Fallback

## OSRM-Setup (hier steckt die Komplexität)
OSRM ist C++. Geprüfte Fakten (Stand 2026):
- **Offizielle vorgebaute Binaries** für Windows x86_64, macOS (arm64 + x86_64)
  und Linux liegen in den GitHub-Releases (monatliche v26.x-Releases).
- **Offizielle Python-Bindings**: `pip install osrm`, abi3-Wheels für CPython 3.12+
  auf genau diesen Plattformen. Read-only = Routing-Abfragen — genau das, was wir
  brauchen. Damit läuft Routing **in-process in FastAPI**, ohne Server, ohne Docker.
- **Kein sauberes Scoop-Paket für OSRM** vorhanden — egal, fürs Wheel nicht nötig.
  Scoop installiert nur Python (und optional git).
- CLI-Binaries auf Windows brauchen Zusatz-Runtime (oneTBB, BZip2) — nur relevant
  beim HTTP-Fallback (osrm-routed.exe), nicht beim Wheel.
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
- **Primär:** in-process über das offizielle `osrm`-Python-Paket.
  ⚠️ Exakte API-Signaturen aus README/Stub der Bindings übernehmen, **nicht raten**.
- **Fallback:** `osrm-routed --algorithm mld germany.osrm` als lokaler Subprozess,
  Abfrage per HTTP (`httpx`) auf
  `/route/v1/driving/{lon},{lat};{lon},{lat}?overview=false`.
  Gut dokumentiert, robust, ebenfalls ohne Docker.
- Engine hinter einem schmalen Interface kapseln, damit Primär/Fallback tauschbar.

## Datenfluss
1. Upload Excel über Streamlit → an FastAPI.
2. Spalten-Mapping (UI): `origin_lat`, `origin_lon`, `dest_lat`, `dest_lon`,
   optional ID-Spalten.
3. FastAPI startet Job: pro Zeile eine Route-Abfrage.
4. Fortschritt per Polling (`GET /jobs/{id}`), Streamlit zeigt Balken.
5. Ergebnis-Download: Originaltabelle + `distance_km`, `duration_min`,
   `status` (ok / snapped_far / no_route / error).

## Verarbeitung — Details
- Pro Paar: kürzeste Fahrstrecke, Distanz m → km (2 Nachkommastellen),
  Dauer in Minuten.
- Concurrency: Thread-Pool (z. B. 8–16 Worker); tausende Abfragen lokal =
  Sekunden bis wenige Minuten.
- **Snapping-Plausi:** OSRM snappt auf die nächste routbare Kante. Snap-Distanz
  prüfen; wenn > Schwelle (konfigurierbar), in `status` markieren.
- **Resumable:** bei tausenden Zeilen Zwischenstand in Checkpoint-Datei
  (Parquet/CSV) schreiben, damit ein Abbruch nicht alles verwirft.
- Robuste Fehlerbehandlung pro Zeile — eine kaputte Zeile darf den Job nicht killen.
- Nur Deutschland-Extract: rein innerdeutsche Strecken ok. Grenznahe Routen, die
  kürzer durchs Ausland gingen, werden leicht zu lang — bei Bedarf DACH+-Extract.

## Projektstruktur (Vorschlag)
```
.
├── CLAUDE.md
├── pyproject.toml
├── .env.example
├── data/                # germany.osrm.* (gitignored, groß)
├── backend/
│   ├── main.py          # FastAPI app
│   ├── routing.py       # OSRM-Abstraktion (in-process ODER HTTP-Fallback)
│   ├── jobs.py          # Job-State, Fortschritt, Checkpointing
│   └── io_excel.py      # Excel lesen/schreiben, Spalten-Mapping
├── frontend/
│   └── app.py           # Streamlit
└── scripts/
    └── build_graph.(sh|ps1)   # einmaliges Preprocessing
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

# Backend
uvicorn backend.main:app --reload --port 8000

# Frontend
streamlit run frontend/app.py
```

## Konventionen
- Type Hints überall, kleine fokussierte Funktionen, sprechende Namen.
- Routing-Engine hinter einem schmalen Interface (Bindings ↔ HTTP-Fallback austauschbar).
- Keine harten Pfade; Konfiguration (Graph-Pfad, Worker-Zahl, Snap-Limit) via .env/Settings.
- Kommentare/Antworten auf Deutsch sind ok.

## Offene Punkte / zuerst klären
1. Exakte API der offiziellen `osrm`-Python-Bindings verifizieren (Methode für
   Einzelroute, Distanz-Feld). Wenn unhandlich → HTTP-Fallback nehmen.
2. Spaltenschema der echten Excel bestätigen (Header, ID-Spalten, ein vs. mehrere Blätter).
3. "Kombinationen" = konkrete Paare (eine Route pro Zeile)? Annahme: ja.
4. RAM beim Graph-Build auf der gewählten Build-Maschine real messen.
