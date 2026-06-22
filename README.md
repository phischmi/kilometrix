# Kilometrix — OSRM Distanz-Tool

Offline-Berechnung von Straßen-Kilometern und Fahrzeiten für Origin→Destination-Paare
in Deutschland (LKW-optimiert). Keine externe Routing-API. Routing läuft über `osrm-routed`
— **lokal** als vom Backend verwalteter Subprozess, **zentral (NAS)** als eigener
Docker-Container (siehe [Zentraler Betrieb](#zentraler-betrieb-docker-poc)).

## Setup

```bash
# Mac
brew install osrm-backend                 # liefert osrm-extract/-partition/-customize/-routed + car.lua
python -m venv venv && source venv/bin/activate
pip install -e ".[dev]"

# Windows (Scoop)
scoop install python
python -m venv venv && venv\Scripts\activate
pip install -e ".[dev]"
# osrm-backend-Binaries (inkl. osrm-routed) aus den GitHub-Releases / per Paketmanager bereitstellen.
```

`.env.example` nach `.env` kopieren und anpassen.

## OSRM-Graph (separat, einmalig)

Der Graph wird **getrennt vom Tool** mit den osrm-backend-CLI-Tools gebaut — standardmäßig
mit dem **LKW-Profil** [`profiles/truck.lua`](profiles/truck.lua):

```bash
./scripts/build_graph.sh        # lädt germany.osm.pbf, baut data/germany.osrm.* (LKW-Profil)
```

`truck.lua` ist von `car.lua` abgeleitet (Maße 4,0 m / 2,55 m / 16,5 m / 40 t, `hgv`-Zugang,
LKW-Geschwindigkeiten, Tempo-Limit 89). Das Skript kopiert das Profil neben das von
osrm-backend mitgelieferte `car.lua`, damit dessen `lib/` gefunden wird (`car.lua` dient nur
noch als lib-Quelle). Anderes Profil: `OSRM_PROFILE=<pfad> ./scripts/build_graph.sh`.

Die erzeugten `data/germany.osrm.*` sind portabel: einmal bauen, dann auf Windows/NAS kopieren.
**Wichtig:** Der Graph muss mit derselben osrm-backend-Version gebaut werden wie das
`osrm-routed`, das ihn lädt. `OSRM_GRAPH_PATH` / `OSRM_ALGORITHM` (MLD) in `.env` setzen.

**RAM / `--mmap`:** `osrm-routed` mappt den Graphen standardmäßig per Memory-Mapping von der
Platte, statt ihn komplett ins RAM zu laden (`OSRM_ROUTED_MMAP=true`; im Compose als `--mmap`
im osrm-Command gesetzt). Das senkt den Leerlauf-Speicher deutlich — wichtig auf der
RAM-knappen NAS —, die erste Abfrage ist dafür minimal langsamer (auf SSD vernachlässigbar).
Auf `false` setzen, wenn der Graph fest ins RAM geladen werden soll.

> **Hinweis:** `truck.lua` ist ein **Startprofil** — Syntax geprüft, aber vor dem
> Produktiveinsatz an echten Routen verifizieren und Maße/Gewicht ggf. an die Flotte
> anpassen. OSRM ist „truck-aware" (HGV-Sperren, Maß-/Gewichtsbeschränkungen aus OSM),
> aber kein Voll-Truck-Router — die Genauigkeit hängt an den OSM-Daten.

## Bedienung: Excel-Add-in (Office.js)

Kilometrix wird **direkt in Excel** bedient: ein Task Pane („Strecken berechnen") liest die
Koordinaten aus dem aktiven Blatt, ruft das Backend und schreibt `distance_km, duration_min,
status, snap_m` in die Nachbarspalten zurück.
Cross-Platform (Windows/Mac), unabhängig von der VBA-Makro-Policy, vollständig offline.
Die Add-in-Seite funktioniert nur in Excel: außerhalb (z. B. direkt im Browser aufgerufen)
zeigt sie einen Hinweis statt der funktionslosen Oberfläche.

Architektur: FastAPI liefert das Add-in **selbst über HTTPS** aus (same-origin) und stellt
`/route-batch` bereit; gerechnet wird über `osrm-routed`. Das läuft **lokal** als ein Prozess
oder **zentral** als zwei Container hinter Traefik (siehe unten).

**Sehr große Blätter:** Das Add-in arbeitet **streamend in Blöcken** (2000 Zeilen): lesen →
berechnen → zurückschreiben pro Block. Der Speicher bleibt konstant, Teilergebnisse erscheinen
sofort, der Fortschrittsbalken läuft mit — so sind auch Blätter mit hunderttausenden Zeilen
ohne Office.js-Payload-Limits machbar.

### Backend starten — lokal **oder** per Docker

- **Lokal** (einzelner Rechner; Add-in unter `https://127.0.0.1:8443`):
  ```bash
  ./scripts/serve_addin.sh        # macOS/Linux
  .\scripts\serve_addin.ps1       # Windows (PowerShell)
  ```
  Erzeugt ein localhost-Zertifikat, startet HTTPS auf :8443 und `osrm-routed`. Zertifikat
  vertrauen: am einfachsten mit `mkcert` (`brew install mkcert` / `scoop install mkcert`,
  per-User, kein Admin); ohne mkcert wird ein selbstsigniertes erzeugt (die `.ps1` importiert es
  unter Windows automatisch in `Cert:\CurrentUser\Root`).
- **Zentral per Docker** (NAS/Server hinter Traefik; Add-in unter der Domain): siehe
  [Zentraler Betrieb (Docker)](#zentraler-betrieb-docker-poc). Dort übernimmt Traefik +
  Let's Encrypt das echte TLS — kein mkcert pro Gerät.

`GET /health` zeigt `engine_ready: true`, sobald der Graph geladen ist.

### Add-in in Excel installieren — vertrauenswürdiger Katalog (Netzwerkfreigabe)

Das Manifest wird über einen *Vertrauenswürdigen Add-in-Katalog* auf einer **Netzwerkfreigabe
(UNC-Pfad `\\server\freigabe`)** eingebunden — **nicht** über einen lokalen Ordner oder OneDrive
(das wird als Katalog nicht erkannt):

1. Passendes Manifest auf die Freigabe legen — `addin/manifest.xml` (lokal, `127.0.0.1:8443`)
   bzw. `addin/manifest.server.xml` (zentral, Domain).
2. Den UNC-Pfad unter *Datei → Optionen → Trust Center → Vertrauenswürdige Add-in-Kataloge*
   eintragen (Häkchen „Im Menü anzeigen"), **Excel neu starten**.
3. *Einfügen → Add-Ins →* Reiter **„Freigegebener Ordner"** (nicht „Store") → **Kilometrix**.

Das Add-in fügt eine Gruppe **„Kilometrix"** mit dem Button **„Strecken berechnen"** auf dem
**Start-Reiter** ein — dort ist der Einstieg.

> **Gesperrte Firmenrechner:** Die Meldung „Der Add-in-Store wurde deaktiviert" ist normal — der
> Reiter **„Freigegebener Ordner"** (Schritt 3) funktioniert trotzdem über den Katalog. Nur wenn
> die Richtlinie **auch Kataloge** sperrt, hilft die zentrale Bereitstellung im **M365-Admin-Center**
> (IT): *Einstellungen → Integrierte Apps → benutzerdefinierte App hochladen → `manifest.server.xml`*.

Ändert man Host/Port/Domain, müssen die URLs im jeweiligen Manifest angepasst werden.

## Zentraler Betrieb (Docker, PoC)

Statt auf jedem Laptop lokal kann das Backend zentral laufen (z. B. NAS hinter Traefik mit
Let's Encrypt). Vorteile: **echtes TLS** (kein mkcert pro Gerät), **zentrale Add-in-Verteilung**
übers M365-Admin-Center, ein gepflegter Graph. `docker-compose.yml` startet zwei Container:
`osrm` (offizielles Image, lädt den Graphen) + `app` (FastAPI, hinter Traefik).

**Variante A — Image aus GHCR ziehen (empfohlen, kein Build auf dem NAS).**
GitHub Actions ([.github/workflows/docker.yml](.github/workflows/docker.yml)) baut bei jedem Push
auf `main` das Image und pusht es nach `ghcr.io/phischmi/kilometrix`. Auf dem NAS reichen dann
`docker-compose.prod.yml`, `.env` und der Graph — **kein Quellcode, kein git**:

```bash
echo "AUTH_SECRET=$(openssl rand -hex 32)" > .env      # data/germany.osrm.* muss daneben liegen
docker login ghcr.io -u phischmi                        # einmalig (PAT mit read:packages)
docker compose -f docker-compose.prod.yml pull
docker compose -f docker-compose.prod.yml up -d
docker compose -f docker-compose.prod.yml exec app python -m backend.tokens create --name philipp --days 90
```

**Variante B — lokal auf dem NAS bauen** (`docker-compose.yml`, braucht den Quellcode dort):

```bash
echo "AUTH_SECRET=$(openssl rand -hex 32)" > .env
docker compose up -d --build
docker compose exec app python -m backend.tokens create --name philipp --days 90
```

In beiden Compose-Dateien ggf. **Netzwerk-Name** (`traefik`) und **certresolver** (`letsencrypt`)
an deine Traefik-Instanz anpassen; die Domain (`kilometrix.philipp-schmidt.de`) steht in den
Router-Labels und im `addin/manifest.server.xml`. (Ist das GHCR-Paket privat, braucht das NAS
einmalig `docker login ghcr.io`.)

**Token-Schutz:** `/route-batch` ist mit einem signierten Bearer-Token (HMAC, definierbare TTL,
ohne DB) geschützt. Beim ersten Öffnen fragt das Add-in das Token ab (Gate), speichert es lokal
und sendet es fortan automatisch.

```bash
# Token erzeugen (TTL frei wählbar) und an den Nutzer geben
docker compose -f docker-compose.prod.yml exec app python -m backend.tokens create --name philipp --days 90

# ALLE Tokens widerrufen: AUTH_SECRET rotieren und App neu starten
sed -i "s/^AUTH_SECRET=.*/AUTH_SECRET=$(openssl rand -hex 32)/" .env
docker compose -f docker-compose.prod.yml up -d
```

Da die Tokens zustandslos sind, lässt sich ein **einzelnes** Token nicht gezielt widerrufen —
entweder kurze TTL vergeben (läuft von selbst ab) oder mit `AUTH_SECRET` **alle** rotieren.
Nach dem Rotieren bekommen alle Nutzer `401` → im Add-in erscheint wieder das Token-Gate.

**Add-in verteilen:** `addin/manifest.server.xml` (zeigt auf die Domain) per M365-Admin-Center
zentral ausrollen — dann erscheint Kilometrix bei den zugewiesenen Nutzern ohne Sideloading.

> **Datenschutz-Hinweis:** Für den echten Firmen-Rollout sollte der Server **von der Firma
> gehostet** sein (On-Prem/Firmen-Cloud), nicht im privaten Homelab — es laufen Firmen-Koordinaten
> darüber. Das Homelab ist ideal fürs PoC.

## API (für eigene Skripte)

Das Add-in spricht mit dem Backend über einen einzigen JSON-Endpoint, den du auch direkt
aus eigenen Skripten nutzen kannst:

```
POST /route-batch
{
  "pairs": [
    {"id": "A1", "origin_lat": 52.52, "origin_lon": 13.405,
     "dest_lat": 48.1372, "dest_lon": 11.5755}
  ]
}
->
{
  "results": [
    {"id": "A1", "distance_km": 585.7, "duration_min": 356.47,
     "status": "snapped_far", "snap_m": 69.2, "message": null}
  ]
}
```

Synchron und parallel (8 Worker). Reihenfolge bleibt erhalten, `id` wird durchgereicht.
Obergrenze: `MAX_SYNC_BATCH` (Default 20.000) pro Request — das Add-in chunkt automatisch
darunter und kann so beliebig große Blätter verarbeiten.

Bei `AUTH_ENABLED=true` (zentraler Betrieb) muss der Header `Authorization: Bearer <token>`
mitgesendet werden (`GET /auth/check` validiert ein Token). Lokal ist Auth aus.

## Tests

```bash
pytest
```
