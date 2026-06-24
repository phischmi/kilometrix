// Package build kapselt die einmaligen Setup-Schritte: Geocoding-Tabelle bauen
// (nativ in Go) und den OSRM-Graphen bauen (orchestriert die osrm-*-CLI-Tools).
package build

import (
	"archive/zip"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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

	tmp, err := os.CreateTemp("", "geonames-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	if err := download(zipURL, tmp); err != nil {
		return fmt.Errorf("Download fehlgeschlagen: %w", err)
	}
	if err := tmp.Sync(); err != nil {
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

func download(url string, dst io.Writer) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	_, err = io.Copy(dst, resp.Body)
	return err
}

type acc struct {
	sumLat, sumLon float64
	n              int
}

// aggregateZip liest <COUNTRY>.txt aus dem Zip und schreibt die Zentroid-CSV.
// GeoNames-Postal-Format (TSV): country, postal, place, admin1, code1, admin2,
// code2, admin3, code3, lat, lon, accuracy
func aggregateZip(zipPath, country, outPath string) (int, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return 0, err
	}
	defer zr.Close()

	want := strings.ToUpper(country) + ".txt"
	var entry *zip.File
	for _, f := range zr.File {
		if strings.EqualFold(filepath.Base(f.Name), want) {
			entry = f
			break
		}
	}
	if entry == nil {
		return 0, fmt.Errorf("%s nicht im Archiv gefunden", want)
	}
	rc, err := entry.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()

	tr := csv.NewReader(rc)
	tr.Comma = '\t'
	tr.FieldsPerRecord = -1
	tr.LazyQuotes = true

	groups := map[string]*acc{}
	for {
		rec, err := tr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
		if len(rec) < 11 {
			continue
		}
		lat, e1 := strconv.ParseFloat(strings.TrimSpace(rec[9]), 64)
		lon, e2 := strconv.ParseFloat(strings.TrimSpace(rec[10]), 64)
		if e1 != nil || e2 != nil {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(rec[0])) + "\x00" + strings.TrimSpace(rec[1])
		a := groups[key]
		if a == nil {
			a = &acc{}
			groups[key] = a
		}
		a.sumLat += lat
		a.sumLon += lon
		a.n++
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return 0, err
	}
	out, err := os.Create(outPath)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	cw := csv.NewWriter(out)
	defer cw.Flush()
	if err := cw.Write([]string{"country", "plz", "lat", "lon"}); err != nil {
		return 0, err
	}

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a := groups[k]
		c, plz, _ := strings.Cut(k, "\x00")
		lat := a.sumLat / float64(a.n)
		lon := a.sumLon / float64(a.n)
		rec := []string{c, plz, strconv.FormatFloat(round6(lat), 'f', -1, 64), strconv.FormatFloat(round6(lon), 'f', -1, 64)}
		if err := cw.Write(rec); err != nil {
			return 0, err
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return 0, err
	}
	// Explizit world-readable, unabhängig von der umask (NAS laufen oft mit umask 077,
	// sonst kann der non-root App-Container die Datei nicht lesen -> permission denied).
	if err := os.Chmod(outPath, 0o644); err != nil {
		return 0, err
	}
	return len(groups), nil
}

func round6(v float64) float64 {
	const f = 1e6
	if v < 0 {
		return float64(int64(v*f-0.5)) / f
	}
	return float64(int64(v*f+0.5)) / f
}

func logf(log func(string), format string, a ...any) {
	if log != nil {
		log(fmt.Sprintf(format, a...))
	}
}
