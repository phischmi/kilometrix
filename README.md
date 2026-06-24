# Kilometrix — OSRM Distanz-Tool

Offline-Berechnung von Straßen-Kilometern und Fahrzeiten für Origin→Destination-Paare
in Deutschland (LKW-optimiert). Keine externe Routing-API. Das Backend ist ein **einzelnes
Go-Binary** (`kilometrix`, reine stdlib, keine Abhängigkeiten); gerechnet wird über
`osrm-routed` — **lokal** als vom Backend verwalteter Subprozess, **zentral (NAS)** als
eigener Docker-Container (siehe [Zentraler Betrieb](#zentraler-betrieb-docker)).

Bedient wird es über ein **Office.js-Excel-Add-in**; für die lokale Steuerung (Server starten,
Graph/Geocoding bauen, Tokens) gibt es zusätzlich eine **Wails-Desktop-GUI** (siehe [`gui/`](gui/)).

## Komponenten

| Teil | Pfad | Zweck |
|------|------|-------|
| Backend-Binary | [`cmd/kilometrix`](cmd/kilometrix), [`internal/`](internal) | `serve`, `build-graph`, `build-geocode`, `token`, `config` |
| Excel-Add-in | [`addin/`](addin) | Task Pane (liest/schreibt das Blatt, ruft `/route-batch`) |
| Desktop-GUI | [`gui/`](gui) | Wails-App zur Backend-Steuerung (separat, nicht im Docker-Image) |

## Setup

```bash
# macOS
brew install go
brew install osrm-backend          # osrm-extract/-partition/-customize/-routed + car.lua

# Windows (Scoop, kein Admin)
scoop install go
# osrm-backend-Binaries (inkl. osrm-routed) aus den GitHub-Releases bereitstellen.
```

Backend bauen:

```bash
go build -o kilometrix ./cmd/kilometrix     # Windows: kilometrix.exe
go test ./...                                # Tests
```

`.env.example` nach `.env` kopieren und anpassen (Pfade, Worker, Auth …).

## Daten bauen (einmalig)

Beide Schritte sind Subcommands des Binaries — kein separates Skript mehr.

```bash
# OSRM-Graph (orchestriert osrm-extract/-partition/-customize, LKW-Profil profiles/truck.lua)
./kilometrix build-graph         # lädt germany.osm.pbf, baut data/germany.osrm.*

# Geocoding-Tabelle (LKZ/PLZ → Zentroid; nativ in Go, GeoNames CC BY 4.0)
./kilometrix build-geocode       # lädt GeoNames DE, schreibt data/plz_centroids.csv
```

Der Graph passt knapp in 16 GB RAM (Peak beim Customize ~7,7 GB). Die erzeugten
`data/germany.osrm.*` und `data/plz_centroids.csv` sind **portabel**: einmal bauen, dann auf
Windows/NAS kopieren. Der Graph muss mit derselben osrm-backend-Version gebaut werden wie das
`osrm-routed`, das ihn lädt.

> **`truck.lua`** ist von `car.lua` abgeleitet (4,0 m / 2,55 m / 16,5 m / 40 t, `hgv`-Zugang) und
> ein **Startprofil** — vor dem Produktiveinsatz an echten Routen verifizieren. OSRM ist
> „truck-aware", aber kein Voll-Truck-Router; die Genauigkeit hängt an den OSM-Daten.

## Lokal starten (HTTPS für das Add-in)

```bash
./kilometrix serve               # https://127.0.0.1:8443 ; startet osrm-routed selbst
```

`serve` erzeugt bei Bedarf ein selbstsigniertes localhost-Zertifikat in `certs/` (oder nutzt ein
vorhandenes von `mkcert`) und startet `osrm-routed` als Subprozess. `GET /health` zeigt
`engine_ready: true`, sobald der Graph geladen ist, und `geocode_ready: true`, sobald die
PLZ-Tabelle vorhanden ist. Optionen: `kilometrix serve -h`.

> **RAM / `--mmap`:** `osrm-routed` mappt den Graphen standardmäßig von der Platte statt ihn ganz
> ins RAM zu laden (`OSRM_ROUTED_MMAP=true`). Senkt den Leerlauf-Speicher; erste Abfrage minimal
> langsamer. Auf `false` setzen, wenn der Graph fest ins RAM soll.

### Desktop-GUI (optional, komfortabler)

Statt des Terminals kann die Wails-GUI das Backend steuern (Server start/stop, Graph/Geocoding
bauen mit Live-Log, Tokens, Status). Siehe [`gui/`](gui):

```bash
go build -o kilometrix ./cmd/kilometrix     # Backend-Binary (die GUI ruft es auf)
cd gui && wails dev                          # Dev-Fenster mit Hot-Reload
# oder: wails build  → gui/build/bin/kilometrix-gui.app (.exe unter Windows)
```

## Koordinaten aus LKZ/PLZ herleiten

Liegen statt Koordinaten nur **LKZ + PLZ** vor (z. B. `DE` / `80331`), leitet Kilometrix die
Koordinaten **offline aus PLZ-Zentroiden** her (`build-geocode`, s. o.). Im Add-in schaltet ein
Umschalter zwischen **„Geocoding + Routing"** (Spalten LKZ + PLZ für Start und Ziel, **Standard**)
und **„Nur Routing"** (Koordinatenspalten). Im Geocoding-Modus löst das Backend die Koordinaten
innerhalb desselben `/route-batch`-Aufrufs auf (ein Round-Trip, mit Dedupe), schreibt die
hergeleiteten `origin_lat/lon`, `dest_lat/lon` sichtbar ins Blatt und routet direkt. Unbekannte
PLZ erscheinen als Status `plz_not_found`.

> **Hinweis:** PLZ-Zentroide sind grob (Ortsmitte) — viele Geocoding-Strecken werden daher als
> `snapped_far` markiert. Das ist **erwartet** und kein Fehler. LKZ wird als ISO-3166 alpha-2
> erwartet (`DE`, `AT`, …); der Standard-Datensatz deckt Deutschland ab.

## Bedienung: Excel-Add-in (Office.js)

Kilometrix wird **direkt in Excel** bedient: ein Task Pane liest die Eingaben aus dem aktiven
Blatt, ruft das Backend und schreibt `distance_km, duration_min, status, snap_m` (und im
Geocoding-Modus die hergeleiteten Koordinaten) in die Nachbarspalten. Cross-Platform (Windows/Mac),
unabhängig von der VBA-Makro-Policy, vollständig offline. Außerhalb von Excel zeigt die Seite
einen Hinweis statt der funktionslosen Oberfläche.

**Sehr große Blätter:** Das Add-in arbeitet **streamend in Blöcken** (2000 Zeilen): lesen →
berechnen → zurückschreiben pro Block. Der Speicher bleibt konstant, Teilergebnisse erscheinen
sofort.

### Add-in installieren — vertrauenswürdiger Katalog (Netzwerkfreigabe)

Das Manifest wird über einen *Vertrauenswürdigen Add-in-Katalog* auf einer **Netzwerkfreigabe
(UNC-Pfad `\\server\freigabe`)** eingebunden — **nicht** über einen lokalen Ordner oder OneDrive:

1. Manifest auf die Freigabe legen — `addin/manifest.xml` (lokal, `127.0.0.1:8443`) bzw.
   `addin/manifest.server.xml` (zentral, Domain).
2. Den UNC-Pfad unter *Datei → Optionen → Trust Center → Vertrauenswürdige Add-in-Kataloge*
   eintragen („Im Menü anzeigen"), **Excel neu starten**.
3. *Einfügen → Add-Ins →* Reiter **„Freigegebener Ordner"** → **Kilometrix**.

> **Gesperrte Firmenrechner:** Ist auch der Katalog gesperrt, hilft die zentrale Bereitstellung
> im **M365-Admin-Center** (`manifest.server.xml`). Ändert man Host/Port/Domain, müssen die URLs
> im jeweiligen Manifest angepasst werden.

## Zentraler Betrieb (Docker)

Das Backend kann zentral hinter Traefik (Let's Encrypt) laufen. `docker compose` startet zwei
Container: `osrm` (offizielles Image, lädt den Graphen) + `app` (das Go-Binary, distroless-Image).
Auf dem NAS liegen `data/germany.osrm.*` und `data/plz_centroids.csv` im gemounteten `./data`.

**Variante A — Image aus GHCR (empfohlen).** GitHub Actions baut bei Push auf `main` das Image
([.github/workflows/docker.yml](.github/workflows/docker.yml)) und pusht nach
`ghcr.io/phischmi/kilometrix`. Auf dem NAS reichen `docker-compose.prod.yml`, `.env`, der Graph
und die Geocoding-CSV — **kein Quellcode**:

```bash
echo "AUTH_SECRET=$(openssl rand -hex 32)" > .env   # data/germany.osrm.* + data/plz_centroids.csv daneben
docker login ghcr.io -u phischmi                      # einmalig (PAT mit read:packages)
docker compose -f docker-compose.prod.yml pull
docker compose -f docker-compose.prod.yml up -d
docker compose -f docker-compose.prod.yml exec app kilometrix token create --name philipp --days 90
```

**Variante B — lokal auf dem NAS bauen** (`docker-compose.yml`, braucht den Quellcode dort):

```bash
echo "AUTH_SECRET=$(openssl rand -hex 32)" > .env
docker compose up -d --build
docker compose exec app kilometrix token create --name philipp --days 90
```

**Datei-Rechte im `./data`:** Der `app`-Container läuft non-root (distroless `:nonroot`,
UID 65532) und mountet `./data` read-only. Die Dateien müssen daher für „others" lesbar sein,
sonst meldet die App beim Start `Geocoder nicht geladen: ... permission denied`. `build-geocode`
schreibt `plz_centroids.csv` bereits world-readable (`0644`); kopierst du Daten manuell aufs NAS
oder läuft die umask restriktiv (`077`), einmalig nachziehen:

```bash
chmod o+rx data && chmod o+r data/plz_centroids.csv data/germany.osrm.*
```

**Zeitzone:** Die App loggt in `Europe/Berlin` (`TZ` in der Compose, Zoneinfo ist ins Binary
eingebettet). Anpassen über die `TZ`-Variable im `app`-Service.

**Token-Schutz:** `/route-batch` ist mit einem signierten Bearer-Token (HMAC, frei wählbare TTL,
ohne DB) geschützt. Einzelne Tokens lassen sich nicht gezielt widerrufen — kurze TTL vergeben oder
mit `AUTH_SECRET` **alle** rotieren (Secret neu setzen, App neu starten).

> **Datenschutz:** Für den echten Firmen-Rollout sollte der Server **von der Firma gehostet** sein
> (es laufen Firmen-Koordinaten darüber). Das Homelab ist ideal fürs PoC.

## API (für eigene Skripte)

```
POST /route-batch
{
  "pairs": [
    {"id": "A1", "origin_lat": 52.52, "origin_lon": 13.405,
     "dest_lat": 48.1372, "dest_lon": 11.5755},
    {"id": "A2", "origin_lkz": "DE", "origin_plz": "10115",
     "dest_lkz": "DE", "dest_plz": "80331"}
  ]
}
->
{
  "results": [
    {"id": "A1", "distance_km": 585.7, "duration_min": 356.47,
     "status": "snapped_far", "snap_m": 69.2, "message": null,
     "origin_lat": 52.52, "origin_lon": 13.405, "dest_lat": 48.1372, "dest_lon": 11.5755},
    {"id": "A2", "distance_km": 585.9, "duration_min": 357.1,
     "status": "snapped_far", "snap_m": 412.0, "message": null,
     "origin_lat": 52.5323, "origin_lon": 13.3846, "dest_lat": 48.1345, "dest_lon": 11.571}
  ]
}
```

Jeder Endpunkt wird **entweder** über `*_lat/*_lon` **oder** über `*_lkz/*_plz` angegeben;
LKZ/PLZ löst das Backend zum Zentroid auf (benötigt `data/plz_centroids.csv`, sonst `503`).
Synchron und parallel (`WORKERS`, Default 8). Reihenfolge bleibt erhalten, `id` wird
durchgereicht. Obergrenze `MAX_SYNC_BATCH` (Default 20.000) pro Request — das Add-in chunkt
automatisch darunter. Bei `AUTH_ENABLED=true` muss `Authorization: Bearer <token>` mitgesendet
werden (`GET /auth/check` validiert ein Token); lokal ist Auth aus.

## Tests

```bash
go test ./...
```
