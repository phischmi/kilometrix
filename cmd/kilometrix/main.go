// Command kilometrix ist das Backend-Binary mit Subcommands:
//
//	kilometrix serve          # HTTP(S)-Backend + osrm-routed (lokal)
//	kilometrix build-graph    # OSRM-Graph bauen (orchestriert osrm-*)
//	kilometrix build-geocode  # PLZ-Zentroid-Tabelle bauen (GeoNames)
//	kilometrix token create --name X --days 90
//	kilometrix token verify <token>
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/phischmi/kilometrix/internal/build"
	"github.com/phischmi/kilometrix/internal/config"
	"github.com/phischmi/kilometrix/internal/osrm"
	"github.com/phischmi/kilometrix/internal/runtime"
	"github.com/phischmi/kilometrix/internal/tokens"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		cmdServe(os.Args[2:])
	case "build-graph":
		cmdBuildGraph(os.Args[2:])
	case "build-geocode":
		cmdBuildGeocode(os.Args[2:])
	case "token":
		cmdToken(os.Args[2:])
	case "config":
		cmdConfig(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unbekannter Befehl: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `kilometrix — OSRM Distanz-Tool (Backend)

Befehle:
  serve          HTTP(S)-Backend starten (+ osrm-routed lokal)
  build-graph    OSRM-Graph bauen (osrm-extract/-partition/-customize)
  build-geocode  PLZ-Zentroid-Tabelle aus GeoNames bauen
  token          Zugangstokens erzeugen/prüfen
  config         Aufgelöste Konfiguration als JSON ausgeben

'kilometrix <befehl> -h' für Optionen.
`)
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	host := fs.String("host", "", "Bind-Host (Default aus ADDIN_HOST)")
	port := fs.Int("port", 0, "Bind-Port (Default aus ADDIN_PORT)")
	tls := fs.Bool("tls", true, "HTTPS mit lokalem Zertifikat (false hinter Reverse Proxy)")
	certDir := fs.String("cert-dir", "certs", "Verzeichnis für localhost-Zertifikat")
	_ = fs.Parse(args)

	err := runtime.Serve(runtime.ServeOptions{
		Host: *host, Port: *port, TLS: *tls, CertDir: *certDir,
	})
	if err != nil {
		fatal(err)
	}
}

func cmdBuildGraph(args []string) {
	fs := flag.NewFlagSet("build-graph", flag.ExitOnError)
	pbf := fs.String("pbf-url", "", "PBF-URL (Default: Geofabrik germany-latest)")
	dataDir := fs.String("data-dir", "data", "Zielverzeichnis")
	profile := fs.String("profile", "", "LKW-Profil (Default: profiles/truck.lua)")
	_ = fs.Parse(args)

	err := build.BuildGraph(build.GraphOptions{
		PBFURL: *pbf, DataDir: *dataDir, ProfilePath: *profile,
	}, stdoutLog)
	if err != nil {
		fatal(err)
	}
}

func cmdBuildGeocode(args []string) {
	fs := flag.NewFlagSet("build-geocode", flag.ExitOnError)
	country := fs.String("country", "DE", "ISO-Land (GeoNames)")
	zipURL := fs.String("zip-url", "", "Override der Zip-URL")
	out := fs.String("out", "data/plz_centroids.csv", "Ziel-CSV")
	_ = fs.Parse(args)

	err := build.BuildGeocode(build.GeocodeOptions{
		Country: *country, ZipURL: *zipURL, OutPath: *out,
	}, stdoutLog)
	if err != nil {
		fatal(err)
	}
}

func cmdToken(args []string) {
	if len(args) < 1 {
		fatal(fmt.Errorf("token: 'create' oder 'verify' erwartet"))
	}
	secret := config.Load().AuthSecret
	if secret == "" {
		fatal(fmt.Errorf("AUTH_SECRET ist nicht gesetzt (in .env / Umgebung)"))
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("token create", flag.ExitOnError)
		name := fs.String("name", "", "Name/Empfänger des Tokens")
		days := fs.Float64("days", 90, "Gültigkeit in Tagen (TTL)")
		_ = fs.Parse(args[1:])
		if *name == "" {
			fatal(fmt.Errorf("--name ist erforderlich"))
		}
		tok := tokens.Mint(secret, *name, time.Duration(*days*24*float64(time.Hour)))
		fmt.Println(tok)
		c, _ := tokens.Verify(secret, tok)
		fmt.Fprintf(os.Stderr, "# name=%s  gültig bis %s UTC\n", *name, time.Unix(c.Exp, 0).UTC().Format("2006-01-02 15:04"))
	case "verify":
		fs := flag.NewFlagSet("token verify", flag.ExitOnError)
		_ = fs.Parse(args[1:])
		if fs.NArg() < 1 {
			fatal(fmt.Errorf("token verify <token>"))
		}
		c, err := tokens.Verify(secret, fs.Arg(0))
		if err != nil {
			fatal(fmt.Errorf("ungültig: %w", err))
		}
		fmt.Printf("sub=%s exp=%d\n", c.Sub, c.Exp)
	default:
		fatal(fmt.Errorf("token: unbekannt %q (create|verify)", args[0]))
	}
}

func cmdConfig(args []string) {
	fs := flag.NewFlagSet("config", flag.ExitOnError)
	_ = fs.Parse(args)
	s := config.Load()
	graphExists := osrm.GraphExists(s.OSRMGraphPath)
	_, geoErr := os.Stat(s.GeocodePath)
	out := map[string]any{
		"osrm_graph_path":    s.OSRMGraphPath,
		"osrm_algorithm":     s.OSRMAlgorithm,
		"osrm_routed_url":    s.RoutedBaseURL(),
		"manage_osrm_routed": s.ManageOSRMRouted,
		"osrm_routed_mmap":   s.OSRMRoutedMmap,
		"geocode_path":       s.GeocodePath,
		"workers":            s.Workers,
		"snap_limit_m":       s.SnapLimitM,
		"max_sync_batch":     s.MaxSyncBatch,
		"auth_enabled":       s.AuthEnabled,
		"addin_host":         s.AddinHost,
		"addin_port":         s.AddinPort,
		"graph_present":      graphExists,
		"geocode_present":    geoErr == nil,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func stdoutLog(s string) { fmt.Println(s) }

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "Fehler:", err)
	os.Exit(1)
}
