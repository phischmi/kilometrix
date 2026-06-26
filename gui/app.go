// Package main (GUI) treibt das kilometrix-Backend-Binary über seine Subcommands an
// und streamt deren Ausgabe live an das Frontend (Wails-Events). Die GUI ist bewusst
// vom Backend entkoppelt (eigenes Modul, nicht im Docker-Image).
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime" // ACHTUNG: das Standard-runtime-Paket (GOOS etc.), nicht unser internal/runtime
	"strings"
	"sync" // Mutex zum Schutz gemeinsamer Felder
	"time"

	// Import mit ALIAS: das wails-runtime-Paket bekommt den Namen `wruntime`, um
	// nicht mit dem Standard-`runtime` oben zu kollidieren. Syntax: alias "pfad".
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App ist der an das Frontend gebundene Zustand.
//
// Methoden auf *App, die exportiert sind (Großbuchstabe), ruft das TS/JS-Frontend
// direkt auf — Wails generiert dafür JavaScript-Bindings.
type App struct {
	ctx     context.Context // von Wails beim Start übergeben (für Events)
	bin     string          // Pfad zum kilometrix-Binary
	workDir string          // Arbeitsverzeichnis (mit data/ + profiles/)

	// GO-EINSTEIGER: Ein Mutex ("mutual exclusion") schützt Felder, auf die
	// MEHRERE Goroutinen zugreifen. Wer mu.Lock() hält, darf serve/busy ändern;
	// alle anderen warten. Ohne das gäbe es "data races".
	mu     sync.Mutex
	serve  *exec.Cmd // laufender 'serve'-Prozess (oder nil)
	busy   bool      // ein Build läuft
	client *http.Client
}

// NewApp erzeugt die App und löst Binary-Pfad + Arbeitsverzeichnis auf.
func NewApp() *App {
	workDir := resolveWorkDir()
	return &App{
		bin:     resolveBinary(workDir),
		workDir: workDir,
		// localhost-Selbstsignatur akzeptieren (nur für den /health-Poll der GUI).
		// InsecureSkipVerify schaltet die Zertifikatsprüfung ab — hier vertretbar,
		// weil es nur localhost ist; im echten Netz wäre das gefährlich.
		client: &http.Client{
			Timeout:   3 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		},
	}
}

// startup wird von Wails beim Start aufgerufen und hinterlegt den Context.
func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// resolveBinary sucht das kilometrix-Binary: ENV → neben der GUI → Arbeitsverzeichnis → PATH.
func resolveBinary(workDir string) string {
	if v := os.Getenv("KILOMETRIX_BIN"); v != "" {
		return v
	}
	name := "kilometrix"
	// runtime.GOOS ist eine Konstante mit dem Ziel-Betriebssystem ("windows", "darwin", ...).
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	candidates := []string{}
	// os.Executable() liefert den Pfad zur laufenden GUI-Binary -> daneben suchen.
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), name))
	}
	candidates = append(candidates, filepath.Join(workDir, name))
	for _, c := range candidates {
		if abs, err := filepath.Abs(c); err == nil && fileExists(abs) {
			return abs
		}
	}
	// Zuletzt im PATH suchen.
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return name // Fallback: einfach den Namen (Aufruf schlägt dann ggf. fehl)
}

// resolveWorkDir wählt das Projektverzeichnis (mit data/ + profiles/).
func resolveWorkDir() string {
	if v := os.Getenv("KILOMETRIX_WORKDIR"); v != "" {
		return v
	}
	if dirExists("data") || dirExists("profiles") {
		return "."
	}
	if dirExists(filepath.Join("..", "data")) || dirExists(filepath.Join("..", "profiles")) {
		return ".."
	}
	return "."
}

// Env beschreibt die GUI-Umgebung fürs Frontend (wird als JSON gebunden).
type Env struct {
	Binary  string `json:"binary"`
	WorkDir string `json:"workDir"`
	BinOK   bool   `json:"binOK"`
}

// GetEnv liefert Binary-/Arbeitsverzeichnis-Status (für die Statusanzeige).
func (a *App) GetEnv() Env {
	_, err := exec.LookPath(a.bin)
	ok := err == nil || fileExists(a.bin)
	return Env{Binary: a.bin, WorkDir: a.workDir, BinOK: ok}
}

// Config gibt die aufgelöste Backend-Konfiguration zurück (via 'kilometrix config').
// Rückgabe (map, error) — die map wird im Frontend zu einem JS-Objekt.
func (a *App) Config() (map[string]any, error) {
	out, err := a.run("config")
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// Health beschreibt den Zustand des laufenden Backends.
type Health struct {
	Online       bool `json:"online"`
	EngineReady  bool `json:"engineReady"`
	GeocodeReady bool `json:"geocodeReady"`
	AuthRequired bool `json:"authRequired"`
}

// Health fragt https://127.0.0.1:<port>/health ab (Selbstsignatur akzeptiert).
func (a *App) Health(port int) Health {
	if port == 0 {
		port = 8443
	}
	resp, err := a.client.Get(fmt.Sprintf("https://127.0.0.1:%d/health", port))
	if err != nil {
		return Health{} // Nullwert: alle Felder false -> "offline"
	}
	defer resp.Body.Close()
	// Eine lokale, anonyme Struct nur zum Dekodieren der Antwortfelder.
	var h struct {
		EngineReady  bool `json:"engine_ready"`
		GeocodeReady bool `json:"geocode_ready"`
		AuthRequired bool `json:"auth_required"`
	}
	if json.NewDecoder(resp.Body).Decode(&h) != nil {
		return Health{Online: true} // erreichbar, aber Body unlesbar
	}
	return Health{Online: true, EngineReady: h.EngineReady, GeocodeReady: h.GeocodeReady, AuthRequired: h.AuthRequired}
}

// ServerRunning meldet, ob der serve-Prozess läuft.
// Lock/Unlock schützen den Zugriff auf a.serve gegen parallele Goroutinen.
func (a *App) ServerRunning() bool {
	a.mu.Lock()
	defer a.mu.Unlock() // defer entsperrt zuverlässig am Funktionsende
	return a.serve != nil
}

// StartServer startet 'kilometrix serve' und streamt die Logs als Event "server:log".
func (a *App) StartServer() error {
	a.mu.Lock()
	if a.serve != nil {
		a.mu.Unlock() // VOR dem return entsperren (kein defer, weil wir früh raus wollen)
		return fmt.Errorf("Server läuft bereits")
	}
	cmd := a.command("serve")
	a.serve = cmd
	a.mu.Unlock()

	if err := a.stream(cmd, "server:log"); err != nil {
		a.mu.Lock()
		a.serve = nil
		a.mu.Unlock()
		return err
	}
	// Eine Goroutine wartet im Hintergrund auf das Prozessende und räumt dann auf.
	go func() {
		_ = cmd.Wait() // blockiert, bis serve sich beendet
		a.mu.Lock()
		a.serve = nil
		a.mu.Unlock()
		a.emit("server:stopped", nil) // Frontend benachrichtigen
	}()
	a.emit("server:started", nil)
	return nil
}

// StopServer beendet den serve-Prozess.
func (a *App) StopServer() error {
	a.mu.Lock()
	cmd := a.serve // unter Lock kopieren, dann sofort wieder freigeben
	a.mu.Unlock()
	if cmd == nil {
		return nil
	}
	return sendStop(cmd) // SIGINT (Unix) oder taskkill /T (Windows)
}

// BuildGeocode startet 'build-geocode' und streamt Logs als "build:log".
func (a *App) BuildGeocode(country string) error {
	args := []string{"build-geocode"}
	if country != "" {
		args = append(args, "--country", country)
	}
	return a.runBuild(args)
}

// BuildGraph startet 'build-graph' und streamt Logs als "build:log".
func (a *App) BuildGraph() error {
	return a.runBuild([]string{"build-graph"})
}

// runBuild startet einen Build-Subcommand (nur einer gleichzeitig dank busy-Flag).
func (a *App) runBuild(args []string) error {
	a.mu.Lock()
	if a.busy {
		a.mu.Unlock()
		return fmt.Errorf("ein Build läuft bereits")
	}
	a.busy = true
	a.mu.Unlock()

	cmd := a.command(args...)
	if err := a.stream(cmd, "build:log"); err != nil {
		a.setBusy(false)
		return err
	}
	go func() {
		err := cmd.Wait()
		a.setBusy(false)
		// Frontend über Erfolg/Fehler informieren (map -> JS-Objekt).
		if err != nil {
			a.emit("build:done", map[string]any{"ok": false, "error": err.Error()})
		} else {
			a.emit("build:done", map[string]any{"ok": true})
		}
	}()
	return nil
}

// setBusy ist ein kleiner thread-sicherer Setter für das busy-Flag.
func (a *App) setBusy(b bool) {
	a.mu.Lock()
	a.busy = b
	a.mu.Unlock()
}

// CreateToken erzeugt ein Zugangstoken (benötigt AUTH_SECRET in .env).
func (a *App) CreateToken(name string, days float64) (string, error) {
	if name == "" {
		return "", fmt.Errorf("Name erforderlich")
	}
	cmd := a.command("token", "create", "--name", name, "--days", fmt.Sprintf("%g", days))
	// stderr in einen Puffer umleiten, um eine evtl. Fehlermeldung lesbar zu machen.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr // bytes.Buffer erfüllt io.Writer -> direkt zuweisbar
	out, err := cmd.Output() // führt aus und gibt stdout zurück
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%s", msg)
		}
		return "", err
	}
	// Erste Zeile = Token (der Erfolgs-Hinweis geht auf stderr).
	tok, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(tok), nil
}

// command baut ein exec.Cmd im Arbeitsverzeichnis.
func (a *App) command(args ...string) *exec.Cmd {
	cmd := exec.Command(a.bin, args...)
	cmd.Dir = a.workDir // Arbeitsverzeichnis des Kindprozesses setzen
	setProcAttr(cmd)    // Windows: kein sichtbares Konsolenfenster
	return cmd
}

// run führt einen Subcommand aus und liefert stdout (für kurze Ausgaben).
func (a *App) run(args ...string) ([]byte, error) {
	return a.command(args...).Output()
}

// stream startet cmd und leitet stdout+stderr zeilenweise als Event an das Frontend.
func (a *App) stream(cmd *exec.Cmd, event string) error {
	// StdoutPipe liefert einen Reader, aus dem wir die Ausgabe des Prozesses lesen.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout // gemeinsamer Strom: stderr → stdout-Pipe
	if err := cmd.Start(); err != nil { // nicht-blockierend starten
		return err
	}
	go a.pump(stdout, event) // im Hintergrund die Ausgabe weiterpumpen
	return nil
}

// pump liest r zeilenweise und schickt jede Zeile als Event ans Frontend.
func (a *App) pump(r io.Reader, event string) {
	sc := bufio.NewScanner(r)
	// Größeren Puffer erlauben (bis 1 MB pro Zeile), falls eine Logzeile lang wird.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		a.emit(event, sc.Text())
	}
}

// emit schickt ein Wails-Event ans Frontend (nur, wenn der Context schon da ist).
func (a *App) emit(event string, data any) {
	if a.ctx != nil {
		wruntime.EventsEmit(a.ctx, event, data)
	}
}

// fileExists / dirExists: kleine os.Stat-Helfer.
func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
