package build

import (
	"fmt"
	"io" // io.Writer / io.Copy für Datenströme
	"os"
	"os/exec"
	"path/filepath"

	"github.com/phischmi/kilometrix/internal/binutil"
)

// GraphOptions steuert den OSRM-Graph-Bau (MLD-Pipeline).
type GraphOptions struct {
	PBFURL      string // Override; sonst Geofabrik germany-latest
	DataDir     string // Zielverzeichnis (Default: data)
	ProfilePath string // LKW-Profil (Default: profiles/truck.lua)
	// Output-Writer für die Tool-Ausgaben (osrm-extract etc.). nil → stderr.
	// io.Writer ist ein Interface ("etwas, in das man schreiben kann") — so kann
	// der Aufrufer die Ausgabe umlenken (Datei, Puffer, ...).
	Output io.Writer
}

// BuildGraph führt das einmalige OSRM-Preprocessing aus: PBF laden (falls nötig),
// dann osrm-extract → osrm-partition → osrm-customize. Die osrm-*-CLI-Tools müssen
// im PATH liegen (macOS: 'brew install osrm-backend').
//
// `log func(string)` ist ein Funktions-Parameter: BuildGraph ruft diese Funktion
// auf, um Fortschritt zu melden, ohne zu wissen, wohin (CLI -> println, Log -> Datei).
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

	// Vorab alle drei Tools auflösen (PATH + neben eigenem Executable + .exe auf Windows).
	resolved := map[string]string{}
	for _, bin := range []string{"osrm-extract", "osrm-partition", "osrm-customize"} {
		p := binutil.Resolve(bin)
		if p == "" {
			return fmt.Errorf("'%s' nicht gefunden — Binary neben kilometrix(.exe) legen oder in den PATH aufnehmen (macOS: brew install osrm-backend)", bin)
		}
		resolved[bin] = p
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
		if err := download(opt.PBFURL, f, log); err != nil {
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
		cmd := exec.Command(resolved[s.name], s.args...)
		cmd.Stdout = opt.Output // Tool-Ausgabe in den gewünschten Writer leiten
		cmd.Stderr = opt.Output
		binutil.HideWindow(cmd) // Windows: kein sichtbares Konsolenfenster
		// Run() startet das Tool UND wartet auf sein Ende (anders als Start()).
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s fehlgeschlagen: %w", s.name, err)
		}
	}
	logf(log, "Fertig. Graph liegt in %s.* — OSRM_ALGORITHM=MLD setzen.", base)
	return nil
}

// stageProfile stellt sicher, dass lib/ neben dem Profil liegt, und gibt dessen Pfad zurück.
// Liegt lib/ bereits neben dem Profil, ist nichts zu tun.
// Andernfalls wird lib/ aus der OSRM-Installation (neben car.lua) dorthin kopiert.
func stageProfile(profilePath string, log func(string)) (string, error) {
	profileDir := filepath.Dir(profilePath)
	libDst := filepath.Join(profileDir, "lib")

	// lib/ schon vorhanden → direkt verwenden.
	if _, err := os.Stat(libDst); err == nil {
		return profilePath, nil
	}

	// lib/ aus der OSRM-Installation kopieren.
	carLua, err := locateCarLua()
	if err != nil {
		return "", fmt.Errorf(
			"OSRM-Lua-Bibliothek (lib/) nicht gefunden.\n"+
				"Kopiere den Ordner profiles/lib/ aus dem OSRM-Quellcode nach %s\\lib\\\n"+
				"Quelle: https://github.com/Project-OSRM/osrm-backend/tree/master/profiles/lib",
			profileDir,
		)
	}
	libSrc := filepath.Join(filepath.Dir(carLua), "lib")
	if _, err := os.Stat(libSrc); err != nil {
		return "", fmt.Errorf("lib/ nicht neben car.lua gefunden: %s", libSrc)
	}
	logf(log, "Kopiere OSRM lib/ nach %s", libDst)
	if err := copyDir(libSrc, libDst); err != nil {
		return "", fmt.Errorf("lib/ kopieren fehlgeschlagen: %w", err)
	}
	return profilePath, nil
}

// locateCarLua sucht das von osrm-backend mitgelieferte car.lua relativ zur
// osrm-extract-Binary: <bin>/../share/osrm/profiles/car.lua.
func locateCarLua() (string, error) {
	bin := binutil.Resolve("osrm-extract")
	if bin == "" {
		return "", fmt.Errorf("osrm-extract nicht gefunden")
	}
	resolved, err := filepath.EvalSymlinks(bin)
	if err != nil {
		resolved = bin
	}
	binDir := filepath.Dir(resolved)
	// Mögliche Speicherorte von car.lua je nach Plattform/Installationsart:
	//   macOS/Linux (brew):  <bin>/../share/osrm/profiles/car.lua
	//   Windows Release:     <bin>/profiles/car.lua
	candidates := []string{
		filepath.Join(binDir, "..", "share", "osrm", "profiles", "car.lua"),
		filepath.Join(binDir, "profiles", "car.lua"),
	}
	for _, c := range candidates {
		clean := filepath.Clean(c)
		if _, err := os.Stat(clean); err == nil {
			return clean, nil
		}
	}
	return "", fmt.Errorf("car.lua nicht gefunden (weder in ../share/osrm/profiles/ noch in profiles/ neben osrm-extract)")
}

// copyDir kopiert ein Verzeichnis flach (eine Ebene, keine Rekursion — lib/ hat keine Unterordner).
func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue // lib/ hat keine Unterverzeichnisse
		}
		if err := copyFile(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
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
