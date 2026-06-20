# Kilometrix FastAPI-App (liefert Add-in + /route-batch). Läuft hinter Traefik (TLS).
# osrm-routed läuft als separater Container (siehe docker-compose.yml).
FROM python:3.12-slim

WORKDIR /app

# Abhängigkeiten aus pyproject installieren (Backend wird zusätzlich aus /app importiert,
# damit _ADDIN_DIR = /app/addin gefunden wird).
COPY pyproject.toml README.md ./
COPY backend ./backend
RUN pip install --no-cache-dir .

COPY addin ./addin

EXPOSE 8000
# Aus /app starten, damit "backend" aus dem Quellbaum kommt (cwd) und /app/addin sichtbar ist.
CMD ["uvicorn", "backend.main:app", "--host", "0.0.0.0", "--port", "8000"]
