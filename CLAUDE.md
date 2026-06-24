# CLAUDE.md — Kilometrix (OSRM Distanz-Tool)

> Diese Datei ist der Kontext für Claude Code.

## Ziel
Offline-Berechnung von Straßen-Kilometern (und Fahrzeiten) für mehrere tausend
Origin→Destination-Paare in Deutschland. Eingabe: Excel mit Koordinaten **oder** LKZ/PLZ je Paar.
Ausgabe: dieselbe Tabelle plus `distance_km`, `duration_min`, `status` (und im Geocoding-Modus
die hergeleiteten Koordinaten). **Keine externe Routing-API** (keine Limits, keine Kosten,
keine Datenweitergabe).

## Harte Randbedingungen (nicht verhandelbar)
- **Client/Entwicklung ohne Docker:** Auf dem **Windows-Firmenlaptop** (Installation via
  **Scoop**, keine Admin-Rechte annehmen) und auf **macOS** (Apple Silicon; ggf. x86_64)
  läuft das Backend als natives Go-Binary, die GUI als native Wails-App.
- **Zentraler Betrieb auf dem Linux-NAS via Docker** (Compose hinter Traefik): `osrm` + `app`
  als Container — siehe `docker-compose*.yml`. Docker gilt nur für die NAS, nicht für den Laptop.
- **Max. 16 GB RAM** auf jeder Zielmaschine.
- Offline-fähig nach einmaligem Daten-Setup.

## Stack
- **Go (≥1.23)** — das gesamte Backend ist ein einzelnes Binary `kilometrix` (reine stdlib,
  **keine externen Abhängigkeiten**). Subcommands: `serve`, `build-graph`, `build-geocode`,
  `token`, `config`. README ist die Quelle der Wahrheit.
- **Office.js-Excel-Add-in** ([`addin/`](addin)) — die Bedienoberfläche (Task Pane, liest/schreibt
  das Blatt direkt). Wird vom Backend same-origin über HTTPS ausgeliefert.
- **Wails-Desktop-GUI** ([`gui/`](gui), eigenes Go-Modul + Vanilla TS/Vite/Tailwind) — steuert
  das Binary über dessen Subcommands. **Nicht** im Docker-Image (separat gehalten).
- OSRM fürs Routing über **osrm-routed** (lokaler HTTP-Subprozess oder NAS-Container).

## OSRM-Setup (hier steckt die Komplexität)
OSRM ist C++. `kilometrix build-graph` orchestriert die CLI-Tools; das Routing spricht
`osrm-routed` per HTTP an. Geprüfte Fakten (Stand 2026):
- **Offizielle vorgebaute Binaries** für Windows x86_64, macOS (arm64+x86_64) und Linux in den
  GitHub-Releases. macOS: `brew install osrm-backend` liefert `osrm-extract/-partition/-customize`
  + `osrm-routed` + `car.lua`. Windows: Binaries aus den Releases (oneTBB, BZip2). Kein
  Scoop-Paket für OSRM.
- Die früheren Python-`osrm-bindings` sind unbrauchbar (archiviertes Wheel) — daher osrm-routed.

### Graph einmal bauen, dann verteilen
`kilometrix build-graph` (osrm-extract → -partition → -customize, MLD) ist der speicherhungrige
Teil: `germany-latest.osm.pbf` von Geofabrik (~4 GB), Peak ~7,7 GB beim Customize (passt knapp in
16 GB). Die erzeugten `data/germany.osrm.*` sind **portabel** — einmal bauen, dann auf
Mac/Windows/NAS kopieren. Graph + osrm-routed müssen dieselbe osrm-backend-Version haben.

### Routing-Strategie
- `osrm-routed`, Abfrage per HTTP auf `/route/v1/driving/{lon},{lat};{lon},{lat}?overview=false`.
  Hinter dem schmalen Interface `routing.Engine` ([`internal/routing`](internal/routing)) →
  `HTTPEngine`. Achtung: OSRM erwartet (lon, lat).
- **Zwei Betriebsarten:** lokal startet `serve` osrm-routed selbst als Subprozess
  (`MANAGE_OSRM_ROUTED=true`, [`internal/osrm`](internal/osrm)); auf dem NAS läuft osrm-routed als
  eigener Container, das Backend zeigt nur darauf (`MANAGE_OSRM_ROUTED=false`, `OSRM_ROUTED_URL`).
- **`--mmap` / `OSRM_ROUTED_MMAP` (Default an):** Graph von der Platte mappen statt ins RAM laden
  → weniger Leerlauf-Speicher (wichtig auf der RAM-knappen NAS), erste Abfrage minimal langsamer.
- Graph standardmäßig mit **LKW-Profil** `profiles/truck.lua` (von car.lua abgeleitet).

## Geocoding (LKZ/PLZ → Zentroid)
- `kilometrix build-geocode` lädt den GeoNames-Postal-Datensatz (CC BY 4.0) und schreibt
  **nativ in Go** `data/plz_centroids.csv` (`country,plz,lat,lon`, Zentroid je PLZ).
- Im `/route-batch` wird jeder Endpunkt **entweder** über Koordinaten **oder** über LKZ/PLZ
  angegeben; LKZ/PLZ wird serverseitig im selben Aufruf aufgelöst (ein Round-Trip, mit Dedupe).
  Unbekannte PLZ → Status `plz_not_found`. LKZ = ISO-3166 alpha-2, DE-PLZ werden auf 5 Stellen
  aufgefüllt. [`internal/geocode`](internal/geocode).

## Datenfluss (Office.js-Add-in)
0. Add-in nur in Excel funktionsfähig: außerhalb (z. B. im Browser) Hinweis statt UI
   (`Office.onReady`-Host + Timeout-Fallback).
1. Task Pane öffnen; Bereich + **Modus** wählen: „Geocoding + Routing" (LKZ+PLZ je Start/Ziel,
   Standard) oder „Nur Routing" (lat/lon). Spalten-Mapping wählen.
2. „Strecken berechnen" → Add-in liest **blockweise** (2000 Zeilen).
3. Pro Block: `POST /route-batch` (JSON) → Backend löst ggf. LKZ/PLZ auf und routet parallel.
4. Ergebnis je Block sofort in die Nachbarspalten: ggf. `origin/dest_lat/lon`, `distance_km`,
   `duration_min`, `status` (ok / snapped_far / no_route / error / plz_not_found), `snap_m`.

## Verarbeitung — Details
- Pro Paar: kürzeste Fahrstrecke, m → km (2 NK), Dauer in Minuten.
- Concurrency: Worker-Pool (`WORKERS`, Default 8) pro /route-batch.
- **Snapping-Plausi:** Snap-Distanz prüfen; > `SNAP_LIMIT_M` → `snapped_far` (bei PLZ-Zentroiden
  erwartbar häufig, da grob).
- **Große Blätter:** Add-in schreibt streamend blockweise zurück — Teilergebnisse sofort,
  Speicher konstant.
- Robuste Fehlerbehandlung pro Zeile.
- Nur Deutschland-Extract: grenznahe Routen über Ausland werden leicht zu lang — bei Bedarf DACH+.

## Projektstruktur
```
.
├── CLAUDE.md  ·  README.md (Quelle der Wahrheit)  ·  go.mod  ·  .env.example
├── Dockerfile               # Multi-Stage Go → distroless (Backend-Image, NAS)
├── docker-compose.yml       # NAS: osrm + app, App-Image lokal gebaut
├── docker-compose.prod.yml  # NAS: App-Image aus GHCR (CI-Build) ziehen
├── data/                    # germany.osrm.*, plz_centroids.csv (gitignored)
├── profiles/truck.lua       # LKW-Profil für build-graph
├── cmd/kilometrix/          # CLI-Entry: serve | build-graph | build-geocode | token | config
├── internal/
│   ├── config/   # Settings via env + .env
│   ├── server/   # /health, /auth/check, /route-batch, Add-in-Auslieferung, CORS
│   ├── routing/  # Engine-Interface + HTTPEngine (osrm-routed), Worker-Pool
│   ├── geocode/  # LKZ/PLZ -> Zentroid (data/plz_centroids.csv)
│   ├── osrm/     # osrm-routed als Subprozess
│   ├── tokens/   # HMAC-Tokens (format-kompatibel zur alten Python-Variante)
│   ├── build/    # build-graph (orchestriert osrm-*) + build-geocode (nativ)
│   ├── tlscert/  # selbstsigniertes localhost-Zertifikat (HTTPS lokal)
│   └── runtime/  # serve-Verdrahtung + graceful shutdown
├── addin/                   # Office.js-Add-in (manifest*.xml, taskpane.html, styles.css, app.js)
└── gui/                     # Wails-App (eigenes Modul; app.go + frontend/, nicht im Docker)
```

## Commands
```bash
# Bauen + Test
go build -o kilometrix ./cmd/kilometrix      # Windows: kilometrix.exe
go test ./...

# Daten (einmalig)
./kilometrix build-graph
./kilometrix build-geocode

# Lokal starten (HTTPS, startet osrm-routed selbst)
./kilometrix serve                            # https://127.0.0.1:8443/addin/taskpane.html

# GUI (Dev)
cd gui && wails dev

# NAS (Docker)
docker compose -f docker-compose.prod.yml pull
docker compose -f docker-compose.prod.yml up -d
docker compose -f docker-compose.prod.yml exec app kilometrix token create --name X --days 90
```

## Konventionen
- Idiomatischer Go-Code: kleine fokussierte Funktionen, sprechende Namen, Fehler explizit
  zurückgeben. Routing hinter dem schmalen Interface `routing.Engine`.
- **Keine externen Go-Abhängigkeiten im Backend** (nur stdlib) — bewusst so halten.
- Keine harten Pfade; Konfiguration via env/.env (gleiche Variablennamen wie zuvor:
  `OSRM_*`, `GEOCODE_PATH`, `WORKERS`, `SNAP_LIMIT_M`, `AUTH_*`, `ADDIN_*`).
- Kommentare/Antworten auf Deutsch sind ok.

## Geklärte Punkte (Stand: erledigt)
1. ✅ Backend von FastAPI/Python **komplett nach Go portiert** (Funktionsparität); ein statisches
   Binary mit Subcommands, keine externen Deps. Python entfernt.
2. ✅ Routing über osrm-routed (HTTP); osrm-bindings unbrauchbar.
3. ✅ Excel-Schema: getrennte Spalten `origin/dest_lat/lon` **oder** `origin/dest_lkz/plz`.
4. ✅ Graph-Build Peak ~7,7 GB (16 GB ok). `--mmap` Default an.
5. ✅ Bedienung über Office.js-Add-in; Add-in zeigt außerhalb von Excel einen Hinweis.
6. ✅ Geocoding LKZ/PLZ (offline, GeoNames, DE-only, ISO-3166 alpha-2): Auflösung in
   `/route-batch` mit Dedupe, sichtbare Koordinaten, `plz_not_found`. Add-in-Umschalter,
   „Geocoding + Routing" ist Default.
7. ✅ Tokens HMAC, **format-kompatibel** zur alten Python-Variante (bestehende Tokens bleiben mit
   gleichem `AUTH_SECRET` gültig).
8. ✅ Zentraler Betrieb via Docker (osrm + app hinter Traefik); App-Image = Multi-Stage Go →
   distroless. Compose unverändert (gleiche Env-Namen, Traefik-Port 8000).
9. ✅ Wails-GUI zur Backend-Steuerung (Design folgt dem Task Pane + System-Dark-Mode); separat,
   nicht im Docker-Image.
