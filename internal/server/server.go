// Package server stellt das HTTP-Backend bereit: liefert das Office.js-Add-in aus
// (same-origin), /health, /auth/check und /route-batch (inkl. LKZ/PLZ-Geocoding).
package server

import (
	"encoding/json"
	"fmt"
	"net/http" // Gos eingebauter HTTP-Server (kein externes Framework nötig)
	"os"
	"path/filepath"
	"strings"

	"github.com/phischmi/kilometrix/internal/config"
	"github.com/phischmi/kilometrix/internal/geocode"
	"github.com/phischmi/kilometrix/internal/routing"
	"github.com/phischmi/kilometrix/internal/tokens"
)

// Server bündelt die Abhängigkeiten der Handler.
//
// Statt globaler Variablen werden alle benötigten Dinge (Engine, Geocoder, Config)
// in dieser Struct gehalten. Die Handler sind Methoden darauf und kommen so an
// ihren Zustand — sauber testbar und ohne globale Zustände.
type Server struct {
	settings    config.Settings
	engine      routing.Engine // Interface-Typ: kann echte HTTPEngine oder Test-Stub sein
	engineErr   string
	geocoder    *geocode.Geocoder // Pointer; nil = Geocoding aus
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
//
// Rückgabetyp http.Handler ist ein Interface mit der Methode ServeHTTP. Alles,
// was das erfüllt (mux, der cors-Wrapper, ...), ist ein Handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux() // ein "Multiplexer" = Pfad -> Handler-Zuordnung

	// Add-in same-origin ausliefern (/addin/...). html=true-Verhalten: Verzeichnis → index.
	addinDir := s.settings.AddinDir
	if st, err := os.Stat(addinDir); err == nil && st.IsDir() {
		// FileServer liefert Dateien aus einem Verzeichnis; StripPrefix entfernt
		// "/addin/" aus dem Pfad, bevor im Verzeichnis gesucht wird.
		fs := http.StripPrefix("/addin/", http.FileServer(http.Dir(addinDir)))
		mux.Handle("/addin/", fs)
	}

	// Seit Go 1.22 kann man Methode + Pfad als Muster angeben ("GET /health").
	// {$} matcht nur den exakten Root-Pfad "/" — sonst kollidiert "GET /" mit "/addin/".
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/addin/taskpane.html", http.StatusFound)
	})
	// HandleFunc verknüpft ein Muster mit einer Handler-FUNKTION. Die Methoden
	// s.handleHealth usw. passen auf die Signatur func(ResponseWriter, *Request).
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /auth/check", s.handleAuthCheck)
	mux.HandleFunc("POST /route-batch", s.handleRouteBatch)

	return cors(mux) // den ganzen mux noch in CORS-Middleware einwickeln
}

// cors: lokales Tool → offen, damit das Office.js-Add-in das Backend erreicht.
//
// GO-EINSTEIGER: Das ist "Middleware" — eine Funktion, die einen Handler nimmt und
// einen NEUEN Handler zurückgibt, der etwas ergänzt (hier CORS-Header) und dann
// den inneren aufruft. http.HandlerFunc wandelt eine Funktion in einen Handler um.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "*")
		h.Set("Access-Control-Allow-Headers", "*")
		// Preflight-Anfragen (OPTIONS) direkt mit 204 beantworten.
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r) // an den eigentlichen Handler weiterreichen
	})
}

// handleHealth meldet den Zustand des Backends als JSON.
// Der zweite Parameter (*http.Request) wird nicht gebraucht -> `_`.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"engine_ready":  s.engine != nil, // Ausdruck ergibt direkt true/false
		"engine_error":  nullable(s.engineErr),
		"geocode_ready": s.geocoder != nil,
		"auth_required": s.authEnabled,
	})
}

// requireToken prüft den Bearer-Token, falls Auth aktiv. Liefert Claims oder Fehler.
// Rückgabe *tokens.Claims (Pointer, damit nil = "kein Token nötig" ausdrückbar ist).
func (s *Server) requireToken(r *http.Request) (*tokens.Claims, error) {
	if !s.authEnabled {
		return nil, nil // Auth aus: alles erlaubt
	}
	auth := r.Header.Get("Authorization")
	// Header sollte "Bearer <token>" sein (Groß/Klein egal -> ToLower).
	if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return nil, fmt.Errorf("Zugangstoken fehlt")
	}
	// auth[len("bearer "):] schneidet das Präfix ab und lässt den reinen Token übrig.
	c, err := tokens.Verify(s.authSecret, strings.TrimSpace(auth[len("bearer "):]))
	if err != nil {
		return nil, fmt.Errorf("Token ungültig: %w", err)
	}
	return &c, nil // Adresse der lokalen Claims-Kopie zurückgeben
}

func (s *Server) handleAuthCheck(w http.ResponseWriter, r *http.Request) {
	claims, err := s.requireToken(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if claims == nil { // Auth ist aus
		writeJSON(w, http.StatusOK, map[string]any{"auth_required": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"auth_required": true, "name": claims.Sub, "exp": claims.Exp,
	})
}

// routePair: jeder Endpunkt ENTWEDER Koordinaten ODER LKZ+PLZ.
//
// Die Koordinaten sind *float64 (Pointer), damit "Feld fehlt im JSON" als nil
// erkennbar ist — bei float64 wäre ein fehlendes Feld nicht von 0.0 unterscheidbar.
// ID ist `any`, weil sie im JSON String oder Zahl sein kann.
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

// endpointOK: ein Endpunkt ist gültig, wenn er ENTWEDER beide Koordinaten ODER
// LKZ und PLZ hat.
func endpointOK(lat, lon *float64, lkz, plz string) bool {
	return (lat != nil && lon != nil) || (lkz != "" && plz != "")
}

// routeBatchRequest ist die JSON-Hülle des Requests: { "pairs": [ ... ] }.
type routeBatchRequest struct {
	Pairs []routePair `json:"pairs"`
}

// routeOut ist eine Ergebniszeile (gespiegelte ID + berechnete Werte + Koordinaten).
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

// handleRouteBatch ist der Kern-Endpunkt: nimmt viele Paare, löst ggf. PLZ auf,
// routet parallel und gibt die Ergebnisse in derselben Reihenfolge zurück.
func (s *Server) handleRouteBatch(w http.ResponseWriter, r *http.Request) {
	// 1) Auth
	if _, err := s.requireToken(r); err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	// 2) JSON aus dem Request-Body in die Struct dekodieren.
	var req routeBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "ungültiges JSON: "+err.Error())
		return
	}
	// 3) Engine bereit? Wenn nicht -> 503 mit der gemerkten Startfehler-Meldung.
	if s.engine == nil {
		writeError(w, http.StatusServiceUnavailable, "Routing-Engine nicht bereit: "+s.engineErr)
		return
	}
	// 4) Block nicht zu groß? (Schutz vor Speicher-Explosion.)
	if len(req.Pairs) > s.maxBatch {
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("%d Paare > Limit %d. Bitte in kleineren Blöcken senden.", len(req.Pairs), s.maxBatch))
		return
	}
	// 5) Validierung: jeder Endpunkt braucht Koordinaten oder LKZ+PLZ.
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

	// 6) Braucht überhaupt jemand Geocoding? Wenn ja, aber kein Geocoder da -> 503.
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

	// 7) Endpunkte auflösen (Koordinaten direkt ODER LKZ/PLZ → Zentroid). Dedupe je Request.
	// cache merkt sich bereits aufgelöste PLZ, damit dieselbe PLZ nicht mehrfach
	// nachgeschlagen wird. Wert ist *geocode.Coord (nil = "PLZ nicht gefunden").
	cache := map[string]*geocode.Coord{}
	// Ein lokaler Hilfs-Struct-Typ, nur innerhalb dieser Funktion gültig.
	type resolved struct{ origin, dest *geocode.Coord }
	res := make([]resolved, len(req.Pairs))
	// toRoute sammelt nur die Paare, die wirklich geroutet werden können (beide
	// Endpunkte aufgelöst). idxOfRoute merkt sich, zu welchem Originalindex jedes
	// gehört, damit wir die Ergebnisse später korrekt zurücksortieren.
	var toRoute []routing.Pair
	idxOfRoute := make([]int, 0, len(req.Pairs)) // Länge 0, Kapazität vorab reserviert
	for i, p := range req.Pairs {
		o := s.resolveEndpoint(p.OriginLat, p.OriginLon, p.OriginLkz, p.OriginPlz, cache)
		d := s.resolveEndpoint(p.DestLat, p.DestLon, p.DestLkz, p.DestPlz, cache)
		res[i] = resolved{o, d}
		if o != nil && d != nil {
			// *o dereferenziert den Pointer (übergibt die Coord als Wert).
			toRoute = append(toRoute, routing.MakePair(*o, *d))
			idxOfRoute = append(idxOfRoute, i)
		}
	}
	// 8) Die routbaren Paare parallel berechnen (Worker-Pool aus routing).
	routeResults := routing.RoutePairs(s.engine, toRoute, s.workers)

	// 9) Ergebnisse zusammenbauen, in Originalreihenfolge.
	out := make([]routeOut, len(req.Pairs))
	// Die geroutet-Ergebnisse über ihren Originalindex auffindbar machen.
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
			// Mindestens ein Endpunkt war eine unbekannte PLZ.
			rr = routing.Result{Status: routing.StatusPLZNotFound}
		}
		out[i] = routeOut{
			ID: p.ID, DistanceKm: rr.DistanceKm, DurationMin: rr.DurationMin,
			Status: rr.Status, SnapM: rr.SnapM, Message: rr.Message,
			// Aufgelöste Koordinaten zurückspiegeln (oder nil, wenn nicht gefunden).
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
	// Cache-Schlüssel aus lkz + plz. \x00 (Null-Byte) als Trenner, damit z. B.
	// ("D","E1") und ("DE","1") nicht denselben Schlüssel ergeben.
	key := lkz + "\x00" + plz
	if c, ok := cache[key]; ok {
		return c // schon mal aufgelöst (auch nil wird gecacht = "nicht gefunden")
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

// latPtr/lonPtr holen einzelne Koordinaten-Felder als Pointer heraus (nil-sicher),
// damit sie als *float64 ins JSON passen (nil -> null).
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

// nullable macht aus einem leeren String ein JSON-null, sonst den String selbst.
// Rückgabetyp `any`, weil mal nil (null) und mal ein String herauskommt.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// writeJSON schreibt v als JSON mit Statuscode. Reihenfolge ist wichtig:
// erst Header setzen, dann WriteHeader(status), dann den Body schreiben.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // direkt in den Response-Stream kodieren
}

// writeError schreibt eine einheitliche Fehler-Antwort { "detail": "..." }.
func writeError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]any{"detail": detail})
}

// AddinIndexPath ist ein Komfort-Helfer für Statusausgaben.
func AddinIndexPath(s config.Settings) string {
	return filepath.Join(s.AddinDir, "taskpane.html")
}
