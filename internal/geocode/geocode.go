// Package geocode löst LKZ/PLZ-Kombinationen offline zu Zentroid-Koordinaten auf.
// Die Tabelle (country,plz,lat,lon) wird mit dem build-geocode-Subcommand aus dem
// GeoNames-Postal-Datensatz (CC BY 4.0) erzeugt.
package geocode

import (
	"encoding/csv"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
)

// Coord ist eine (lat, lon)-Koordinate.
type Coord struct {
	Lat float64
	Lon float64
}

// Geocoder ist ein In-Memory-Lookup (LKZ, PLZ) → Zentroid.
type Geocoder struct {
	table map[key]Coord
}

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
		p = strings.Repeat("0", 5-len(p)) + p
	}
	return p
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Resolve liefert die Zentroid-Koordinate für (lkz, plz), oder ok=false wenn unbekannt.
func (g *Geocoder) Resolve(lkz, plz string) (Coord, bool) {
	c := NormCountry(lkz)
	co, ok := g.table[key{c, NormPLZ(c, plz)}]
	return co, ok
}

// Len gibt die Anzahl der geladenen PLZ-Zentroide zurück.
func (g *Geocoder) Len() int { return len(g.table) }

// New baut einen Geocoder aus einer fertigen Tabelle (praktisch für Tests).
func New(table map[key]Coord) *Geocoder { return &Geocoder{table: table} }

// MakeKey erzeugt einen normalisierten Tabellenschlüssel (für Tests/Befüllung).
func MakeKey(country, plz string) key {
	c := NormCountry(country)
	return key{c, NormPLZ(c, plz)}
}

// LoadTable liest eine CSV (Header country,plz,lat,lon) in eine Lookup-Tabelle.
func LoadTable(r io.Reader) (map[key]Coord, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err != nil {
		return nil, err
	}
	idx := columnIndex(header)
	if idx.country < 0 || idx.plz < 0 || idx.lat < 0 || idx.lon < 0 {
		return nil, errors.New("CSV-Header muss country,plz,lat,lon enthalten")
	}
	table := make(map[key]Coord)
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(rec) <= idx.max() {
			continue
		}
		lat, err1 := strconv.ParseFloat(strings.TrimSpace(rec[idx.lat]), 64)
		lon, err2 := strconv.ParseFloat(strings.TrimSpace(rec[idx.lon]), 64)
		if err1 != nil || err2 != nil {
			continue
		}
		c := NormCountry(rec[idx.country])
		table[key{c, NormPLZ(c, rec[idx.plz])}] = Coord{lat, lon}
	}
	return table, nil
}

type colIdx struct{ country, plz, lat, lon int }

func (c colIdx) max() int {
	m := c.country
	for _, v := range []int{c.plz, c.lat, c.lon} {
		if v > m {
			m = v
		}
	}
	return m
}

func columnIndex(header []string) colIdx {
	idx := colIdx{-1, -1, -1, -1}
	for i, h := range header {
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
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
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
