// Package routing kapselt das Routing hinter einem schmalen Interface. Die einzige
// Implementierung (HTTPEngine) fragt einen lokalen osrm-routed per HTTP ab.
// OSRM erwartet Koordinaten als (lon, lat).
package routing

import (
	"encoding/json"
	"fmt"
	"math"     // math.Round
	"net/http" // HTTP-Client
	"net/url"  // URL-Escaping
	"sync"     // WaitGroup für die parallele Verarbeitung
	"time"

	"github.com/phischmi/kilometrix/internal/geocode"
)

// Statuswerte je Route.
//
// GO-EINSTEIGER: Ein `const`-Block deklariert Konstanten. Go hat kein echtes
// "enum"; stattdessen nimmt man oft solche benannten String-Konstanten. Sie sind
// exportiert (Großbuchstabe) und werden im ganzen Projekt als Status verwendet.
const (
	StatusOK          = "ok"
	StatusSnappedFar  = "snapped_far"
	StatusNoRoute     = "no_route"
	StatusError       = "error"
	StatusPLZNotFound = "plz_not_found"
)

// Coord ist eine (lat, lon)-Koordinate (menschenfreundliche interne Reihenfolge).
//
// `type A = B` ist ein ALIAS (mit `=`): Coord IST exakt geocode.Coord, nur unter
// anderem Namen. So muss man im routing-Paket nicht überall "geocode.Coord"
// schreiben. (Ohne `=` wäre es ein eigenständiger neuer Typ.)
type Coord = geocode.Coord

// Result ist das Ergebnis einer einzelnen Route. Nullable-Felder sind Pointer
// (→ JSON null), passend zur bisherigen API.
//
// Warum Pointer (*float64) statt float64? Ein float64 ist nie "leer" — er ist
// mindestens 0. Mit einem Pointer kann der Wert dagegen `nil` sein, was im JSON
// zu `null` wird. So unterscheiden wir "Distanz = 0" von "keine Distanz vorhanden".
type Result struct {
	DistanceKm  *float64 `json:"distance_km"`
	DurationMin *float64 `json:"duration_min"`
	Status      string   `json:"status"`
	SnapM       *float64 `json:"snap_m"`
	Message     *string  `json:"message"`
}

// Engine ist das schmale Routing-Interface.
//
// GO-EINSTEIGER: Ein INTERFACE listet nur Methoden-Signaturen, keine Implementierung.
// Jeder Typ, der eine Methode `Route(Coord, Coord) Result` besitzt, "erfüllt"
// dieses Interface AUTOMATISCH — man muss das nirgends deklarieren (anders als
// Pythons explizites Erben/ABC). Vorteil: Der Server kennt nur `Engine` und ist
// damit von der konkreten HTTP-Implementierung entkoppelt; im Test kann man eine
// Fake-Engine einsetzen.
type Engine interface {
	Route(origin, dest Coord) Result
}

// HTTPEngine fragt osrm-routed per HTTP ab. Der http.Client ist thread-safe und wird
// über alle Worker geteilt.
type HTTPEngine struct {
	baseURL    string
	snapLimitM float64
	client     *http.Client // ein Pointer auf den HTTP-Client (geteilt, wiederverwendet)
}

// NewHTTPEngine erzeugt eine Engine gegen baseURL (z. B. http://127.0.0.1:5001).
// Rückgabetyp *HTTPEngine (Pointer). Da *HTTPEngine die Route-Methode hat,
// erfüllt es das Engine-Interface.
func NewHTTPEngine(baseURL string, snapLimitM float64) *HTTPEngine {
	return &HTTPEngine{
		baseURL:    trimSlash(baseURL),
		snapLimitM: snapLimitM,
		// Einen eigenen Client mit Timeout bauen (der Default-Client hat keinen!).
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// trimSlash entfernt abschließende "/", damit baseURL + Pfad sauber zusammenpasst.
func trimSlash(s string) string {
	// s[len(s)-1] ist das letzte Byte; s[:len(s)-1] ist alles ohne das letzte
	// Byte (Slicing wie in Python, aber auf Bytes).
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// osrmResponse spiegelt nur die Teile der OSRM-Antwort, die uns interessieren.
// json.Decode füllt anhand der Tags. Verschachtelte anonyme Structs ([]struct{...})
// bilden die JSON-Arrays "routes" und "waypoints" ab, ohne dass wir eigene
// benannte Typen dafür brauchen.
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
// Diese Methode macht HTTPEngine zur Engine.
func (e *HTTPEngine) Route(origin, dest Coord) Result {
	// ACHTUNG: OSRM will die Reihenfolge lon,lat — deshalb Lon zuerst.
	u := fmt.Sprintf("%s/route/v1/driving/%s,%s;%s,%s?overview=false",
		e.baseURL,
		ftoa(origin.Lon), ftoa(origin.Lat),
		ftoa(dest.Lon), ftoa(dest.Lat),
	)
	resp, err := e.client.Get(u)
	if err != nil {
		return errResult(err.Error()) // err.Error() = die Fehlermeldung als String
	}
	// Den Response-Body MUSS man schließen, sonst leakt die Verbindung. defer
	// erledigt das beim Verlassen der Funktion.
	defer resp.Body.Close()

	var data osrmResponse
	// Aus dem Body-Stream direkt in die Struct dekodieren.
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return errResult(err.Error())
	}
	return e.parse(data)
}

// parse wertet die OSRM-Antwort aus und füllt ein Result. (Eigene Methode, damit
// sie im Test ohne echtes HTTP geprüft werden kann — siehe routing_test.go.)
func (e *HTTPEngine) parse(res osrmResponse) Result {
	if res.Code != "Ok" || len(res.Routes) == 0 {
		code := res.Code
		// &code: Adresse einer lokalen Variable, um sie als *string-Feld (Message)
		// zu setzen. Das ist erlaubt — Go hält die Variable am Leben, solange der
		// Pointer existiert (kein "dangling pointer" wie in C).
		return Result{Status: StatusNoRoute, Message: &code}
	}
	km := round2(res.Routes[0].Distance / 1000.0) // Meter -> Kilometer
	min := round2(res.Routes[0].Duration / 60.0)  // Sekunden -> Minuten
	snap := maxSnap(res.Waypoints)
	status := StatusOK
	// snap != nil: es gibt überhaupt einen Wert; *snap: dessen Inhalt vergleichen.
	if snap != nil && *snap > e.snapLimitM {
		status = StatusSnappedFar
	}
	return Result{DistanceKm: &km, DurationMin: &min, Status: status, SnapM: snap}
}

// maxSnap liefert die größte Snap-Distanz aller Waypoints (oder nil, wenn keine
// vorhanden). Der Parametertyp wiederholt den anonymen struct-Typ aus osrmResponse.
func maxSnap(waypoints []struct {
	Distance *float64 `json:"distance"`
}) *float64 {
	var m *float64 // Nullwert eines Pointers ist nil
	for _, wp := range waypoints {
		if wp.Distance == nil {
			continue
		}
		if m == nil || *wp.Distance > *m {
			// Wichtig: erst in eine lokale Variable kopieren, dann deren Adresse
			// nehmen. Würde man &wp.Distance... nehmen, hinge man an der
			// Schleifenvariable. So zeigt m auf einen stabilen eigenen Wert.
			v := *wp.Distance
			m = &v
		}
	}
	return m
}

// errResult ist eine kleine Hilfsfunktion für Fehler-Ergebnisse.
func errResult(msg string) Result {
	return Result{Status: StatusError, Message: &msg}
}

// pair bündelt Origin/Dest für die parallele Verarbeitung. Kleingeschrieben = privat.
type pair struct {
	Origin Coord
	Dest   Coord
}

// Pair ist ein zu routendes Origin→Dest-Paar.
// Alias, damit der private Typ `pair` von außen unter dem Namen `Pair` nutzbar ist.
type Pair = pair

// MakePair erzeugt ein Pair. (Konstruktor, weil die Felder von außen nicht direkt
// gesetzt werden sollen.)
func MakePair(origin, dest Coord) Pair { return pair{origin, dest} }

// RoutePairs berechnet mehrere Paare parallel (Reihenfolge bleibt erhalten).
//
// GO-EINSTEIGER: Hier kommt Gos Nebenläufigkeit zum Einsatz — der eigentliche
// Grund, Go für so etwas zu nehmen. Drei Bausteine:
//   - Goroutine: ein extrem leichtgewichtiger "Thread", gestartet mit `go f()`.
//   - Channel:   eine typisierte Pipe, über die Goroutinen sicher kommunizieren.
//   - WaitGroup: ein Zähler, um auf das Ende mehrerer Goroutinen zu warten.
func RoutePairs(engine Engine, pairs []Pair, workers int) []Result {
	// Ergebnis-Slice vorab in voller Länge anlegen. Jeder Worker schreibt an den
	// Index i — verschiedene Indizes, daher KEIN Lock nötig, und die Reihenfolge
	// bleibt automatisch erhalten.
	results := make([]Result, len(pairs))
	if len(pairs) == 0 {
		return results
	}
	if workers < 1 {
		workers = 1
	}
	// jobs ist ein Channel von int (Indizes). Worker lesen daraus, der Verteiler
	// schreibt hinein. `make(chan int)` erzeugt einen ungepufferten Channel.
	jobs := make(chan int)
	var wg sync.WaitGroup
	// `workers` Goroutinen starten, die parallel Jobs abarbeiten.
	for w := 0; w < workers; w++ {
		wg.Add(1) // Zähler hochzählen: eine Goroutine mehr, auf die wir warten
		// `go func() { ... }()` startet eine anonyme Funktion als Goroutine.
		go func() {
			defer wg.Done() // beim Ende der Goroutine Zähler herunterzählen
			// `for i := range jobs` liest so lange aus dem Channel, bis er
			// geschlossen ist. Mehrere Worker lesen aus demselben Channel — Go
			// verteilt die Jobs automatisch & threadsicher.
			for i := range jobs {
				results[i] = engine.Route(pairs[i].Origin, pairs[i].Dest)
			}
		}()
	}
	// Alle Job-Indizes in den Channel schicken (der Verteiler). Blockiert jeweils,
	// bis ein freier Worker den Wert abnimmt.
	for i := range pairs {
		jobs <- i // "sende i in den Channel"
	}
	close(jobs) // signalisiert den Workern: keine Jobs mehr -> ihre range-Schleifen enden
	wg.Wait()   // warten, bis alle Worker fertig sind (Zähler bei 0)
	return results
}

// round2 rundet auf 2 Nachkommastellen.
func round2(v float64) float64 { return math.Round(v*100) / 100 }

// ftoa formatiert eine Koordinate kompakt ohne Exponenten und URL-escaped sie.
func ftoa(v float64) string {
	return url.QueryEscape(trimFloat(v))
}

// trimFloat: %g wählt die kürzeste sinnvolle Darstellung (ohne unnötige Nullen
// und ohne Exponentialschreibweise bei normalen Koordinaten).
func trimFloat(v float64) string {
	return fmt.Sprintf("%g", v)
}
