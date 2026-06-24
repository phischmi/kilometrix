// Package osrm verwaltet osrm-routed als lokalen Subprozess (Start, Readiness, Stop).
package osrm

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// Process kapselt einen osrm-routed-Subprozess.
type Process struct {
	Binary    string
	GraphPath string
	Algorithm string
	Host      string
	Port      int
	Verbosity string
	Mmap      bool

	cmd *exec.Cmd
}

// BaseURL ist die HTTP-Basis-URL des Prozesses.
func (p *Process) BaseURL() string {
	return fmt.Sprintf("http://%s:%d", p.Host, p.Port)
}

// GraphExists prüft, ob der Graph (Basis-Pfad + .*-Dateien) vorhanden ist.
func GraphExists(graphPath string) bool {
	if _, err := os.Stat(graphPath); err == nil {
		return true
	}
	matches, _ := filepath.Glob(graphPath + ".*")
	return len(matches) > 0
}

// Start startet osrm-routed und wartet, bis er bereit ist.
func (p *Process) Start(readyTimeout time.Duration) error {
	if _, err := exec.LookPath(p.Binary); err != nil {
		if _, statErr := os.Stat(p.Binary); statErr != nil {
			return fmt.Errorf("'%s' nicht gefunden. Auf macOS: 'brew install osrm-backend'", p.Binary)
		}
	}
	if !GraphExists(p.GraphPath) {
		return fmt.Errorf("OSRM-Graph nicht gefunden: %s.* — erst mit 'kilometrix build-graph' bauen", p.GraphPath)
	}

	args := []string{
		"--algorithm", p.Algorithm,
		"--verbosity", p.Verbosity, // WARNING: keine Koordinaten-Logs
		"--ip", p.Host,
		"--port", strconv.Itoa(p.Port),
	}
	if p.Mmap {
		// explizit "--mmap true": ohne Wert würde der Graph-Pfad als Flag-Wert gelesen.
		args = append(args, "--mmap", "true")
	}
	args = append(args, p.GraphPath) // Basis-Pfad bleibt letztes Argument

	p.cmd = exec.Command(p.Binary, args...)
	p.cmd.Stdout = os.Stderr // osrm-Logs auf stderr durchreichen
	p.cmd.Stderr = os.Stderr
	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("osrm-routed konnte nicht gestartet werden: %w", err)
	}
	return p.waitReady(readyTimeout)
}

func (p *Process) waitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	probe := p.BaseURL() + "/route/v1/driving/13.4,52.5;13.4,52.5?overview=false"
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		if p.exited() {
			return fmt.Errorf("osrm-routed beendete sich beim Start (Exit-Code %d)", p.cmd.ProcessState.ExitCode())
		}
		if resp, err := client.Get(probe); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	_ = p.Stop()
	return fmt.Errorf("osrm-routed wurde nicht innerhalb von %s bereit", timeout)
}

func (p *Process) exited() bool {
	return p.cmd != nil && p.cmd.ProcessState != nil && p.cmd.ProcessState.Exited()
}

// Stop beendet den Prozess (SIGTERM, dann Kill).
func (p *Process) Stop() error {
	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	_ = p.cmd.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = p.cmd.Process.Kill()
		<-done
	}
	p.cmd = nil
	return nil
}
