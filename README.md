# Kilometrix — OSRM Distanz-Tool

Offline-Berechnung von Straßen-Kilometern und Fahrzeiten für Origin→Destination-Paare
in Deutschland (LKW-optimiert). Keine externe Routing-API. Das Backend ist ein **einzelnes
Go-Binary** (`kilometrix`, reine stdlib, keine Abhängigkeiten); gerechnet wird über
`osrm-routed`.

Der **Produktivbetrieb läuft zentral auf einem Server** (Docker hinter Traefik) — siehe
[Zentraler Betrieb](#zentraler-betrieb-docker). Lokal lässt sich das Backend **zu Testzwecken**
direkt als Binary starten, das `osrm-routed` dann selbst verwaltet. Bedient wird Kilometrix über
ein **Office.js-Excel-Add-in**.

## Komponenten

| Teil | Pfad | Zweck |
|------|------|-------|
| Backend-Binary | [`cmd/kilometrix`](cmd/kilometrix), [`internal/`](internal) | `serve`, `build-graph`, `build-geocode`, `token`, `config` |
| Excel-Add-in | [`addin/`](addin) | Task Pane (liest/schreibt das Blatt, ruft `/route-batch`) |

## Daten bauen (einmalig)

OSRM braucht einen Routing-Graphen, das Geocoding eine PLZ-Tabelle. Beides sind Subcommands
des Binaries. Voraussetzung: `osrm-backend`-CLI verfügbar (z. B. `brew install osrm-backend`).

```bash
go build -o kilometrix ./cmd/kilometrix     # Windows: kilometrix.exe

# OSRM-Graph (orchestriert osrm-extract/-partition/-customize, LKW-Profil profiles/truck.lua)
./kilometrix build-graph         # lädt germany.osm.pbf, baut data/germany.osrm.*

# Geocoding-Tabelle (LKZ/PLZ → Zentroid; nativ in Go, GeoNames CC BY 4.0)
./kilometrix build-geocode       # schreibt data/plz_centroids.csv
```

Der Graph-Build ist speicherhungrig (Peak ~7,7 GB, passt in 16 GB RAM). Die erzeugten
`data/germany.osrm.*` und `data/plz_centroids.csv` sind **portabel** — einmal bauen, dann auf
den Server kopieren. Der Graph muss mit derselben osrm-backend-Version gebaut werden wie das
`osrm-routed`, das ihn lädt.

> **`truck.lua`** ist von `car.lua` abgeleitet (4,0 m / 2,55 m / 16,5 m / 40 t, `hgv`-Zugang) und
> ein **Startprofil** — vor dem Produktiveinsatz an echten Routen verifizieren.

## Zentraler Betrieb (Docker)

Das Backend läuft zentral hinter Traefik (Let's Encrypt). `docker compose` startet zwei
Container: `osrm` (offizielles Image, lädt den Graphen) + `app` (das Go-Binary, distroless-Image).
`data/germany.osrm.*` und `data/plz_centroids.csv` liegen im gemounteten `./data`.

GitHub Actions baut bei Push auf `main` das App-Image
([.github/workflows/docker.yml](.github/workflows/docker.yml)) und pusht es nach
`ghcr.io/phischmi/kilometrix`. Auf dem Server reichen `docker-compose.prod.yml`, `.env`, der
Graph und die Geocoding-CSV — **kein Quellcode**:

```bash
# .env: AUTH_SECRET + deine Domain (für die Traefik-Host-Regel)
printf 'AUTH_SECRET=%s\nKILOMETRIX_HOST=%s\n' "$(openssl rand -hex 32)" "deine-domain.de" > .env
# data/germany.osrm.* + data/plz_centroids.csv müssen daneben liegen
docker login ghcr.io -u phischmi                      # einmalig (PAT mit read:packages)
docker compose -f docker-compose.prod.yml pull
docker compose -f docker-compose.prod.yml up -d
docker compose -f docker-compose.prod.yml exec app kilometrix token create --name philipp --days 90
```

Alternativ baut `docker-compose.yml` das App-Image lokal aus dem Quellcode (`up -d --build`).

**Datei-Rechte im `./data`:** Der `app`-Container läuft non-root (distroless `:nonroot`,
UID 65532) und mountet `./data` read-only — die Dateien müssen für „others" lesbar sein. Bei
restriktiver umask einmalig nachziehen:

```bash
chmod o+rx data && chmod o+r data/plz_centroids.csv data/germany.osrm.*
```

**Token-Schutz:** `/route-batch` ist mit einem signierten Bearer-Token (HMAC, frei wählbare TTL,
ohne DB) geschützt. Einzelne Tokens lassen sich nicht gezielt widerrufen — kurze TTL vergeben oder
mit `AUTH_SECRET` **alle** rotieren (Secret neu setzen, App neu starten).

## Lokal starten (zu Testzwecken)

```bash
./kilometrix serve               # https://127.0.0.1:8443 ; startet osrm-routed selbst
```

`serve` erzeugt bei Bedarf ein selbstsigniertes localhost-Zertifikat in `certs/` (oder nutzt ein
vorhandenes von `mkcert`) und startet `osrm-routed` als Subprozess. `GET /health` zeigt
`engine_ready: true`, sobald der Graph geladen ist, und `geocode_ready: true`, sobald die
PLZ-Tabelle vorhanden ist. Auth ist lokal aus. Optionen: `kilometrix serve -h`.

## Bedienung: Excel-Add-in (Office.js)

Kilometrix wird **direkt in Excel** bedient: ein Task Pane liest die Eingaben aus dem aktiven
Blatt, ruft das Backend und schreibt `distance_km, duration_min, status, snap_m` (und im
Geocoding-Modus die hergeleiteten Koordinaten) in die Nachbarspalten. Cross-Platform (Windows/Mac),
vollständig offline. Das Add-in arbeitet **streamend in Blöcken** (2000 Zeilen): lesen →
berechnen → zurückschreiben pro Block, damit der Speicher auch bei großen Blättern konstant bleibt.

Liegen statt Koordinaten nur **LKZ + PLZ** vor (z. B. `DE` / `80331`), leitet Kilometrix die
Koordinaten **offline aus PLZ-Zentroiden** her. Im Add-in schaltet ein Umschalter zwischen
**„Geocoding + Routing"** (LKZ + PLZ, **Standard**) und **„Nur Routing"** (Koordinatenspalten).
Unbekannte PLZ erscheinen als Status `plz_not_found`.

> **Hinweis:** PLZ-Zentroide sind grob (Ortsmitte) — viele Geocoding-Strecken werden daher als
> `snapped_far` markiert. Das ist **erwartet** und kein Fehler. LKZ wird als ISO-3166 alpha-2
> erwartet (`DE`, `AT`, …); der Standard-Datensatz deckt Deutschland ab.

### Add-in installieren

Das Manifest wird über einen *Vertrauenswürdigen Add-in-Katalog* auf einer **Netzwerkfreigabe
(UNC-Pfad `\\server\freigabe`)** eingebunden — **nicht** über einen lokalen Ordner oder OneDrive:

1. Manifest auf die Freigabe legen — `addin/manifest.xml` (lokal, `127.0.0.1:8443`) bzw. das
   zentrale `addin/manifest.server.xml`. Letzteres ist **nicht** im Repo (enthält die Domain) —
   einmalig aus dem Template erzeugen:
   ```bash
   sed "s/__DOMAIN__/deine-domain.de/g" addin/manifest.server.template.xml > addin/manifest.server.xml
   ```
2. Den UNC-Pfad unter *Datei → Optionen → Trust Center → Vertrauenswürdige Add-in-Kataloge*
   eintragen, **Excel neu starten**.
3. *Einfügen → Add-Ins →* Reiter **„Freigegebener Ordner"** → **Kilometrix**.

> **Gesperrte Firmenrechner:** Ist auch der Katalog gesperrt, hilft die zentrale Bereitstellung
> im **M365-Admin-Center** (`manifest.server.xml`).

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
werden (`GET /auth/check` validiert ein Token).

## Tests

```bash
go test ./...
```

## Lizenz

MIT — siehe [LICENSE](LICENSE).
