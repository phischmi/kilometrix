// Package server stellt das HTTP-Backend bereit: liefert das Office.js-Add-in aus
// (same-origin), /health, /auth/check und /route-batch (inkl. LKZ/PLZ-Geocoding).
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/phischmi/kilometrix/internal/config"
	"github.com/phischmi/kilometrix/internal/geocode"
	"github.com/phischmi/kilometrix/internal/routing"
	"github.com/phischmi/kilometrix/internal/tokens"
)

// Server bündelt die Abhängigkeiten der Handler.
type Server struct {
	settings    config.Settings
	engine      routing.Engine
	engineErr   string
	geocoder    *geocode.Geocoder
	maxBatch    int
	workers     int
	authEnabled bool
	authSecret  string
}

// New erzeugt einen Server. engine darf nil sein (dann meldet /route-batch 503).
func New(s config.Settings, engine routing.Engine, engineErr string, geo *geocode.Geocoder) *Server {
	return &Server{
		settings:    s,
		engine:      engine,
		engineErr:   engineErr,
		geocoder:    geo,
		maxBatch:    s.MaxSyncBatch,
		workers:     s.Workers,
		authEnabled: s.AuthEnabled,
		authSecret:  s.AuthSecret,
	}
}

// Handler baut den Router (net/http ServeMux mit Methoden-Patterns).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Add-in same-origin ausliefern (/addin/...). html=true-Verhalten: Verzeichnis → index.
	addinDir := s.settings.AddinDir
	if st, err := os.Stat(addinDir); err == nil && st.IsDir() {
		fs := http.StripPrefix("/addin/", http.FileServer(http.Dir(addinDir)))
		mux.Handle("/addin/", fs)
	}

	// {$} matcht nur den exakten Root-Pfad "/" — sonst kollidiert "GET /" mit "/addin/".
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/addin/taskpane.html", http.StatusFound)
	})
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /auth/check", s.handleAuthCheck)
	mux.HandleFunc("POST /route-batch", s.handleRouteBatch)

	return cors(mux)
}

// cors: lokales Tool → offen, damit das Office.js-Add-in das Backend erreicht.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "*")
		h.Set("Access-Control-Allow-Headers", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"engine_ready":  s.engine != nil,
		"engine_error":  nullable(s.engineErr),
		"geocode_ready": s.geocoder != nil,
		"auth_required": s.authEnabled,
	})
}

// requireToken prüft den Bearer-Token, falls Auth aktiv. Liefert Claims oder Fehler.
func (s *Server) requireToken(r *http.Request) (*tokens.Claims, error) {
	if !s.authEnabled {
		return nil, nil
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return nil, fmt.Errorf("Zugangstoken fehlt")
	}
	c, err := tokens.Verify(s.authSecret, strings.TrimSpace(auth[len("bearer "):]))
	if err != nil {
		return nil, fmt.Errorf("Token ungültig: %w", err)
	}
	return &c, nil
}

func (s *Server) handleAuthCheck(w http.ResponseWriter, r *http.Request) {
	claims, err := s.requireToken(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if claims == nil {
		writeJSON(w, http.StatusOK, map[string]any{"auth_required": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"auth_required": true, "name": claims.Sub, "exp": claims.Exp,
	})
}

// routePair: jeder Endpunkt ENTWEDER Koordinaten ODER LKZ+PLZ.
type routePair struct {
	ID        any      `json:"id"`
	OriginLat *float64 `json:"origin_lat"`
	OriginLon *float64 `json:"origin_lon"`
	DestLat   *float64 `json:"dest_lat"`
	DestLon   *float64 `json:"dest_lon"`
	OriginLkz string   `json:"origin_lkz"`
	OriginPlz string   `json:"origin_plz"`
	DestLkz   string   `json:"dest_lkz"`
	DestPlz   string   `json:"dest_plz"`
}

func endpointOK(lat, lon *float64, lkz, plz string) bool {
	return (lat != nil && lon != nil) || (lkz != "" && plz != "")
}

type routeBatchRequest struct {
	Pairs []routePair `json:"pairs"`
}

type routeOut struct {
	ID          any      `json:"id"`
	DistanceKm  *float64 `json:"distance_km"`
	DurationMin *float64 `json:"duration_min"`
	Status      string   `json:"status"`
	SnapM       *float64 `json:"snap_m"`
	Message     *string  `json:"message"`
	OriginLat   *float64 `json:"origin_lat"`
	OriginLon   *float64 `json:"origin_lon"`
	DestLat     *float64 `json:"dest_lat"`
	DestLon     *float64 `json:"dest_lon"`
}

func (s *Server) handleRouteBatch(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireToken(r); err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req routeBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "ungültiges JSON: "+err.Error())
		return
	}
	if s.engine == nil {
		writeError(w, http.StatusServiceUnavailable, "Routing-Engine nicht bereit: "+s.engineErr)
		return
	}
	if len(req.Pairs) > s.maxBatch {
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("%d Paare > Limit %d. Bitte in kleineren Blöcken senden.", len(req.Pairs), s.maxBatch))
		return
	}
	// Validierung: jeder Endpunkt braucht Koordinaten oder LKZ+PLZ.
	for i, p := range req.Pairs {
		if !endpointOK(p.OriginLat, p.OriginLon, p.OriginLkz, p.OriginPlz) {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("Paar %d origin: Koordinaten ODER LKZ+PLZ angeben", i))
			return
		}
		if !endpointOK(p.DestLat, p.DestLon, p.DestLkz, p.DestPlz) {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("Paar %d dest: Koordinaten ODER LKZ+PLZ angeben", i))
			return
		}
	}

	needsGeo := false
	for _, p := range req.Pairs {
		if p.OriginLat == nil || p.OriginLon == nil || p.DestLat == nil || p.DestLon == nil {
			needsGeo = true
			break
		}
	}
	if needsGeo && s.geocoder == nil {
		writeError(w, http.StatusServiceUnavailable,
			"Geocoding nicht verfügbar: data/plz_centroids.csv fehlt. Bitte 'kilometrix build-geocode' ausführen.")
		return
	}

	// Endpunkte auflösen (Koordinaten direkt ODER LKZ/PLZ → Zentroid). Dedupe je Request.
	cache := map[string]*geocode.Coord{}
	type resolved struct{ origin, dest *geocode.Coord }
	res := make([]resolved, len(req.Pairs))
	var toRoute []routing.Pair
	idxOfRoute := make([]int, 0, len(req.Pairs))
	for i, p := range req.Pairs {
		o := s.resolveEndpoint(p.OriginLat, p.OriginLon, p.OriginLkz, p.OriginPlz, cache)
		d := s.resolveEndpoint(p.DestLat, p.DestLon, p.DestLkz, p.DestPlz, cache)
		res[i] = resolved{o, d}
		if o != nil && d != nil {
			toRoute = append(toRoute, routing.MakePair(*o, *d))
			idxOfRoute = append(idxOfRoute, i)
		}
	}
	routeResults := routing.RoutePairs(s.engine, toRoute, s.workers)

	out := make([]routeOut, len(req.Pairs))
	routeAt := map[int]routing.Result{}
	for k, i := range idxOfRoute {
		routeAt[i] = routeResults[k]
	}
	for i, p := range req.Pairs {
		o, d := res[i].origin, res[i].dest
		var rr routing.Result
		if o != nil && d != nil {
			rr = routeAt[i]
		} else {
			rr = routing.Result{Status: routing.StatusPLZNotFound}
		}
		out[i] = routeOut{
			ID: p.ID, DistanceKm: rr.DistanceKm, DurationMin: rr.DurationMin,
			Status: rr.Status, SnapM: rr.SnapM, Message: rr.Message,
			OriginLat: latPtr(o), OriginLon: lonPtr(o), DestLat: latPtr(d), DestLon: lonPtr(d),
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out})
}

// resolveEndpoint: Koordinaten haben Vorrang; sonst LKZ/PLZ über den Geocoder (mit Dedupe).
func (s *Server) resolveEndpoint(lat, lon *float64, lkz, plz string, cache map[string]*geocode.Coord) *geocode.Coord {
	if lat != nil && lon != nil {
		return &geocode.Coord{Lat: *lat, Lon: *lon}
	}
	key := lkz + "\x00" + plz
	if c, ok := cache[key]; ok {
		return c
	}
	var out *geocode.Coord
	if s.geocoder != nil {
		if c, ok := s.geocoder.Resolve(lkz, plz); ok {
			out = &c
		}
	}
	cache[key] = out
	return out
}

func latPtr(c *geocode.Coord) *float64 {
	if c == nil {
		return nil
	}
	return &c.Lat
}
func lonPtr(c *geocode.Coord) *float64 {
	if c == nil {
		return nil
	}
	return &c.Lon
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]any{"detail": detail})
}

// AddinIndexPath ist ein Komfort-Helfer für Statusausgaben.
func AddinIndexPath(s config.Settings) string {
	return filepath.Join(s.AddinDir, "taskpane.html")
}
