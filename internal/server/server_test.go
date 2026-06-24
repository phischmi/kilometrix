package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest" // Test-Helfer: Requests/Recorder ohne echtes Netzwerk
	"testing"

	"github.com/phischmi/kilometrix/internal/config"
	"github.com/phischmi/kilometrix/internal/geocode"
	"github.com/phischmi/kilometrix/internal/routing"
)

// stubEngine ist wieder eine Fake-Engine (erfüllt routing.Engine) und liefert
// feste Werte, damit der Test nicht von osrm abhängt.
type stubEngine struct{ calls int }

func (s *stubEngine) Route(o, d routing.Coord) routing.Result {
	s.calls++
	km, min := 123.45, 67.8
	return routing.Result{DistanceKm: &km, DurationMin: &min, Status: routing.StatusOK}
}

// testServer baut einen Server mit Minimal-Einstellungen für die Tests.
func testServer(eng routing.Engine, geo *geocode.Geocoder) *Server {
	s := config.Settings{MaxSyncBatch: 1000, Workers: 4, AddinDir: "."}
	return New(s, eng, "", geo)
}

// Der wichtigste Integrationstest: deckt Geocoding, Echo der Koordinaten und das
// Überspringen unbekannter PLZ in einem Durchlauf ab.
func TestRouteBatchResolvesAndEchoes(t *testing.T) {
	tbl, _ := geocode.LoadTable(bytes.NewReader([]byte(
		"country,plz,lat,lon\nDE,80331,48.13,11.57\nDE,10115,52.53,13.38\n")))
	geo := geocode.New(tbl)
	eng := &stubEngine{}
	srv := testServer(eng, geo)

	// Request-Body als JSON (raw string literal über mehrere Zeilen).
	body := `{"pairs":[
		{"id":"A1","origin_lkz":"DE","origin_plz":"80331","dest_lkz":"DE","dest_plz":"10115"},
		{"id":"A2","origin_lkz":"DE","origin_plz":"00000","dest_lkz":"DE","dest_plz":"10115"},
		{"id":"A3","origin_lat":48.1,"origin_lon":11.5,"dest_lat":52.5,"dest_lon":13.4}
	]}`
	// httptest.NewRequest baut einen Fake-Request, NewRecorder fängt die Antwort
	// ab. ServeHTTP ruft den echten Handler — alles im Speicher, ohne Port.
	req := httptest.NewRequest("POST", "/route-batch", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	// Antwort-JSON in eine passende Struct dekodieren.
	var resp struct {
		Results []routeOut `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("want 3 results, got %d", len(resp.Results))
	}
	// A1: geocodet + geroutet, Koordinaten echoed
	if resp.Results[0].Status != routing.StatusOK || resp.Results[0].OriginLat == nil || *resp.Results[0].OriginLat != 48.13 {
		t.Fatalf("A1 unerwartet: %+v", resp.Results[0]) // %+v zeigt Feldnamen+Werte
	}
	// A2: unbekannte origin-PLZ → plz_not_found, kein Routing
	if resp.Results[1].Status != routing.StatusPLZNotFound || resp.Results[1].OriginLat != nil {
		t.Fatalf("A2 unerwartet: %+v", resp.Results[1])
	}
	// A3: reine Koordinaten
	if resp.Results[2].Status != routing.StatusOK {
		t.Fatalf("A3 unerwartet: %+v", resp.Results[2])
	}
	// nur A1 und A3 wurden geroutet (A2 fiel wegen unbekannter PLZ raus)
	if eng.calls != 2 {
		t.Fatalf("engine calls = %d, want 2", eng.calls)
	}
}

// Ohne Geocoder muss eine PLZ-Anfrage mit 503 abgewiesen werden.
func TestRouteBatch503WithoutGeocoder(t *testing.T) {
	srv := testServer(&stubEngine{}, nil) // geo = nil
	body := `{"pairs":[{"id":"A1","origin_lkz":"DE","origin_plz":"80331","dest_lkz":"DE","dest_plz":"10115"}]}`
	req := httptest.NewRequest("POST", "/route-batch", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestHealth(t *testing.T) {
	srv := testServer(&stubEngine{}, nil)
	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var h map[string]any
	json.Unmarshal(rec.Body.Bytes(), &h)
	// engine_ready muss true (Stub gesetzt), geocode_ready false (geo == nil) sein.
	if h["engine_ready"] != true || h["geocode_ready"] != false {
		t.Fatalf("health = %v", h)
	}
}
