# Einmaliges OSRM-Preprocessing (MLD-Pipeline) für Windows — SEPARAT vom Tool, manuell.
# Ergebnis: data\germany.osrm.* (portable Daten, danach kopierbar).
#
# Voraussetzung: OSRM-CLI-Binaries (osrm-extract/-partition/-customize) aus den
# GitHub-Releases von Project-OSRM/osrm-backend + Zusatz-Runtime (oneTBB, BZip2).
# NICHT das Python-Wheel (read-only). Kein Docker.
#
# RAM-Hinweis: Car-Profil Deutschland passt knapp in 16 GB. Andere Programme schließen.
$ErrorActionPreference = "Stop"

$Root = Split-Path -Parent $PSScriptRoot
$DataDir = Join-Path $Root "data"
$PbfUrl = if ($env:PBF_URL) { $env:PBF_URL } else { "https://download.geofabrik.de/europe/germany-latest.osm.pbf" }
$Pbf = Join-Path $DataDir "germany-latest.osm.pbf"
$Base = Join-Path $DataDir "germany.osrm"
# Eigenes Profil (z. B. LKW) via $env:PROFILE_FILE — muss neben dem mitgelieferten
# car.lua / dem lib-Ordner liegen, damit `require "lib/..."` aufgeht.
$Profile = if ($env:PROFILE_FILE) { $env:PROFILE_FILE } elseif ($env:OSRM_PROFILE) { $env:OSRM_PROFILE } else { "car.lua" }

New-Item -ItemType Directory -Force -Path $DataDir | Out-Null

foreach ($bin in @("osrm-extract", "osrm-partition", "osrm-customize")) {
    if (-not (Get-Command $bin -ErrorAction SilentlyContinue)) {
        Write-Error "'$bin' nicht im PATH. OSRM-CLI-Binaries aus den GitHub-Releases installieren."
    }
}

if (-not (Test-Path $Pbf)) {
    Write-Host "Lade OSM-Daten: $PbfUrl"
    Invoke-WebRequest -Uri $PbfUrl -OutFile $Pbf
}

Write-Host "==> osrm-extract (Profil: $Profile)"
osrm-extract -p $Profile $Pbf

Write-Host "==> osrm-partition"
osrm-partition $Base

Write-Host "==> osrm-customize"
osrm-customize $Base

Write-Host "Fertig. Graph liegt in $Base.* — OSRM_ALGORITHM=MLD in .env setzen."
