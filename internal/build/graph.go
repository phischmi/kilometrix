package build

import (
	"fmt"
	"io" // io.Writer / io.Copy für Datenströme
	"os"
	"os/exec"
	"path/filepath"
)

// GraphOptions steuert den OSRM-Graph-Bau (MLD-Pipeline).
type GraphOptions struct {
	PBFURL      string // Override; sonst Geofabrik germany-latest
	DataDir     string // Zielverzeichnis (Default: data)
	ProfilePath string // LKW-Profil (Default: profiles/truck.lua)
	// Output-Writer für die Tool-Ausgaben (osrm-extract etc.). nil → stderr.
	// io.Writer ist ein Interface ("etwas, in das man schreiben kann") — so kann
	// der Aufrufer die Ausgabe umlenken (Datei, Puffer, GUI, ...).
	Output io.Writer
}

// BuildGraph führt das einmalige OSRM-Preprocessing aus: PBF laden (falls nötig),
// dann osrm-extract → osrm-partition → osrm-customize. Die osrm-*-CLI-Tools müssen
// im PATH liegen (macOS: 'brew install osrm-backend').
//
// `log func(string)` ist ein Funktions-Parameter: BuildGraph ruft diese Funktion
// auf, um Fortschritt zu melden, ohne zu wissen, wohin (CLI -> println, GUI -> Event).
func BuildGraph(opt GraphOptions, log func(string)) error {
	// Defaults setzen, wo der Aufrufer nichts angegeben hat.
	if opt.DataDir == "" {
		opt.DataDir = "data"
	}
	if opt.PBFURL == "" {
		opt.PBFURL = "https://download.geofabrik.de/europe/germany-latest.osm.pbf"
	}
	if opt.ProfilePath == "" {
		opt.ProfilePath = filepath.Join("profiles", "truck.lua") // OS-gerecht zusammensetzen
	}
	if opt.Output == nil {
		opt.Output = os.Stderr
	}

	// Vorab prüfen, ob alle drei Tools im PATH sind — sonst gleich eine klare
	// Fehlermeldung statt eines kryptischen Abbruchs mittendrin.
	for _, bin := range []string{"osrm-extract", "osrm-partition", "osrm-customize"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("'%s' nicht im PATH. macOS: 'brew install osrm-backend'", bin)
		}
	}

	// MkdirAll legt das Verzeichnis (inkl. Elternpfade) an; existiert es schon, ok.
	// 0o755 ist eine oktale Unix-Berechtigung (rwx r-x r-x).
	if err := os.MkdirAll(opt.DataDir, 0o755); err != nil {
		return err
	}
	// Der Ausgabe-Basisname leitet sich aus dem PBF-Dateinamen ab → germany.osm.pbf,
	// damit extract germany.osrm.* erzeugt.
	pbf := filepath.Join(opt.DataDir, "germany.osm.pbf")
	base := filepath.Join(opt.DataDir, "germany.osrm")

	// PBF nur herunterladen, wenn sie noch nicht da ist (os.IsNotExist prüft das).
	if _, err := os.Stat(pbf); os.IsNotExist(err) {
		logf(log, "Lade OSM-Daten: %s", opt.PBFURL)
		f, err := os.Create(pbf) // Datei zum Schreiben anlegen
		if err != nil {
			return err
		}
		if err := download(opt.PBFURL, f); err != nil {
			f.Close() // bei Fehler trotzdem schließen
			return fmt.Errorf("PBF-Download fehlgeschlagen: %w", err)
		}
		f.Close()
	}

	// Profil neben das mitgelieferte car.lua kopieren, damit dessen lib/ gefunden wird.
	profile, err := stageProfile(opt.ProfilePath, log)
	if err != nil {
		return err
	}

	// Die drei Schritte als Slice anonymer Structs: Name + Argumentliste.
	steps := []struct {
		name string
		args []string
	}{
		{"osrm-extract", []string{"-p", profile, pbf}},
		{"osrm-partition", []string{base}},
		{"osrm-customize", []string{base}},
	}
	for _, s := range steps {
		logf(log, "==> %s", s.name)
		cmd := exec.Command(s.name, s.args...)
		cmd.Stdout = opt.Output // Tool-Ausgabe in den gewünschten Writer leiten
		cmd.Stderr = opt.Output
		// Run() startet das Tool UND wartet auf sein Ende (anders als Start()).
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s fehlgeschlagen: %w", s.name, err)
		}
	}
	logf(log, "Fertig. Graph liegt in %s.* — OSRM_ALGORITHM=MLD setzen.", base)
	return nil
}

// stageProfile kopiert das LKW-Profil neben das von osrm-backend mitgelieferte car.lua
// (dort liegt das benötigte lib/) und liefert den Zielpfad.
func stageProfile(profilePath string, log func(string)) (string, error) {
	carLua, err := locateCarLua()
	if err != nil {
		// Kein car.lua gefunden → Profil direkt verwenden (funktioniert, wenn lib/ daneben liegt).
		logf(log, "Hinweis: car.lua nicht gefunden, nutze Profil direkt: %s", profilePath)
		return profilePath, nil
	}
	dstDir := filepath.Dir(carLua)                           // Verzeichnis von car.lua
	dst := filepath.Join(dstDir, filepath.Base(profilePath)) // Zielpfad = dortiges Verzeichnis + Dateiname
	if err := copyFile(profilePath, dst); err != nil {
		return "", fmt.Errorf("Profil kopieren fehlgeschlagen: %w", err)
	}
	return dst, nil
}

// locateCarLua sucht das von osrm-backend mitgelieferte car.lua relativ zur
// osrm-extract-Binary: <bin>/../share/osrm/profiles/car.lua.
func locateCarLua() (string, error) {
	bin, err := exec.LookPath("osrm-extract")
	if err != nil {
		return "", err
	}
	// EvalSymlinks folgt symbolischen Links (brew installiert oft per Symlink),
	// damit wir den ECHTEN Installationsort finden.
	resolved, err := filepath.EvalSymlinks(bin)
	if err != nil {
		resolved = bin
	}
	candidate := filepath.Join(filepath.Dir(resolved), "..", "share", "osrm", "profiles", "car.lua")
	if _, err := os.Stat(candidate); err != nil {
		return "", err
	}
	return filepath.Clean(candidate), nil // ".."-Teile auflösen/normalisieren
}

// copyFile kopiert eine Datei. Schönes Beispiel für defer + io.Copy.
func copyFile(src, dst string) error {
	in, err := os.Open(src) // Quelle zum Lesen
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst) // Ziel zum Schreiben
	if err != nil {
		return err
	}
	defer out.Close()
	// io.Copy streamt von in nach out (puffert intern, lädt nichts komplett in den RAM).
	_, err = io.Copy(out, in)
	return err
}
