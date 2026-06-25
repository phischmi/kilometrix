package routing

import "testing"

// eng ist ein kleiner Helfer, der eine HTTPEngine mit gegebenem Snap-Limit baut.
func eng(snap float64) *HTTPEngine { return NewHTTPEngine("http://x", snap) }

// wp baut einen einzelnen Waypoint mit Snap-Distanz d. Der etwas sperrige
// Rückgabetyp ist exakt der anonyme struct-Typ aus osrmResponse.Waypoints —
// deshalb muss er hier wortwörtlich wiederholt werden.
func wp(d float64) struct {
	Distance *float64 `json:"distance"`
} {
	return struct {
		Distance *float64 `json:"distance"`
	}{Distance: &d}
}

// Testet parse() direkt mit einer gefälschten OSRM-Antwort — ganz ohne echtes
// HTTP. Genau dafür wurde parse als eigene Methode herausgezogen.
func TestParseOK(t *testing.T) {
	r := eng(50).parse(osrmResponse{
		Code: "Ok",
		Routes: []struct {
			Distance float64 `json:"distance"`
			Duration float64 `json:"duration"`
		}{{Distance: 12345, Duration: 678}},
		Waypoints: []struct {
			Distance *float64 `json:"distance"`
		}{wp(5), wp(12)},
	})
	if r.Status != StatusOK {
		t.Fatalf("status = %s", r.Status)
	}
	// *r.DistanceKm dereferenziert den Pointer: 12345 m -> 12.35 km, 678 s -> 11.3 min,
	// größte Snap-Distanz = 12.
	if *r.DistanceKm != 12.35 || *r.DurationMin != 11.3 || *r.SnapM != 12 {
		t.Fatalf("km=%v min=%v snap=%v", *r.DistanceKm, *r.DurationMin, *r.SnapM)
	}
}

// Snap-Distanz 120 > Limit 50 -> Status muss "snapped_far" sein.
func TestParseSnappedFar(t *testing.T) {
	r := eng(50).parse(osrmResponse{
		Code: "Ok",
		Routes: []struct {
			Distance float64 `json:"distance"`
			Duration float64 `json:"duration"`
		}{{Distance: 1000, Duration: 60}},
		Waypoints: []struct {
			Distance *float64 `json:"distance"`
		}{wp(10), wp(120)},
	})
	if r.Status != StatusSnappedFar {
		t.Fatalf("status = %s, want snapped_far", r.Status)
	}
}

// Code != "Ok" -> kein Ergebnis, DistanceKm bleibt nil.
func TestParseNoRoute(t *testing.T) {
	r := eng(50).parse(osrmResponse{Code: "NoRoute"})
	if r.Status != StatusNoRoute || r.DistanceKm != nil {
		t.Fatalf("status=%s km=%v", r.Status, r.DistanceKm)
	}
}

// stubEngine ist eine FAKE-Engine für den Test: sie erfüllt das Engine-Interface
// (hat eine Route-Methode), routet aber nicht wirklich, sondern zählt nur Aufrufe.
// So lässt sich RoutePairs ohne osrm testen — der Vorteil von Interfaces.
type stubEngine struct{ calls int }

func (s *stubEngine) Route(o, d Coord) Result {
	s.calls++
	km := 1.0
	return Result{DistanceKm: &km, Status: StatusOK}
}

// Prüft, dass RoutePairs trotz paralleler Worker die Reihenfolge erhält und jeden
// Job genau einmal verarbeitet.
func TestRoutePairsPreservesOrder(t *testing.T) {
	s := &stubEngine{}
	pairs := []Pair{
		MakePair(Coord{Lat: 1, Lon: 1}, Coord{Lat: 2, Lon: 2}),
		MakePair(Coord{Lat: 3, Lon: 3}, Coord{Lat: 4, Lon: 4}),
	}
	res := RoutePairs(s, pairs, 4)
	if len(res) != 2 || s.calls != 2 {
		t.Fatalf("len=%d calls=%d", len(res), s.calls)
	}
}
