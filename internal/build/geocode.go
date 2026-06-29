// Package build kapselt die einmaligen Setup-Schritte: Geocoding-Tabelle bauen
// (nativ in Go) und den OSRM-Graphen bauen (orchestriert die osrm-*-CLI-Tools).
package build

import (
	"archive/zip"  // Zip-Archive lesen
	"encoding/csv" // CSV/TSV lesen+schreiben
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort" // Slices sortieren (deterministische Ausgabe)
	"strconv"
	"strings"
	"time"
)

// GeocodeOptions steuert den Geocoding-Bau.
type GeocodeOptions struct {
	Country string // ISO-Land, z. B. "DE"
	ZipURL  string // Override; sonst aus Country abgeleitet
	OutPath string // Ziel-CSV, z. B. data/plz_centroids.csv
}

// BuildGeocode lädt die GeoNames-Postal-Daten, mittelt je PLZ den Zentroid und
// schreibt eine kompakte CSV (country,plz,lat,lon). Fortschritt geht an log.
// Quelle: GeoNames (CC BY 4.0).
func BuildGeocode(opt GeocodeOptions, log func(string)) error {
	if opt.Country == "" {
		opt.Country = "DE"
	}
	if opt.OutPath == "" {
		opt.OutPath = filepath.Join("data", "plz_centroids.csv")
	}
	zipURL := opt.ZipURL
	if zipURL == "" {
		zipURL = "https://download.geonames.org/export/zip/" + opt.Country + ".zip"
	}
	logf(log, "Lade GeoNames Postal-Daten: %s", zipURL)

	// Temporäre Datei für das Zip. "geonames-*.zip": der * wird durch Zufallszeichen
	// ersetzt, damit der Name eindeutig ist.
	tmp, err := os.CreateTemp("", "geonames-*.zip")
	if err != nil {
		return err
	}
	// Zwei defer: aufräumen in UMGEKEHRTER Reihenfolge (erst Close, dann Remove).
	// defer-Aufrufe stapeln sich (LIFO) und laufen am Funktionsende.
	defer os.Remove(tmp.Name()) // Datei am Ende löschen
	defer tmp.Close()           // vorher schließen

	if err := download(zipURL, tmp, log); err != nil {
		return fmt.Errorf("Download fehlgeschlagen: %w", err)
	}
	if err := tmp.Sync(); err != nil { // Puffer auf die Platte zwingen, bevor wir wieder lesen
		return err
	}

	logf(log, "==> Zentroide aggregieren")
	count, err := aggregateZip(tmp.Name(), opt.Country, opt.OutPath)
	if err != nil {
		return err
	}
	logf(log, "Fertig. %s: %d PLZ-Zentroide", opt.OutPath, count)
	return nil
}

// download lädt eine URL, schreibt den Body in dst und meldet Fortschritt via log.
func download(url string, dst io.Writer, log func(string)) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	pw := &progressWriter{dst: dst, length: resp.ContentLength, start: time.Now(), lastLog: time.Now(), log: log}
	_, err = io.Copy(pw, resp.Body)
	if err == nil && log != nil {
		elapsed := time.Since(pw.start).Seconds()
		speed := float64(pw.total) / elapsed
		log(fmt.Sprintf("  fertig: %s in %.1fs (%s/s)", fmtBytes(pw.total), elapsed, fmtBytes(int64(speed))))
	}
	return err
}

// progressWriter leitet Bytes weiter und meldet Fortschritt ca. alle 500 ms.
type progressWriter struct {
	dst     io.Writer
	total   int64
	lastN   int64
	length  int64 // -1 = unbekannt
	start   time.Time
	lastLog time.Time
	log     func(string)
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.dst.Write(p)
	pw.total += int64(n)
	if pw.log != nil && time.Since(pw.lastLog) >= 500*time.Millisecond {
		elapsed := time.Since(pw.lastLog).Seconds()
		speed := float64(pw.total-pw.lastN) / elapsed
		var msg string
		if pw.length > 0 {
			pct := int(float64(pw.total) / float64(pw.length) * 100)
			msg = fmt.Sprintf("  %s / %s (%d%%)  %s/s",
				fmtBytes(pw.total), fmtBytes(pw.length), pct, fmtBytes(int64(speed)))
		} else {
			msg = fmt.Sprintf("  %s  %s/s", fmtBytes(pw.total), fmtBytes(int64(speed)))
		}
		pw.log(msg)
		pw.lastLog = time.Now()
		pw.lastN = pw.total
	}
	return n, err
}

// fmtBytes formatiert eine Bytezahl als lesbare Größe (B / KB / MB / GB).
func fmtBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// acc ("accumulator") sammelt pro PLZ die Summen der Koordinaten und die Anzahl,
// um daraus den Mittelwert (Zentroid) zu bilden.
type acc struct {
	sumLat, sumLon float64
	n              int
}

// aggregateZip liest <COUNTRY>.txt aus dem Zip und schreibt die Zentroid-CSV.
// GeoNames-Postal-Format (TSV): country, postal, place, admin1, code1, admin2,
// code2, admin3, code3, lat, lon, accuracy
func aggregateZip(zipPath, country, outPath string) (int, error) {
	zr, err := zip.OpenReader(zipPath) // Zip öffnen
	if err != nil {
		return 0, err
	}
	defer zr.Close()

	// Den richtigen Eintrag im Archiv finden (z. B. "DE.txt"), Groß/Klein egal.
	want := strings.ToUpper(country) + ".txt"
	var entry *zip.File // Pointer, anfangs nil = "noch nicht gefunden"
	for _, f := range zr.File {
		if strings.EqualFold(filepath.Base(f.Name), want) {
			entry = f
			break
		}
	}
	if entry == nil {
		return 0, fmt.Errorf("%s nicht im Archiv gefunden", want)
	}
	rc, err := entry.Open() // den Eintrag als Stream öffnen
	if err != nil {
		return 0, err
	}
	defer rc.Close()

	// Den csv.Reader als TSV (Tab-getrennt) konfigurieren.
	tr := csv.NewReader(rc)
	tr.Comma = '\t'         // Trennzeichen = Tabulator
	tr.FieldsPerRecord = -1 // ungleiche Spaltenzahl erlauben
	tr.LazyQuotes = true    // mit "krummen" Anführungszeichen tolerant sein

	// groups: PLZ-Schlüssel -> Akkumulator (Pointer, damit man ihn in der Map
	// in-place verändern kann).
	groups := map[string]*acc{}
	for {
		rec, err := tr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
		if len(rec) < 11 { // brauchen mindestens bis Spalte 10 (lat) und 11 (lon)
			continue
		}
		lat, e1 := strconv.ParseFloat(strings.TrimSpace(rec[9]), 64)
		lon, e2 := strconv.ParseFloat(strings.TrimSpace(rec[10]), 64)
		if e1 != nil || e2 != nil {
			continue
		}
		// Schlüssel = LAND \x00 PLZ. \x00 (Null-Byte) ist ein sicherer Trenner.
		key := strings.ToUpper(strings.TrimSpace(rec[0])) + "\x00" + strings.TrimSpace(rec[1])
		a := groups[key]
		if a == nil { // erster Eintrag dieser PLZ: neuen Akkumulator anlegen
			a = &acc{}
			groups[key] = a
		}
		// a ist ein Pointer -> diese Änderungen wirken direkt im Map-Eintrag.
		a.sumLat += lat
		a.sumLon += lon
		a.n++
	}

	// Zielverzeichnis sicherstellen und CSV-Datei anlegen.
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return 0, err
	}
	out, err := os.Create(outPath)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	cw := csv.NewWriter(out)
	defer cw.Flush() // gepufferte Daten am Ende rausschreiben
	if err := cw.Write([]string{"country", "plz", "lat", "lon"}); err != nil {
		return 0, err
	}

	// Schlüssel sortieren, damit die Ausgabe deterministisch ist (Maps haben in Go
	// KEINE garantierte Reihenfolge — das ist Absicht der Sprache).
	keys := make([]string, 0, len(groups))
	for k := range groups { // nur die Schlüssel der Map (ohne Wert)
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a := groups[k]
		c, plz, _ := strings.Cut(k, "\x00") // Schlüssel wieder in Land + PLZ zerlegen
		lat := a.sumLat / float64(a.n)      // Mittelwert = Zentroid
		lon := a.sumLon / float64(a.n)
		// FormatFloat mit 'f', Präzision -1 = "so viele Stellen wie nötig".
		rec := []string{c, plz, strconv.FormatFloat(round6(lat), 'f', -1, 64), strconv.FormatFloat(round6(lon), 'f', -1, 64)}
		if err := cw.Write(rec); err != nil {
			return 0, err
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil { // beim Schreiben aufgetretene Fehler prüfen
		return 0, err
	}
	// Explizit world-readable, unabhängig von der umask (Server laufen oft mit umask 077,
	// sonst kann der non-root App-Container die Datei nicht lesen -> permission denied).
	if err := os.Chmod(outPath, 0o644); err != nil {
		return 0, err
	}
	return len(groups), nil
}

// round6 rundet auf 6 Nachkommastellen (kaufmännisch, ohne math-Import).
// Für Koordinaten reichen 6 Stellen (~0,1 m Genauigkeit) locker.
func round6(v float64) float64 {
	const f = 1e6
	if v < 0 {
		// int64(...) schneidet Nachkommastellen ab (Richtung 0). Das -0.5 sorgt fürs
		// korrekte Runden bei negativen Werten.
		return float64(int64(v*f-0.5)) / f
	}
	return float64(int64(v*f+0.5)) / f
}

// logf ist ein kleiner Formatierungs-Helfer: ruft die übergebene log-Funktion mit
// einem fertig formatierten String auf — aber nur, wenn log != nil ist.
// `a ...any` ist ein "variadischer" Parameter: beliebig viele Argumente (wie *args).
func logf(log func(string), format string, a ...any) {
	if log != nil {
		log(fmt.Sprintf(format, a...)) // a... entpackt das Slice wieder in Einzelargumente
	}
}
