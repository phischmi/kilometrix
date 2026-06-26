// Package osrm verwaltet osrm-routed als lokalen Subprozess (Start, Readiness, Stop).
package osrm

import (
	"fmt"
	"net/http"
	"os"
	"os/exec" // externe Programme starten (wie Pythons subprocess)
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Process kapselt einen osrm-routed-Subprozess.
//
// Die ersten Felder (groß geschrieben) werden von außen befüllt (Konfiguration),
// das letzte Feld `cmd` ist privat (klein) und hält intern den laufenden Prozess.
type Process struct {
	Binary    string
	GraphPath string
	Algorithm string
	Host      string
	Port      int
	Verbosity string
	Mmap      bool

	cmd *exec.Cmd // Pointer auf das laufende Kommando; nil = läuft (noch) nicht
}

// BaseURL ist die HTTP-Basis-URL des Prozesses.
func (p *Process) BaseURL() string {
	return fmt.Sprintf("http://%s:%d", p.Host, p.Port)
}

// resolveBin sucht ein Binary: erst PATH, dann neben dem eigenen Executable.
// Auf Windows wird ggf. ".exe" angehängt. Gibt "" zurück, wenn nichts gefunden.
func resolveBin(name string) string {
	// Absoluter Pfad oder expliziter Relative-Pfad mit Separator → direkt prüfen.
	if filepath.IsAbs(name) || strings.ContainsRune(name, filepath.Separator) {
		if _, err := os.Stat(name); err == nil {
			return name
		}
		return ""
	}
	// Auf Windows ".exe" ergänzen, falls noch nicht vorhanden.
	candidates := []string{name}
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		candidates = append(candidates, name+".exe")
	}
	for _, n := range candidates {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
		// Neben dem eigenen Executable suchen (typisch: alles in einem Ordner).
		if exe, err := os.Executable(); err == nil {
			p := filepath.Join(filepath.Dir(exe), n)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// GraphExists prüft, ob der Graph (Basis-Pfad + .*-Dateien) vorhanden ist.
// Freie Funktion (kein Receiver), daher von außen als osrm.GraphExists(...) nutzbar.
func GraphExists(graphPath string) bool {
	// os.Stat fragt Datei-Infos ab. Ist err == nil, existiert die Datei.
	if _, err := os.Stat(graphPath); err == nil {
		return true
	}
	// Sonst per Glob nach "<pfad>.*" suchen (der Graph besteht aus mehreren
	// Dateien wie germany.osrm.edges, .nodes, ...). Den Fehler ignorieren wir (_).
	matches, _ := filepath.Glob(graphPath + ".*")
	return len(matches) > 0
}

// Start startet osrm-routed und wartet, bis er bereit ist.
func (p *Process) Start(readyTimeout time.Duration) error {
	bin := resolveBin(p.Binary)
	if bin == "" {
		return fmt.Errorf("'%s' nicht gefunden — Binary neben kilometrix(.exe) legen oder OSRM_ROUTED_BIN setzen (macOS: brew install osrm-backend)", p.Binary)
	}
	p.Binary = bin // aufgelösten Pfad speichern (exec.Command braucht dann kein PATH-Lookup)
	if !GraphExists(p.GraphPath) {
		return fmt.Errorf("OSRM-Graph nicht gefunden: %s.* — erst mit 'kilometrix build-graph' bauen", p.GraphPath)
	}

	// Kommandozeilen-Argumente als []string aufbauen.
	args := []string{
		"--algorithm", p.Algorithm,
		"--verbosity", p.Verbosity, // WARNING: keine Koordinaten-Logs
		"--ip", p.Host,
		"--port", strconv.Itoa(p.Port), // Itoa = int to ASCII (Zahl -> String)
	}
	if p.Mmap {
		// `append` hängt an ein Slice an und gibt das (ggf. neue) Slice zurück;
		// das Ergebnis MUSS man wieder zuweisen. Mehrere Werte auf einmal möglich.
		// explizit "--mmap true": ohne Wert würde der Graph-Pfad als Flag-Wert gelesen.
		args = append(args, "--mmap", "true")
	}
	args = append(args, p.GraphPath) // Basis-Pfad bleibt letztes Argument

	// exec.Command(name, args...) baut das Kommando. Das `...` "entpackt" das
	// Slice in einzelne Argumente (wie Pythons *args).
	p.cmd = exec.Command(p.Binary, args...)
	p.cmd.Stdout = os.Stderr // osrm-Logs auf stderr durchreichen
	p.cmd.Stderr = os.Stderr
	setProcAttr(p.cmd) // Windows: kein sichtbares Konsolenfenster
	// Start() startet den Prozess und kehrt sofort zurück (nicht-blockierend),
	// anders als Run(), das auf das Ende warten würde.
	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("osrm-routed konnte nicht gestartet werden: %w", err)
	}
	return p.waitReady(readyTimeout)
}

// waitReady pollt den HTTP-Endpunkt, bis osrm-routed antwortet oder das Timeout greift.
func (p *Process) waitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout) // Zeitpunkt, ab dem wir aufgeben
	// Eine triviale Test-Route (gleicher Punkt zweimal) als Lebenszeichen.
	probe := p.BaseURL() + "/route/v1/driving/13.4,52.5;13.4,52.5?overview=false"
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		// Hat sich der Prozess schon wieder beendet? Dann hat der Start versagt.
		if p.exited() {
			return fmt.Errorf("osrm-routed beendete sich beim Start (Exit-Code %d)", p.cmd.ProcessState.ExitCode())
		}
		if resp, err := client.Get(probe); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil // bereit!
			}
		}
		time.Sleep(500 * time.Millisecond) // kurz warten, dann erneut versuchen
	}
	_ = p.Stop() // Timeout: aufräumen
	return fmt.Errorf("osrm-routed wurde nicht innerhalb von %s bereit", timeout)
}

// exited meldet, ob der Prozess bereits beendet ist. ProcessState ist erst nach
// Wait() bzw. nach Prozessende gesetzt — daher die nil-Prüfungen.
func (p *Process) exited() bool {
	return p.cmd != nil && p.cmd.ProcessState != nil && p.cmd.ProcessState.Exited()
}

// Stop beendet den Prozess (SIGTERM, dann Kill).
func (p *Process) Stop() error {
	if p.cmd == nil || p.cmd.Process == nil {
		return nil // nichts zu tun
	}
	_ = sendStop(p.cmd) // SIGINT (Unix) oder Kill (Windows)
	// Wait() blockiert bis zum Prozessende. Wir lagern es in eine Goroutine aus
	// und melden das Ergebnis über einen gepufferten Channel (Kapazität 1).
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	// `select` wartet auf MEHRERE Channel-Ereignisse gleichzeitig und nimmt das
	// erste, das eintritt (wie ein "switch" für Channels).
	select {
	case <-done:
		// Prozess hat sich brav beendet. (Wert aus dem Channel verwerfen.)
	case <-time.After(10 * time.Second):
		// Nach 10s immer noch da -> hart killen und dann auf done warten.
		_ = p.cmd.Process.Kill()
		<-done
	}
	p.cmd = nil
	return nil
}
