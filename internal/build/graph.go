package build

import (
	"fmt"
	"io"
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
	Output io.Writer
}

// BuildGraph führt das einmalige OSRM-Preprocessing aus: PBF laden (falls nötig),
// dann osrm-extract → osrm-partition → osrm-customize. Die osrm-*-CLI-Tools müssen
// im PATH liegen (macOS: 'brew install osrm-backend').
func BuildGraph(opt GraphOptions, log func(string)) error {
	if opt.DataDir == "" {
		opt.DataDir = "data"
	}
	if opt.PBFURL == "" {
		opt.PBFURL = "https://download.geofabrik.de/europe/germany-latest.osm.pbf"
	}
	if opt.ProfilePath == "" {
		opt.ProfilePath = filepath.Join("profiles", "truck.lua")
	}
	if opt.Output == nil {
		opt.Output = os.Stderr
	}

	for _, bin := range []string{"osrm-extract", "osrm-partition", "osrm-customize"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("'%s' nicht im PATH. macOS: 'brew install osrm-backend'", bin)
		}
	}

	if err := os.MkdirAll(opt.DataDir, 0o755); err != nil {
		return err
	}
	// Der Ausgabe-Basisname leitet sich aus dem PBF-Dateinamen ab → germany.osm.pbf,
	// damit extract germany.osrm.* erzeugt.
	pbf := filepath.Join(opt.DataDir, "germany.osm.pbf")
	base := filepath.Join(opt.DataDir, "germany.osrm")

	if _, err := os.Stat(pbf); os.IsNotExist(err) {
		logf(log, "Lade OSM-Daten: %s", opt.PBFURL)
		f, err := os.Create(pbf)
		if err != nil {
			return err
		}
		if err := download(opt.PBFURL, f); err != nil {
			f.Close()
			return fmt.Errorf("PBF-Download fehlgeschlagen: %w", err)
		}
		f.Close()
	}

	// Profil neben das mitgelieferte car.lua kopieren, damit dessen lib/ gefunden wird.
	profile, err := stageProfile(opt.ProfilePath, log)
	if err != nil {
		return err
	}

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
		cmd.Stdout = opt.Output
		cmd.Stderr = opt.Output
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
	dstDir := filepath.Dir(carLua)
	dst := filepath.Join(dstDir, filepath.Base(profilePath))
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
	resolved, err := filepath.EvalSymlinks(bin)
	if err != nil {
		resolved = bin
	}
	candidate := filepath.Join(filepath.Dir(resolved), "..", "share", "osrm", "profiles", "car.lua")
	if _, err := os.Stat(candidate); err != nil {
		return "", err
	}
	return filepath.Clean(candidate), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
