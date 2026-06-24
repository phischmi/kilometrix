// Package routing kapselt das Routing hinter einem schmalen Interface. Die einzige
// Implementierung (HTTPEngine) fragt einen lokalen osrm-routed per HTTP ab.
// OSRM erwartet Koordinaten als (lon, lat).
package routing

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/phischmi/kilometrix/internal/geocode"
)

// Statuswerte je Route.
const (
	StatusOK          = "ok"
	StatusSnappedFar  = "snapped_far"
	StatusNoRoute     = "no_route"
	StatusError       = "error"
	StatusPLZNotFound = "plz_not_found"
)

// Coord ist eine (lat, lon)-Koordinate (menschenfreundliche interne Reihenfolge).
type Coord = geocode.Coord

// Result ist das Ergebnis einer einzelnen Route. Nullable-Felder sind Pointer
// (→ JSON null), passend zur bisherigen API.
type Result struct {
	DistanceKm  *float64 `json:"distance_km"`
	DurationMin *float64 `json:"duration_min"`
	Status      string   `json:"status"`
	SnapM       *float64 `json:"snap_m"`
	Message     *string  `json:"message"`
}

// Engine ist das schmale Routing-Interface.
type Engine interface {
	Route(origin, dest Coord) Result
}

// HTTPEngine fragt osrm-routed per HTTP ab. Der http.Client ist thread-safe und wird
// über alle Worker geteilt.
type HTTPEngine struct {
	baseURL    string
	snapLimitM float64
	client     *http.Client
}

// NewHTTPEngine erzeugt eine Engine gegen baseURL (z. B. http://127.0.0.1:5001).
func NewHTTPEngine(baseURL string, snapLimitM float64) *HTTPEngine {
	return &HTTPEngine{
		baseURL:    trimSlash(baseURL),
		snapLimitM: snapLimitM,
		client:     &http.Client{Timeout: 30 * time.Second},
	}
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

type osrmResponse struct {
	Code   string `json:"code"`
	Routes []struct {
		Distance float64 `json:"distance"`
		Duration float64 `json:"duration"`
	} `json:"routes"`
	Waypoints []struct {
		Distance *float64 `json:"distance"`
	} `json:"waypoints"`
}

// Route berechnet die kürzeste Fahrstrecke zwischen origin und dest (je (lat, lon)).
func (e *HTTPEngine) Route(origin, dest Coord) Result {
	u := fmt.Sprintf("%s/route/v1/driving/%s,%s;%s,%s?overview=false",
		e.baseURL,
		ftoa(origin.Lon), ftoa(origin.Lat),
		ftoa(dest.Lon), ftoa(dest.Lat),
	)
	resp, err := e.client.Get(u)
	if err != nil {
		return errResult(err.Error())
	}
	defer resp.Body.Close()

	var data osrmResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return errResult(err.Error())
	}
	return e.parse(data)
}

func (e *HTTPEngine) parse(res osrmResponse) Result {
	if res.Code != "Ok" || len(res.Routes) == 0 {
		code := res.Code
		return Result{Status: StatusNoRoute, Message: &code}
	}
	km := round2(res.Routes[0].Distance / 1000.0)
	min := round2(res.Routes[0].Duration / 60.0)
	snap := maxSnap(res.Waypoints)
	status := StatusOK
	if snap != nil && *snap > e.snapLimitM {
		status = StatusSnappedFar
	}
	return Result{DistanceKm: &km, DurationMin: &min, Status: status, SnapM: snap}
}

func maxSnap(waypoints []struct {
	Distance *float64 `json:"distance"`
}) *float64 {
	var m *float64
	for _, wp := range waypoints {
		if wp.Distance == nil {
			continue
		}
		if m == nil || *wp.Distance > *m {
			v := *wp.Distance
			m = &v
		}
	}
	return m
}

func errResult(msg string) Result {
	return Result{Status: StatusError, Message: &msg}
}

// pair bündelt Origin/Dest für die parallele Verarbeitung.
type pair struct {
	Origin Coord
	Dest   Coord
}

// Pair ist ein zu routendes Origin→Dest-Paar.
type Pair = pair

// MakePair erzeugt ein Pair.
func MakePair(origin, dest Coord) Pair { return pair{origin, dest} }

// RoutePairs berechnet mehrere Paare parallel (Reihenfolge bleibt erhalten).
func RoutePairs(engine Engine, pairs []Pair, workers int) []Result {
	results := make([]Result, len(pairs))
	if len(pairs) == 0 {
		return results
	}
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = engine.Route(pairs[i].Origin, pairs[i].Dest)
			}
		}()
	}
	for i := range pairs {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// ftoa formatiert eine Koordinate kompakt ohne Exponenten.
func ftoa(v float64) string {
	return url.QueryEscape(trimFloat(v))
}

func trimFloat(v float64) string {
	return fmt.Sprintf("%g", v)
}
