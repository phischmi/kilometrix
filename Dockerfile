# Kilometrix-Backend (Go). Liefert das Add-in + /route-batch, zeigt auf den separaten
# osrm-Container (MANAGE_OSRM_ROUTED=false). Läuft hinter Traefik (TLS terminiert Traefik).
#
# Build-Stage: statisches Go-Binary (reine stdlib, keine externen Abhängigkeiten).
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/kilometrix ./cmd/kilometrix

# Runtime-Stage: minimal + non-root. Enthält nur Binary + Add-in-Dateien.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/kilometrix /usr/local/bin/kilometrix
COPY addin ./addin

EXPOSE 8000
ENTRYPOINT ["/usr/local/bin/kilometrix"]
# HTTP (kein TLS — das macht Traefik), an alle Interfaces auf :8000.
CMD ["serve", "--host", "0.0.0.0", "--port", "8000", "--tls=false"]
