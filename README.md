# Kilometrix — OSRM Distanz-Tool

Offline-Berechnung von Straßen-Kilometern und Fahrzeiten für Origin→Destination-Paare
in Deutschland. Keine externe Routing-API, kein Docker. Routing läuft lokal über
`osrm-routed` (HTTP), das das Backend selbst als Subprozess startet und stoppt.

> **Engine-Hinweis:** Standard ist `ENGINE=http` (osrm-routed). Die in-process-Variante
> `ENGINE=bindings` ist vorbereitet, aber das PyPI-Wheel `osrm-bindings` ist archiviert
> und liegt im Datenformat hinter osrm-backend Stable — es lädt einen mit aktuellem
> `osrm-backend` gebauten Graphen nicht (Fingerprint-Mismatch). Für in-process wäre ein
> versionsgleicher Source-Build nötig (`pip install --no-binary osrm-bindings osrm-bindings`).

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

Der Graph wird **getrennt vom Tool** mit den osrm-backend-CLI-Tools gebaut:

```bash
./scripts/build_graph.sh        # lädt germany.osm.pbf, erzeugt data/germany.osrm.*
```

Die erzeugten `data/germany.osrm.*` sind portabel: einmal bauen, dann auf
Windows/NAS kopieren. **Wichtig:** Der Graph muss mit derselben osrm-backend-Version
gebaut werden wie das `osrm-routed`, das ihn lädt. `OSRM_GRAPH_PATH` und
`OSRM_ALGORITHM` (MLD) in `.env` setzen.

## Bedienung: Excel-Add-in (Office.js)

Kilometrix wird **direkt in Excel** bedient: ein Task Pane („Strecken berechnen") liest die
Koordinaten aus dem aktiven Blatt, ruft das lokale Backend und schreibt `distance_km,
duration_min, status, snap_m` in die Nachbarspalten zurück.
Cross-Platform (Windows/Mac), unabhängig von der VBA-Makro-Policy, vollständig offline.

Architektur: FastAPI liefert das Add-in **selbst über HTTPS** aus (same-origin) und stellt
gleichzeitig `/route-batch` bereit und startet `osrm-routed` — ein einziger lokaler Prozess.

**Sehr große Blätter:** Das Add-in arbeitet **streamend in Blöcken** (2000 Zeilen): lesen →
berechnen → zurückschreiben pro Block. Der Speicher bleibt konstant, Teilergebnisse erscheinen
sofort, der Fortschrittsbalken läuft mit — so sind auch Blätter mit hunderttausenden Zeilen
ohne Office.js-Payload-Limits machbar.

**Starten:**

```bash
# macOS/Linux
./scripts/serve_addin.sh        # erzeugt localhost-Zertifikat, HTTPS auf :8443, startet osrm-routed

# Windows (PowerShell)
.\scripts\serve_addin.ps1
```

`GET /health` zeigt `engine_ready: true`, sobald `osrm-routed` den Graphen geladen hat.
Port 5000 ist auf macOS vom AirPlay-Receiver belegt — daher Default `OSRM_ROUTED_PORT=5001`.
Add-in liegt dann unter `https://127.0.0.1:8443/addin/taskpane.html`.

**Zertifikat vertrauen (einmalig):** Office lädt das Pane nur über vertrauenswürdiges HTTPS
(macOS: Keychain, Windows: Zertifikatspeicher / WebView2). Am einfachsten mit `mkcert` — richtet
eine **per-User-CA ein, ohne Admin**:

```bash
brew install mkcert      # macOS
scoop install mkcert     # Windows
```

Danach das Serve-Skript erneut starten. Ohne mkcert wird ein selbstsigniertes Zertifikat
(`certs/localhost.pem`) erzeugt; die `.ps1` importiert es unter Windows automatisch in
`Cert:\CurrentUser\Root` (kein Admin), auf macOS muss es einmalig manuell vertraut werden.

**Sideloading (Manifest `addin/manifest.xml`):**

- **macOS:** den `wef`-Ordner anlegen (existiert standardmäßig nicht) und das Manifest
  hineinkopieren, dann **Excel komplett beenden (⌘Q) und neu öffnen**:
  ```bash
  mkdir -p ~/Library/Containers/com.microsoft.Excel/Data/Documents/wef
  cp addin/manifest.xml ~/Library/Containers/com.microsoft.Excel/Data/Documents/wef/
  ```
- **Windows (per-User, kein Admin):** Ordner mit `manifest.xml` als *Vertrauenswürdigen
  Add-in-Katalog* unter *Datei → Optionen → Trust Center → Vertrauenswürdige Add-in-Kataloge*
  registrieren (Häkchen „Im Menü anzeigen"), Excel neu starten →
  *Einfügen → Add-Ins → Freigegebener Ordner → Kilometrix*.

Das Add-in fügt eine eigene Gruppe **„Kilometrix"** mit dem Button **„Strecken berechnen"**
auf dem **Start-Reiter** ein — dort ist der Einstieg (nicht unter „Einfügen → Meine Add-ins").

Für einen breiten Rollout gäbe es zusätzlich die zentrale Verteilung übers M365-Admin-Center
(braucht dann IT). Ändert man Host/Port, müssen die URLs in `addin/manifest.xml` angepasst werden.

## Zentraler Betrieb (Docker, PoC)

Statt auf jedem Laptop lokal kann das Backend zentral laufen (z. B. NAS hinter Traefik mit
Let's Encrypt). Vorteile: **echtes TLS** (kein mkcert pro Gerät), **zentrale Add-in-Verteilung**
übers M365-Admin-Center, ein gepflegter Graph. `docker-compose.yml` startet zwei Container:
`kilometrix-osrm` (offizielles Image, lädt den Graphen) + `kilometrix-app` (FastAPI, hinter Traefik).

**Variante A — Image aus GHCR ziehen (empfohlen, kein Build auf dem NAS).**
GitHub Actions ([.github/workflows/docker.yml](.github/workflows/docker.yml)) baut bei jedem Push
auf `main` das Image und pusht es nach `ghcr.io/phischmi/kilometrix`. Auf dem NAS reichen dann
`docker-compose.prod.yml`, `.env` und der Graph — **kein Quellcode, kein git**:

```bash
echo "AUTH_SECRET=$(openssl rand -hex 32)" > .env      # data/germany.osrm.* muss daneben liegen
docker login ghcr.io -u phischmi                        # einmalig (PAT mit read:packages)
docker compose -f docker-compose.prod.yml pull
docker compose -f docker-compose.prod.yml up -d
docker compose -f docker-compose.prod.yml exec kilometrix-app python -m backend.tokens create --name philipp --days 90
```

**Variante B — lokal auf dem NAS bauen** (`docker-compose.yml`, braucht den Quellcode dort):

```bash
echo "AUTH_SECRET=$(openssl rand -hex 32)" > .env
docker compose up -d --build
docker compose exec kilometrix-app python -m backend.tokens create --name philipp --days 90
```

In beiden Compose-Dateien ggf. **Netzwerk-Name** (`traefik`) und **certresolver** (`letsencrypt`)
an deine Traefik-Instanz anpassen; die Domain (`kilometrix.philipp-schmidt.de`) steht in den
Router-Labels und im `addin/manifest.server.xml`. (Ist das GHCR-Paket privat, braucht das NAS
einmalig `docker login ghcr.io`.)

**Token-Schutz:** `/route-batch` ist mit einem signierten Bearer-Token (HMAC, definierbare TTL,
ohne DB) geschützt. Beim ersten Öffnen fragt das Add-in das Token ab (Gate), speichert es lokal
und sendet es fortan automatisch. Widerruf = `AUTH_SECRET` rotieren.

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
