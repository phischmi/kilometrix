// Package runtime verdrahtet die Komponenten zum laufenden Backend: Geocoder laden,
// osrm-routed ggf. starten, Engine bauen, HTTP(S) bedienen und sauber herunterfahren.
package runtime

import (
	"context" // Steuerung von Abbruch/Timeouts (z. B. beim Shutdown)
	"errors"  // errors.Is zum Fehlervergleich
	"fmt"
	"log" // einfache Logausgaben mit Zeitstempel
	"net/http"
	"os"
	"os/signal" // auf SIGINT/SIGTERM lauschen
	"syscall"   // syscall.SIGTERM
	"time"

	"github.com/phischmi/kilometrix/internal/config"
	"github.com/phischmi/kilometrix/internal/geocode"
	"github.com/phischmi/kilometrix/internal/osrm"
	"github.com/phischmi/kilometrix/internal/routing"
	"github.com/phischmi/kilometrix/internal/server"
	"github.com/phischmi/kilometrix/internal/tlscert"
)

// ServeOptions steuert das Serve-Verhalten (CLI-Flags überschreiben die Defaults).
type ServeOptions struct {
	Host    string
	Port    int
	TLS     bool   // true = HTTPS mit Zertifikat aus CertDir (lokal); false = HTTP (hinter Proxy)
	CertDir string // Verzeichnis für localhost.pem/-key.pem
}

// Serve startet das Backend und blockiert bis SIGINT/SIGTERM.
// Hier "verdrahtet" sich das ganze Programm: jede Komponente wird gebaut und der
// nächsten übergeben (Dependency Injection ganz ohne Framework).
func Serve(opt ServeOptions) error {
	settings := config.Load()
	// Leere Optionen mit den Konfig-Defaults füllen. "" und 0 sind die Nullwerte,
	// an denen wir "nicht gesetzt" erkennen.
	if opt.Host == "" {
		opt.Host = settings.AddinHost
	}
	if opt.Port == 0 {
		opt.Port = settings.AddinPort
	}
	if opt.CertDir == "" {
		opt.CertDir = "certs"
	}

	// osrm-routed ggf. als Subprozess starten (lokaler Betrieb).
	// `var proc *osrm.Process` deklariert einen Pointer ohne Initialwert -> nil.
	var proc *osrm.Process
	engineErr := ""
	if settings.ManageOSRMRouted {
		// Struct-Literal mit Pointer (&...) -> proc zeigt auf das neue Process-Objekt.
		proc = &osrm.Process{
			Binary:    settings.OSRMRoutedBin,
			GraphPath: settings.OSRMGraphPath,
			Algorithm: settings.OSRMAlgorithm,
			Host:      settings.OSRMRoutedHost,
			Port:      settings.OSRMRoutedPort,
			Verbosity: settings.OSRMRoutedVerbosity,
			Mmap:      settings.OSRMRoutedMmap,
		}
		readyTimeout := time.Duration(settings.OSRMRoutedReadyTimeout) * time.Second
		if readyTimeout <= 0 {
			readyTimeout = 5 * time.Minute
		}
		log.Printf("starte osrm-routed (%s), Timeout %s…", settings.OSRMGraphPath, readyTimeout)
		if err := proc.Start(readyTimeout); err != nil {
			// Start fehlgeschlagen: Fehler merken, aber Backend trotzdem starten
			// (es meldet dann bei /route-batch 503 statt komplett abzustürzen).
			engineErr = err.Error()
			log.Printf("WARN: osrm-routed nicht gestartet: %v", err)
			proc = nil
		}
	}

	// engine ist vom Interface-Typ routing.Engine. Nur wenn osrm läuft, bekommt es
	// eine echte Implementierung; sonst bleibt es nil.
	var engine routing.Engine
	if engineErr == "" {
		engine = routing.NewHTTPEngine(settings.RoutedBaseURL(), settings.SnapLimitM)
	}

	// Geocoder laden (optional). Fehlt die Datei, ist geo == nil und Geocoding aus.
	geo, err := geocode.Load(settings.GeocodePath)
	if err != nil {
		log.Printf("WARN: Geocoder nicht geladen: %v", err)
		geo = nil
	}
	if geo != nil {
		log.Printf("Geocoder geladen: %d PLZ-Zentroide", geo.Len())
	}

	// Server bauen und in einen Standard-http.Server stecken.
	srv := server.New(settings, engine, engineErr, geo)
	addr := fmt.Sprintf("%s:%d", opt.Host, opt.Port)
	httpSrv := &http.Server{Addr: addr, Handler: srv.Handler()}

	// Sauberes Herunterfahren: erst HTTP-Server, dann osrm-routed.
	// errCh transportiert einen evtl. Server-Startfehler aus der Goroutine heraus.
	errCh := make(chan error, 1)
	// Den blockierenden ListenAndServe in eine Goroutine auslagern, damit die
	// Hauptfunktion gleichzeitig auf Beenden-Signale warten kann.
	go func() {
		scheme := "http"
		var err error
		if opt.TLS {
			scheme = "https"
			// Zertifikat sicherstellen (erzeugen, falls nicht vorhanden).
			certPath, keyPath, e := tlscert.Ensure(opt.CertDir)
			if e != nil {
				errCh <- fmt.Errorf("Zertifikat: %w", e)
				return
			}
			log.Printf("Backend (HTTPS) auf %s://%s  (Add-in: %s://%s/addin/taskpane.html)", scheme, addr, scheme, addr)
			err = httpSrv.ListenAndServeTLS(certPath, keyPath)
		} else {
			log.Printf("Backend (HTTP) auf %s://%s", scheme, addr)
			err = httpSrv.ListenAndServe()
		}
		// ListenAndServe blockiert und liefert beim Beenden IMMER einen Fehler.
		// http.ErrServerClosed ist dabei der NORMALFALL (sauberes Shutdown) und
		// soll nicht als echter Fehler gemeldet werden.
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	// Auf Betriebssystem-Signale lauschen (Strg+C = os.Interrupt, oder SIGTERM).
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	// select wartet auf das ERSTE Ereignis: entweder ein Serverfehler ODER ein Signal.
	select {
	case err := <-errCh:
		stop(httpSrv, proc)
		return err
	case <-sig:
		log.Println("fahre herunter…")
		stop(httpSrv, proc)
		return nil
	}
}

// stop fährt HTTP-Server und Subprozess geordnet herunter.
func stop(httpSrv *http.Server, proc *osrm.Process) {
	// Ein Context mit Timeout: Shutdown darf höchstens 10s dauern, danach wird
	// abgebrochen. `cancel` MUSS aufgerufen werden (per defer), um Ressourcen
	// freizugeben — Standard-Muster bei context.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx) // wartet, bis offene Requests fertig sind (bis Timeout)
	if proc != nil {
		_ = proc.Stop()
	}
}

