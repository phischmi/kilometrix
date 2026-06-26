// Package config lädt die Einstellungen aus Umgebungsvariablen und optional einer .env-Datei
// (keine harten Pfade).
package config

import (
	"bufio"   // gepuffertes Lesen, hier zeilenweises Scannen der .env
	"fmt"     // Sprintf für die URL-Zusammensetzung
	"os"      // Umgebungsvariablen + Datei öffnen
	"strconv" // String -> Zahl (Atoi, ParseFloat)
	"strings" // String-Helfer (TrimSpace, ToLower, Cut, ...)
)

// Settings hält die gesamte Laufzeitkonfiguration.
//
// GO-EINSTEIGER: Eine `struct` ist ein zusammengesetzter Datentyp mit benannten
// Feldern (wie eine @dataclass in Python). Jedes Feld hat einen festen Typ.
// Felder, die mit GROSSBUCHSTABEN beginnen, sind "exportiert", d. h. von anderen
// Paketen aus sichtbar (Gos Variante von public/private — Kleinbuchstabe = privat).
type Settings struct {
	// OSRM-Graph (vom osrm-routed geladen)
	OSRMGraphPath string
	OSRMAlgorithm string

	// osrm-routed als lokaler Subprozess
	OSRMRoutedBin          string
	OSRMRoutedHost         string
	OSRMRoutedPort         int
	ManageOSRMRouted       bool
	OSRMRoutedURL          string // expliziter Override; sonst host:port
	OSRMRoutedVerbosity    string
	OSRMRoutedMmap         bool
	OSRMRoutedReadyTimeout int // Sekunden; 0 = Default (300)

	// Geocoding (LKZ/PLZ → Zentroid)
	GeocodePath string

	// Verarbeitung
	Workers      int
	SnapLimitM   float64 // float64 = Gleitkommazahl mit doppelter Genauigkeit (Pythons float)
	MaxSyncBatch int

	// Auth (für zentralen Betrieb hinter Reverse Proxy)
	AuthEnabled bool
	AuthSecret  string

	// Backend / Office.js-Add-in (HTTPS)
	AddinHost string
	AddinPort int

	// Verzeichnis mit den Add-in-Dateien (taskpane.html etc.)
	AddinDir string
}

// RoutedBaseURL ist die Basis-URL des osrm-routed (Override oder host:port).
//
// GO-EINSTEIGER: Das `(s Settings)` vor dem Namen ist der "Receiver" — damit wird
// die Funktion zu einer METHODE auf Settings (wie `self` in Python, nur explizit
// und benannt). Aufruf: settings.RoutedBaseURL(). Hier ist der Receiver eine
// Kopie (kein Pointer), weil die Methode nichts verändert, nur liest.
func (s Settings) RoutedBaseURL() string {
	if s.OSRMRoutedURL != "" {
		return s.OSRMRoutedURL
	}
	// Sprintf baut einen String aus dem Format + Werten (gibt ihn zurück, statt
	// ihn auszugeben). %s = String, %d = Ganzzahl.
	return fmt.Sprintf("http://%s:%d", s.OSRMRoutedHost, s.OSRMRoutedPort)
}

// Load liest .env (falls vorhanden) und die Umgebung und liefert Settings mit Defaults.
// Echte Umgebungsvariablen haben Vorrang vor .env.
func Load() Settings {
	loadDotEnv(".env")
	// Hier wird eine Settings-Struct mit benannten Feldern erzeugt und direkt
	// zurückgegeben. Jedes Feld bekommt seinen Wert über die get*-Helfer unten,
	// die jeweils einen Default mitbringen.
	return Settings{
		OSRMGraphPath:       getStr("OSRM_GRAPH_PATH", "data/germany.osrm"),
		OSRMAlgorithm:       getStr("OSRM_ALGORITHM", "MLD"),
		OSRMRoutedBin:          getStr("OSRM_ROUTED_BIN", "osrm-routed"),
		OSRMRoutedHost:         getStr("OSRM_ROUTED_HOST", "127.0.0.1"),
		OSRMRoutedPort:         getInt("OSRM_ROUTED_PORT", 5001),
		ManageOSRMRouted:       getBool("MANAGE_OSRM_ROUTED", true),
		OSRMRoutedURL:          getStr("OSRM_ROUTED_URL", ""),
		OSRMRoutedVerbosity:    getStr("OSRM_ROUTED_VERBOSITY", "INFO"),
		OSRMRoutedMmap:         getBool("OSRM_ROUTED_MMAP", mmapDefault),
		OSRMRoutedReadyTimeout: getInt("OSRM_ROUTED_READY_TIMEOUT", 0),
		GeocodePath:         getStr("GEOCODE_PATH", "data/plz_centroids.csv"),
		Workers:             getInt("WORKERS", 8),
		SnapLimitM:          getFloat("SNAP_LIMIT_M", 100.0),
		MaxSyncBatch:        getInt("MAX_SYNC_BATCH", 20000),
		AuthEnabled:         getBool("AUTH_ENABLED", false),
		AuthSecret:          getStr("AUTH_SECRET", ""),
		AddinHost:           getStr("ADDIN_HOST", "127.0.0.1"),
		AddinPort:           getInt("ADDIN_PORT", 8443),
		AddinDir:            getStr("ADDIN_DIR", "addin"),
	}
}

// loadDotEnv setzt KEY=VALUE-Paare aus der Datei in die Umgebung, ohne bereits
// gesetzte echte Variablen zu überschreiben. Fehlt die Datei, ist das ein No-op.
func loadDotEnv(path string) {
	// os.Open gibt (Datei, Fehler) zurück. Fehlt die Datei, brechen wir einfach
	// ab (kein Fehler nach außen) — .env ist optional.
	f, err := os.Open(path)
	if err != nil {
		return
	}
	// `defer` plant einen Aufruf für das ENDE der Funktion ein — egal wie sie
	// verlassen wird. Klassisches Muster zum Aufräumen (hier: Datei schließen),
	// vergleichbar mit Pythons `with`/try-finally.
	defer f.Close()
	// Ein Scanner liest die Datei Zeile für Zeile. sc.Scan() liefert true, solange
	// es noch eine Zeile gibt; sc.Text() ist die aktuelle Zeile.
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		// Leerzeilen und Kommentare (#) überspringen. `continue` springt zur
		// nächsten Schleifeniteration.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// strings.Cut teilt "KEY=VALUE" am ersten "=" in zwei Teile + ein bool,
		// das sagt, ob das Trennzeichen überhaupt vorkam. Drei Rückgabewerte!
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		// Umschließende " oder ' entfernen (Backtick-String listet die zu trimmenden Zeichen).
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		// LookupEnv gibt (Wert, existiert?) zurück. Nur setzen, wenn die Variable
		// noch NICHT existiert — echte Env-Variablen haben also Vorrang.
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}

// getStr liefert die Env-Variable `key`, sonst den Default `def`.
func getStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

// getInt wie getStr, wandelt aber nach int um. Schlägt die Umwandlung fehl,
// gilt der Default.
func getInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		// strconv.Atoi: "ASCII to integer". Gibt (Zahl, Fehler) zurück; nur bei
		// err == nil (= keine Fehler) verwenden wir die Zahl.
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func getFloat(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok {
		// ParseFloat mit Bit-Größe 64 = float64.
		if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return n
		}
	}
	return def
}

func getBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		// switch ohne Vergleichsausdruck auf einen normalisierten String:
		// mehrere Schreibweisen werden als true bzw. false akzeptiert.
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return def
}
