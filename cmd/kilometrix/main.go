// Command kilometrix ist das Backend-Binary mit Subcommands:
//
//	kilometrix serve          # HTTP(S)-Backend + osrm-routed (lokal)
//	kilometrix build-graph    # OSRM-Graph bauen (orchestriert osrm-*)
//	kilometrix build-geocode  # PLZ-Zentroid-Tabelle bauen (GeoNames)
//	kilometrix token create --name X --days 90
//	kilometrix token verify <token>

// GO-EINSTEIGER: Jede .go-Datei beginnt mit `package <name>`. Ein Paket ist die
// Einheit der Kapselung (vergleichbar mit einem Python-Modul/Package). Genau das
// Paket namens `main` ist besonders: Go baut daraus ein ausführbares Programm und
// ruft beim Start die Funktion `main()` auf. Alle anderen Pakete (internal/...)
// sind Bibliotheken, die importiert werden.
package main

// `import` zieht andere Pakete herein. Anders als in Python gibt es keine
// "ungenutzten Imports": Was du importierst, musst du auch verwenden, sonst
// kompiliert Go nicht. In Klammern listet man mehrere Imports untereinander.
import (
	"encoding/json" // JSON lesen/schreiben (wie Pythons json)
	"flag"          // CLI-Flags parsen (wie argparse, nur schlanker)
	"fmt"           // Formatierte Ausgabe (Println/Printf) und Fehlertexte
	"os"            // Betriebssystem: Argumente, Exit-Codes, stdout/stderr, Dateien
	"time"          // Zeit/Dauer

	// Der Unterstrich `_` ist ein "blank import": Das Paket wird nur wegen seiner
	// Seiteneffekte geladen (hier: Zeitzonendaten ins Binary einbetten), ohne dass
	// wir einen Namen daraus direkt benutzen. Ohne den `_` würde Go den
	// "unbenutzten Import" als Fehler werten.
	_ "time/tzdata" // Zoneinfo ins Binary einbetten (distroless hat keine) -> TZ=Europe/Berlin wirkt

	// Eigene Pakete dieses Projekts. Der lange Pfad ist der Modul-Pfad aus go.mod
	// plus Verzeichnis. Verwendet werden sie über den letzten Pfadteil, z. B.
	// `build.BuildGraph(...)`, `config.Load()`.
	"github.com/phischmi/kilometrix/internal/build"
	"github.com/phischmi/kilometrix/internal/config"
	"github.com/phischmi/kilometrix/internal/osrm"
	"github.com/phischmi/kilometrix/internal/runtime"
	"github.com/phischmi/kilometrix/internal/tokens"
)

// main ist der Programmstart. Hier dispatchen wir das erste CLI-Argument
// (z. B. "serve") auf die passende Funktion.
func main() {
	// os.Args ist ein Slice (dynamisches Array) der Kommandozeilen-Argumente.
	// os.Args[0] ist der Programmname selbst, os.Args[1] das erste echte Argument.
	// `len(...)` liefert die Länge. Ohne Subcommand -> Hilfe zeigen und beenden.
	if len(os.Args) < 2 {
		usage()
		os.Exit(2) // Exit-Code != 0 signalisiert "Fehler" an die Shell.
	}
	// `switch` ist wie in Python match/case, aber ohne automatisches Durchfallen:
	// nach einem passenden `case` ist Schluss (kein `break` nötig).
	// .env einmalig laden, damit HTTPS_PROXY u. a. für alle Subcommands verfügbar sind.
	config.Load()

	switch os.Args[1] {
	case "serve":
		cmdServe(os.Args[2:]) // os.Args[2:] = alle Argumente ab Index 2 (Slice-Ausschnitt)
	case "build-graph":
		cmdBuildGraph(os.Args[2:])
	case "build-geocode":
		cmdBuildGeocode(os.Args[2:])
	case "token":
		cmdToken(os.Args[2:])
	case "config":
		cmdConfig(os.Args[2:])
	case "-h", "--help", "help": // mehrere Werte pro case durch Komma getrennt
		usage()
	default: // wie `else`/`case _` — wenn nichts passt
		// Fprintf schreibt nach os.Stderr (Standard-Fehlerkanal). %s ist ein
		// Platzhalter für einen String, \n ein Zeilenumbruch.
		fmt.Fprintf(os.Stderr, "unbekannter Befehl: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

// usage gibt den Hilfetext aus. Der Text steht in einem "raw string literal"
// (in Backticks “), in dem \n & Co. NICHT interpretiert werden und das über
// mehrere Zeilen gehen darf — praktisch für Mehrzeiler.
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

// cmdServe verarbeitet `kilometrix serve [...]`. `args` sind die Argumente NACH
// dem Wort "serve". []string ist ein Slice von Strings.
func cmdServe(args []string) {
	// Ein FlagSet ist eine eigene Gruppe von CLI-Flags für diesen Subcommand.
	// flag.ExitOnError = bei Parse-Fehler Programm beenden.
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	// fs.String(name, default, hilfetext) registriert ein Flag und gibt einen
	// *Pointer* auf den späteren Wert zurück (deshalb der Stern bei *host weiter
	// unten). Der Wert steht erst nach fs.Parse(...) fest.
	host := fs.String("host", "", "Bind-Host (Default aus ADDIN_HOST)")
	port := fs.Int("port", 0, "Bind-Port (Default aus ADDIN_PORT)")
	tls := fs.Bool("tls", true, "HTTPS mit lokalem Zertifikat (false hinter Reverse Proxy)")
	certDir := fs.String("cert-dir", "certs", "Verzeichnis für localhost-Zertifikat")
	// fs.Parse füllt die Flags. Der Rückgabewert (ein Fehler) interessiert hier
	// nicht, weil ExitOnError schon abbricht; `_ =` wirft ihn bewusst weg.
	// (Go zwingt dich sonst, jeden Rückgabewert zu benutzen.)
	_ = fs.Parse(args)

	// runtime.Serve bekommt eine Struct mit den Optionen. Der Stern `*host`
	// dereferenziert den Pointer = "hol den Wert hinter der Adresse".
	err := runtime.Serve(runtime.ServeOptions{
		Host: *host, Port: *port, TLS: *tls, CertDir: *certDir,
	})
	// Go-typisches Fehlermuster: Funktionen geben Fehler als letzten Wert zurück.
	// Man prüft sie sofort mit `if err != nil`. nil ist der "leere"/null-Wert.
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

	// Zweites Argument `stdoutLog` ist eine FUNKTION, die als Wert übergeben wird
	// (Funktionen sind in Go "first class", wie in Python). BuildGraph ruft sie
	// auf, um Fortschritt auszugeben.
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

	logEnvDiag(stdoutLog)

	err := build.BuildGeocode(build.GeocodeOptions{
		Country: *country, ZipURL: *zipURL, OutPath: *out,
	}, stdoutLog)
	if err != nil {
		fatal(err)
	}
}

func cmdToken(args []string) {
	if len(args) < 1 {
		// fmt.Errorf baut einen neuen Fehlerwert aus einem Text (wie eine Exception
		// erzeugen, aber Go wirft nicht — es gibt den Fehler nur zurück/weiter).
		fatal(fmt.Errorf("token: 'create' oder 'verify' erwartet"))
	}
	// config.Load() liefert eine Struct; mit dem Punkt greift man auf ein Feld zu.
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
		// time.Duration ist eine Dauer in Nanosekunden. *days*24*Stunden ergibt
		// die TTL. float64(...) ist eine explizite Typumwandlung — Go konvertiert
		// Zahlentypen NIE automatisch, anders als Python.
		tok := tokens.Mint(secret, *name, time.Duration(*days*24*float64(time.Hour)))
		fmt.Println(tok) // Token nach stdout (kann weiterverarbeitet werden)
		// tokens.Verify gibt ZWEI Werte zurück (Claims, Fehler). Den Fehler werfen
		// wir hier mit `_` weg, weil wir gerade selbst signiert haben.
		c, _ := tokens.Verify(secret, tok)
		// time.Unix(...) wandelt einen Unix-Zeitstempel in eine Zeit; Format(...)
		// nutzt Gos Referenzdatum "2006-01-02 15:04" als Muster (kein %Y%m%d!).
		fmt.Fprintf(os.Stderr, "# name=%s  gültig bis %s UTC\n", *name, time.Unix(c.Exp, 0).UTC().Format("2006-01-02 15:04"))
	case "verify":
		fs := flag.NewFlagSet("token verify", flag.ExitOnError)
		_ = fs.Parse(args[1:])
		if fs.NArg() < 1 { // NArg = Anzahl übriger Argumente nach den Flags
			fatal(fmt.Errorf("token verify <token>"))
		}
		c, err := tokens.Verify(secret, fs.Arg(0)) // Arg(0) = erstes übriges Argument
		if err != nil {
			// %w bettet den ursprünglichen Fehler ein ("wrapping"): so bleibt die
			// Ursache erhalten und ist später mit errors.Is/As prüfbar.
			fatal(fmt.Errorf("ungültig: %w", err))
		}
		fmt.Printf("sub=%s exp=%d\n", c.Sub, c.Exp) // %d = Ganzzahl
	default:
		fatal(fmt.Errorf("token: unbekannt %q (create|verify)", args[0])) // %q = in Anführungszeichen
	}
}

func cmdConfig(args []string) {
	fs := flag.NewFlagSet("config", flag.ExitOnError)
	_ = fs.Parse(args)
	s := config.Load()
	graphExists := osrm.GraphExists(s.OSRMGraphPath)
	// os.Stat liefert Datei-Infos und einen Fehler. Hier interessiert nur, OB ein
	// Fehler auftrat (Datei fehlt), die Infos selbst werfen wir mit `_` weg.
	_, geoErr := os.Stat(s.GeocodePath)
	// map[string]any ist eine Map (Dictionary) von String -> beliebiger Typ.
	// `any` ist das leere Interface, ein Platzhalter für "irgendein Typ"
	// (vergleichbar mit Pythons dynamischen Werten).
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
		"geocode_present":    geoErr == nil, // nil == kein Fehler == Datei da
	}
	// JSON schön eingerückt nach stdout schreiben (Encoder schreibt direkt in den Stream).
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

// logEnvDiag gibt .env-Status und aktive Proxy-Konfiguration aus.
func logEnvDiag(log func(string)) {
	if _, err := os.Stat(".env"); err == nil {
		log(".env gefunden und geladen")
	} else {
		log("WARN: .env nicht gefunden — Proxy/Pfade nur aus Umgebungsvariablen")
	}
	if p := os.Getenv("HTTPS_PROXY"); p != "" {
		log("HTTPS_PROXY=" + p)
	} else if p := os.Getenv("HTTP_PROXY"); p != "" {
		log("HTTP_PROXY=" + p)
	} else {
		log("Kein Proxy konfiguriert (HTTPS_PROXY/HTTP_PROXY nicht gesetzt)")
	}
}

// stdoutLog ist eine winzige Funktion, die wir als Callback weiterreichen
// (siehe BuildGraph/BuildGeocode). Einzeiler dürfen in Go in einer Zeile stehen.
func stdoutLog(s string) { fmt.Println(s) }

// fatal gibt eine Fehlermeldung aus und beendet das Programm mit Code 1.
// `err error` nutzt den eingebauten Interface-Typ `error`.
func fatal(err error) {
	fmt.Fprintln(os.Stderr, "Fehler:", err)
	os.Exit(1)
}
