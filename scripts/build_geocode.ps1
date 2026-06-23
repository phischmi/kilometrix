# Einmaliges Geocoding-Setup (Windows): GeoNames-Postal-Daten -> data/plz_centroids.csv.
# Zentroid je PLZ. Danach offline nutzbar. Standard: Deutschland ($env:GEONAMES_COUNTRY=DE).
# Datenquelle: GeoNames (CC BY 4.0). Python muss im PATH sein (wie fuers Backend).
$ErrorActionPreference = "Stop"

$Root = Split-Path -Parent $PSScriptRoot
$DataDir = Join-Path $Root "data"
$Country = if ($env:GEONAMES_COUNTRY) { $env:GEONAMES_COUNTRY } else { "DE" }
$ZipUrl  = if ($env:ZIP_URL) { $env:ZIP_URL } else { "https://download.geonames.org/export/zip/$Country.zip" }
$Out = Join-Path $DataDir "plz_centroids.csv"
$Tmp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString())

New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
New-Item -ItemType Directory -Force -Path $Tmp | Out-Null
try {
    Write-Host "Lade GeoNames Postal-Daten: $ZipUrl"
    $Zip = Join-Path $Tmp "geonames.zip"
    Invoke-WebRequest -Uri $ZipUrl -OutFile $Zip
    Expand-Archive -Path $Zip -DestinationPath $Tmp -Force

    Write-Host "==> Zentroide aggregieren"
    python (Join-Path $PSScriptRoot "_geonames_centroids.py") (Join-Path $Tmp "$Country.txt") $Out
}
finally {
    Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue
}

Write-Host "Fertig. Geocoding-Tabelle liegt in $Out — portabel nach NAS kopierbar."
