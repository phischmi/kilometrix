# Startet das Backend über HTTPS (für das Office.js-Add-in) auf Port 8443 — Windows/PowerShell.
# Liefert Add-in-Oberfläche + /route-batch + startet osrm-routed — alles same-origin, offline.
# Erzeugt bei Bedarf ein localhost-Zertifikat (mkcert bevorzugt = vertrauenswürdig ohne Admin,
# sonst openssl-selbstsigniert + Import in den Benutzer-Zertifikatspeicher).
$ErrorActionPreference = "Stop"

$Root    = Split-Path -Parent $PSScriptRoot
$CertDir = Join-Path $Root "certs"
$Cert    = Join-Path $CertDir "localhost.pem"
$Key     = Join-Path $CertDir "localhost-key.pem"
$VHost   = if ($env:ADDIN_HOST) { $env:ADDIN_HOST } else { "127.0.0.1" }
$Port    = if ($env:ADDIN_PORT) { $env:ADDIN_PORT } else { "8443" }

Set-Location $Root
New-Item -ItemType Directory -Force -Path $CertDir | Out-Null

# uvicorn aus dem venv bevorzugen
$Uvicorn = Join-Path $Root "venv\Scripts\uvicorn.exe"
if (-not (Test-Path $Uvicorn)) {
    if (Get-Command uvicorn -ErrorAction SilentlyContinue) { $Uvicorn = "uvicorn" }
    else { Write-Error "uvicorn nicht gefunden. Erst: pip install -e `".[dev]`"" }
}

if (-not (Test-Path $Cert) -or -not (Test-Path $Key)) {
    if (Get-Command mkcert -ErrorAction SilentlyContinue) {
        Write-Host "==> Zertifikat via mkcert (vertrauenswürdig, kein Admin)…"
        mkcert -install | Out-Null
        mkcert -cert-file $Cert -key-file $Key 127.0.0.1 localhost ::1 | Out-Null
    }
    elseif (Get-Command openssl -ErrorAction SilentlyContinue) {
        Write-Host "==> mkcert nicht gefunden — erzeuge selbstsigniertes Zertifikat via openssl."
        openssl req -x509 -newkey rsa:2048 -nodes -days 825 `
            -keyout $Key -out $Cert `
            -subj "/CN=localhost/O=Kilometrix" `
            -addext "subjectAltName=DNS:localhost,IP:127.0.0.1,IP:::1" 2>$null
        # Selbstsigniertes Cert in den Benutzer-Root-Speicher importieren (kein Admin nötig)
        try {
            Import-Certificate -FilePath $Cert -CertStoreLocation Cert:\CurrentUser\Root | Out-Null
            Write-Host "    In Cert:\CurrentUser\Root importiert (vertrauenswürdig)."
        } catch {
            Write-Host "    Hinweis: Import in Cert:\CurrentUser\Root fehlgeschlagen — bitte manuell vertrauen."
        }
    }
    else {
        Write-Error "Weder mkcert noch openssl gefunden. Empfohlen: scoop install mkcert"
    }
    Write-Host "    Zertifikat: $Cert"
}

Write-Host "==> Backend (HTTPS) auf https://${VHost}:${Port}  (Add-in: https://${VHost}:${Port}/addin/taskpane.html)"
& $Uvicorn backend.main:app --host $VHost --port $Port --ssl-keyfile $Key --ssl-certfile $Cert
