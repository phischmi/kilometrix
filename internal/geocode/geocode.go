// Package geocode löst LKZ/PLZ-Kombinationen offline zu Zentroid-Koordinaten auf.
// Die Tabelle (country,plz,lat,lon) wird mit dem build-geocode-Subcommand aus dem
// GeoNames-Postal-Datensatz (CC BY 4.0) erzeugt.
package geocode

import (
	"encoding/csv" // CSV lesen
	"errors"       // Fehlerwerte erzeugen/vergleichen (errors.Is)
	"io"           // io.Reader/io.EOF — abstrakte Datenströme
	"os"           // Datei öffnen
	"strconv"      // String -> float
	"strings"
)

// Coord ist eine (lat, lon)-Koordinate.
//
// GO-EINSTEIGER: Eine kleine struct mit zwei float64-Feldern. Wird per Wert
// kopiert, wenn man sie herumreicht (kein versteckter Zeiger wie bei Python-Objekten).
type Coord struct {
	Lat float64
	Lon float64
}

// Geocoder ist ein In-Memory-Lookup (LKZ, PLZ) → Zentroid.
//
// Das Feld `table` ist klein geschrieben -> privat (nur innerhalb dieses Pakets
// sichtbar). Eine `map[key]Coord` ist ein Dictionary mit Schlüsseltyp `key` und
// Wert `Coord`.
type Geocoder struct {
	table map[key]Coord
}

// key ist der zusammengesetzte Map-Schlüssel. In Go darf eine struct als
// Map-Schlüssel dienen (sie ist vergleichbar), solange alle Felder vergleichbar
// sind — sehr praktisch statt String-Verkettung wie "DE|01067".
type key struct {
	country string
	plz     string
}

// NormCountry normalisiert das Länderkennzeichen (trim + Großbuchstaben).
func NormCountry(lkz string) string {
	return strings.ToUpper(strings.TrimSpace(lkz))
}

// NormPLZ normalisiert die PLZ. Excel liefert PLZ oft als Zahl → führende Nullen
// fehlen; deutsche PLZ sind fünfstellig, daher auffüllen.
func NormPLZ(country, plz string) string {
	p := strings.TrimSpace(plz)
	if country == "DE" && isDigits(p) && len(p) < 5 {
		// "0" so oft wiederholen, bis 5 Stellen erreicht sind, dann p anhängen.
		p = strings.Repeat("0", 5-len(p)) + p
	}
	return p
}

// isDigits prüft, ob s nur aus Ziffern besteht.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	// `range` über einen String liefert (Index, rune). Hier interessiert nur die
	// rune `r` (ein Unicode-Zeichen, im Grunde eine Ganzzahl). Zeichen-Literale
	// stehen in einfachen Anführungszeichen: '0', '9'.
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Resolve liefert die Zentroid-Koordinate für (lkz, plz), oder ok=false wenn unbekannt.
//
// GO-EINSTEIGER: Receiver `(g *Geocoder)` ist hier ein POINTER. Bei großen
// Strukturen (die table kann zehntausende Einträge haben) vermeidet das eine
// teure Kopie pro Aufruf. Lesen kann man auch ohne Pointer — hier ist er v. a.
// aus Konsistenz/Effizienz gewählt.
func (g *Geocoder) Resolve(lkz, plz string) (Coord, bool) {
	c := NormCountry(lkz)
	// Map-Zugriff mit "comma ok": Der zweite Wert `ok` ist true, wenn der
	// Schlüssel existiert (sonst bekäme man den Nullwert und wüsste nicht, ob
	// er echt ist). key{...} ist ein struct-Literal als Schlüssel.
	co, ok := g.table[key{c, NormPLZ(c, plz)}]
	return co, ok
}

// Len gibt die Anzahl der geladenen PLZ-Zentroide zurück. `len` auf einer Map =
// Anzahl Einträge.
func (g *Geocoder) Len() int { return len(g.table) }

// New baut einen Geocoder aus einer fertigen Tabelle (praktisch für Tests).
// `&Geocoder{...}` erzeugt die Struct und gibt einen Pointer darauf zurück (`&`
// = "Adresse von"). So entstehen in Go üblicherweise Konstruktor-Funktionen.
func New(table map[key]Coord) *Geocoder { return &Geocoder{table: table} }

// MakeKey erzeugt einen normalisierten Tabellenschlüssel (für Tests/Befüllung).
func MakeKey(country, plz string) key {
	c := NormCountry(country)
	return key{c, NormPLZ(c, plz)}
}

// LoadTable liest eine CSV (Header country,plz,lat,lon) in eine Lookup-Tabelle.
//
// Parameter ist ein `io.Reader` — ein INTERFACE. Egal ob die Daten aus einer
// Datei, dem Netzwerk oder einem String kommen: Hauptsache, das Objekt kann
// Read(). Das macht die Funktion gut testbar (Test gibt einfach einen String-Reader).
func LoadTable(r io.Reader) (map[key]Coord, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // variable Spaltenanzahl zulassen (nicht erzwingen)
	header, err := cr.Read()
	if err != nil {
		return nil, err // nil-Map + Fehler zurück
	}
	idx := columnIndex(header)
	// Fehlt eine erwartete Spalte, ist ihr Index -1. errors.New baut einen
	// einfachen Fehlerwert aus Text.
	if idx.country < 0 || idx.plz < 0 || idx.lat < 0 || idx.lon < 0 {
		return nil, errors.New("CSV-Header muss country,plz,lat,lon enthalten")
	}
	// make(map[...]...) legt eine leere, benutzbare Map an. Ohne make wäre eine
	// Map nil und ein Schreibzugriff würde abstürzen.
	table := make(map[key]Coord)
	// Endlosschleife `for { ... }` — verlassen wird sie unten per break bei EOF.
	for {
		rec, err := cr.Read()
		if err == io.EOF { // EOF = End Of File = Datei zu Ende, regulärer Abbruch
			break
		}
		if err != nil {
			return nil, err
		}
		// Defensive: hat die Zeile genug Spalten? max() = größter benötigter Index.
		if len(rec) <= idx.max() {
			continue
		}
		// Zwei Umwandlungen, zwei Fehlervariablen. Kaputte Zeilen still überspringen.
		lat, err1 := strconv.ParseFloat(strings.TrimSpace(rec[idx.lat]), 64)
		lon, err2 := strconv.ParseFloat(strings.TrimSpace(rec[idx.lon]), 64)
		if err1 != nil || err2 != nil {
			continue
		}
		c := NormCountry(rec[idx.country])
		// In die Map schreiben: Schlüssel = key{...}, Wert = Coord{lat, lon}
		// (Felder in Reihenfolge der struct-Definition, ohne Namen).
		table[key{c, NormPLZ(c, rec[idx.plz])}] = Coord{lat, lon}
	}
	return table, nil
}

// colIdx merkt sich, in welcher Spalte (Index) welches Feld steht.
type colIdx struct{ country, plz, lat, lon int }

// max liefert den größten der vier Indizes.
func (c colIdx) max() int {
	m := c.country
	// Über ein Slice-Literal []int{...} iterieren. `_` ignoriert den Index, `v`
	// ist der Wert.
	for _, v := range []int{c.plz, c.lat, c.lon} {
		if v > m {
			m = v
		}
	}
	return m
}

// columnIndex ermittelt die Spaltenpositionen anhand der Header-Namen
// (Reihenfolge in der CSV ist damit egal).
func columnIndex(header []string) colIdx {
	idx := colIdx{-1, -1, -1, -1} // -1 = "noch nicht gefunden"
	for i, h := range header {    // hier brauchen wir Index UND Wert
		switch strings.ToLower(strings.TrimSpace(h)) {
		case "country":
			idx.country = i
		case "plz":
			idx.plz = i
		case "lat":
			idx.lat = i
		case "lon":
			idx.lon = i
		}
	}
	return idx
}

// Load lädt den Geocoder aus der CSV unter path. Fehlt die Datei, ist (nil, nil)
// das Ergebnis (Feature deaktiviert, kein Fehler) — analog zur Python-Variante.
func Load(path string) (*Geocoder, error) {
	f, err := os.Open(path)
	// errors.Is prüft, ob der Fehler (ggf. tief verschachtelt) einem bekannten
	// Sentinel entspricht — hier "Datei existiert nicht". Robuster als ein
	// String-Vergleich der Fehlermeldung.
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil // bewusst KEIN Fehler: Geocoding ist dann einfach aus
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	table, err := LoadTable(f)
	if err != nil {
		return nil, err
	}
	return &Geocoder{table: table}, nil
}
