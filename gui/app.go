// Package main (GUI) treibt das kilometrix-Backend-Binary über seine Subcommands an
// und streamt deren Ausgabe live an das Frontend (Wails-Events). Die GUI ist bewusst
// vom Backend entkoppelt (eigenes Modul, nicht im Docker-Image).
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App ist der an das Frontend gebundene Zustand.
type App struct {
	ctx     context.Context
	bin     string
	workDir string

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
		client: &http.Client{
			Timeout:   3 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		},
	}
}

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// resolveBinary sucht das kilometrix-Binary: ENV → neben der GUI → Arbeitsverzeichnis → PATH.
func resolveBinary(workDir string) string {
	if v := os.Getenv("KILOMETRIX_BIN"); v != "" {
		return v
	}
	name := "kilometrix"
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), name))
	}
	candidates = append(candidates, filepath.Join(workDir, name))
	for _, c := range candidates {
		if abs, err := filepath.Abs(c); err == nil && fileExists(abs) {
			return abs
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return name
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

// Env beschreibt die GUI-Umgebung fürs Frontend.
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
		return Health{}
	}
	defer resp.Body.Close()
	var h struct {
		EngineReady  bool `json:"engine_ready"`
		GeocodeReady bool `json:"geocode_ready"`
		AuthRequired bool `json:"auth_required"`
	}
	if json.NewDecoder(resp.Body).Decode(&h) != nil {
		return Health{Online: true}
	}
	return Health{Online: true, EngineReady: h.EngineReady, GeocodeReady: h.GeocodeReady, AuthRequired: h.AuthRequired}
}

// ServerRunning meldet, ob der serve-Prozess läuft.
func (a *App) ServerRunning() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.serve != nil
}

// StartServer startet 'kilometrix serve' und streamt die Logs als Event "server:log".
func (a *App) StartServer() error {
	a.mu.Lock()
	if a.serve != nil {
		a.mu.Unlock()
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
	go func() {
		_ = cmd.Wait()
		a.mu.Lock()
		a.serve = nil
		a.mu.Unlock()
		a.emit("server:stopped", nil)
	}()
	a.emit("server:started", nil)
	return nil
}

// StopServer beendet den serve-Prozess.
func (a *App) StopServer() error {
	a.mu.Lock()
	cmd := a.serve
	a.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = cmd.Process.Signal(os.Interrupt)
	return nil
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
		if err != nil {
			a.emit("build:done", map[string]any{"ok": false, "error": err.Error()})
		} else {
			a.emit("build:done", map[string]any{"ok": true})
		}
	}()
	return nil
}

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
	out, err := a.run("token", "create", "--name", name, "--days", fmt.Sprintf("%g", days))
	if err != nil {
		return "", err
	}
	// Erste Zeile = Token (der Hinweis geht auf stderr).
	tok := string(out)
	for i, c := range tok {
		if c == '\n' {
			tok = tok[:i]
			break
		}
	}
	return tok, nil
}

// command baut ein exec.Cmd im Arbeitsverzeichnis.
func (a *App) command(args ...string) *exec.Cmd {
	cmd := exec.Command(a.bin, args...)
	cmd.Dir = a.workDir
	return cmd
}

// run führt einen Subcommand aus und liefert stdout (für kurze Ausgaben).
func (a *App) run(args ...string) ([]byte, error) {
	return a.command(args...).Output()
}

// stream startet cmd und leitet stdout+stderr zeilenweise als Event an das Frontend.
func (a *App) stream(cmd *exec.Cmd, event string) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout // gemeinsamer Strom: stderr → stdout-Pipe
	if err := cmd.Start(); err != nil {
		return err
	}
	go a.pump(stdout, event)
	return nil
}

func (a *App) pump(r io.Reader, event string) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		a.emit(event, sc.Text())
	}
}

func (a *App) emit(event string, data any) {
	if a.ctx != nil {
		wruntime.EventsEmit(a.ctx, event, data)
	}
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
