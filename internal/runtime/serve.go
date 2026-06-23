// Package runtime verdrahtet die Komponenten zum laufenden Backend (entspricht dem
// früheren FastAPI-lifespan): Geocoder laden, osrm-routed ggf. starten, Engine bauen,
// HTTP(S) bedienen und sauber herunterfahren.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
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
func Serve(opt ServeOptions) error {
	settings := config.Load()
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
	var proc *osrm.Process
	engineErr := ""
	if settings.ManageOSRMRouted {
		proc = &osrm.Process{
			Binary:    settings.OSRMRoutedBin,
			GraphPath: settings.OSRMGraphPath,
			Algorithm: lower(settings.OSRMAlgorithm),
			Host:      settings.OSRMRoutedHost,
			Port:      settings.OSRMRoutedPort,
			Verbosity: settings.OSRMRoutedVerbosity,
			Mmap:      settings.OSRMRoutedMmap,
		}
		log.Printf("starte osrm-routed (%s)…", settings.OSRMGraphPath)
		if err := proc.Start(60 * time.Second); err != nil {
			engineErr = err.Error()
			log.Printf("WARN: osrm-routed nicht gestartet: %v", err)
			proc = nil
		}
	}

	var engine routing.Engine
	if engineErr == "" {
		engine = routing.NewHTTPEngine(settings.RoutedBaseURL(), settings.SnapLimitM)
	}

	geo, err := geocode.Load(settings.GeocodePath)
	if err != nil {
		log.Printf("WARN: Geocoder nicht geladen: %v", err)
		geo = nil
	}
	if geo != nil {
		log.Printf("Geocoder geladen: %d PLZ-Zentroide", geo.Len())
	}

	srv := server.New(settings, engine, engineErr, geo)
	addr := fmt.Sprintf("%s:%d", opt.Host, opt.Port)
	httpSrv := &http.Server{Addr: addr, Handler: srv.Handler()}

	// Sauberes Herunterfahren: erst HTTP-Server, dann osrm-routed.
	errCh := make(chan error, 1)
	go func() {
		scheme := "http"
		var err error
		if opt.TLS {
			scheme = "https"
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
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
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

func stop(httpSrv *http.Server, proc *osrm.Process) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	if proc != nil {
		_ = proc.Stop()
	}
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
