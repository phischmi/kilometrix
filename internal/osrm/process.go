// Package osrm verwaltet osrm-routed als lokalen Subprozess (Start, Readiness, Stop).
package osrm

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec" // externe Programme starten (wie Pythons subprocess)
	"path/filepath"
	"strconv"
	"time"

	"github.com/phischmi/kilometrix/internal/binutil"
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

	cmd    *exec.Cmd    // Pointer auf das laufende Kommando; nil = läuft (noch) nicht
	waitCh chan error    // empfängt das Ergebnis von cmd.Wait(); gepuffert (Kapazität 1)
}

// BaseURL ist die HTTP-Basis-URL des Prozesses.
func (p *Process) BaseURL() string {
	return fmt.Sprintf("http://%s:%d", p.Host, p.Port)
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
	bin := binutil.Resolve(p.Binary)
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
		args = append(args, "--mmap", "true")
	}
	args = append(args, p.GraphPath) // Basis-Pfad bleibt letztes Argument

	// exec.Command(name, args...) baut das Kommando. Das `...` "entpackt" das
	// Slice in einzelne Argumente (wie Pythons *args).
	p.cmd = exec.Command(p.Binary, args...)
	p.cmd.Stdout = os.Stderr // osrm-Logs auf stderr durchreichen
	p.cmd.Stderr = os.Stderr
	binutil.HideWindow(p.cmd) // Windows: kein sichtbares Konsolenfenster
	// Start() startet den Prozess und kehrt sofort zurück (nicht-blockierend),
	// anders als Run(), das auf das Ende warten würde.
	log.Printf("osrm-routed Befehl: %s", p.cmd.String())
	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("osrm-routed konnte nicht gestartet werden: %w", err)
	}
	// Wait() genau einmal starten; Channel gepuffert, damit die Goroutine nicht
	// blockiert wenn niemand liest (z. B. nach erfolgreichem Start).
	p.waitCh = make(chan error, 1)
	go func() { p.waitCh <- p.cmd.Wait() }()
	return p.waitReady(readyTimeout)
}

// waitReady pollt den HTTP-Endpunkt, bis osrm-routed antwortet oder das Timeout greift.
func (p *Process) waitReady(timeout time.Duration) error {
	probe := p.BaseURL() + "/route/v1/driving/13.4,52.5;13.4,52.5?overview=false"
	client := &http.Client{Timeout: 2 * time.Second}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()
	start := time.Now()

	for {
		select {
		case err := <-p.waitCh:
			// Prozess hat sich beendet bevor er bereit war — Fehler sofort melden.
			code := -1
			if p.cmd.ProcessState != nil {
				code = p.cmd.ProcessState.ExitCode()
			}
			// waitCh wieder befüllen, damit Stop() ihn lesen kann (gepuffert → kein Deadlock).
			p.waitCh <- err
			if err != nil {
				return fmt.Errorf("osrm-routed beendete sich beim Start (Exit-Code %d): %w", code, err)
			}
			return fmt.Errorf("osrm-routed beendete sich beim Start (Exit-Code %d)", code)
		case <-timer.C:
			_ = p.cmd.Process.Kill()
			<-p.waitCh
			return fmt.Errorf("osrm-routed wurde nicht innerhalb von %s bereit", timeout)
		case <-heartbeat.C:
			log.Printf("osrm-routed lädt noch… (%.0f s vergangen, warte bis %.0f s)", time.Since(start).Seconds(), timeout.Seconds())
		case <-tick.C:
			resp, err := client.Get(probe)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					log.Printf("osrm-routed bereit (%.0f s)", time.Since(start).Seconds())
					return nil
				}
			}
		}
	}
}

// Stop beendet den Prozess (SIGTERM/Kill) und wartet auf sein Ende.
func (p *Process) Stop() error {
	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	_ = sendStop(p.cmd) // SIGINT (Unix) oder Kill (Windows)
	// waitCh wurde in Start() gestartet — darüber auf das Prozessende warten,
	// kein zweites Wait() starten (das wäre ein Fehler).
	select {
	case <-p.waitCh:
	case <-time.After(30 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.waitCh
	}
	p.cmd = nil
	return nil
}
