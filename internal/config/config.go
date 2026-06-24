// Package config lädt die Einstellungen aus Umgebungsvariablen und optional einer .env-Datei
// (keine harten Pfade).
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Settings hält die gesamte Laufzeitkonfiguration.
type Settings struct {
	// OSRM-Graph (vom osrm-routed geladen)
	OSRMGraphPath string
	OSRMAlgorithm string

	// osrm-routed als lokaler Subprozess
	OSRMRoutedBin       string
	OSRMRoutedHost      string
	OSRMRoutedPort      int
	ManageOSRMRouted    bool
	OSRMRoutedURL       string // expliziter Override; sonst host:port
	OSRMRoutedVerbosity string
	OSRMRoutedMmap      bool

	// Geocoding (LKZ/PLZ → Zentroid)
	GeocodePath string

	// Verarbeitung
	Workers      int
	SnapLimitM   float64
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
func (s Settings) RoutedBaseURL() string {
	if s.OSRMRoutedURL != "" {
		return s.OSRMRoutedURL
	}
	return fmt.Sprintf("http://%s:%d", s.OSRMRoutedHost, s.OSRMRoutedPort)
}

// Load liest .env (falls vorhanden) und die Umgebung und liefert Settings mit Defaults.
// Echte Umgebungsvariablen haben Vorrang vor .env.
func Load() Settings {
	loadDotEnv(".env")
	return Settings{
		OSRMGraphPath:       getStr("OSRM_GRAPH_PATH", "data/germany.osrm"),
		OSRMAlgorithm:       getStr("OSRM_ALGORITHM", "MLD"),
		OSRMRoutedBin:       getStr("OSRM_ROUTED_BIN", "osrm-routed"),
		OSRMRoutedHost:      getStr("OSRM_ROUTED_HOST", "127.0.0.1"),
		OSRMRoutedPort:      getInt("OSRM_ROUTED_PORT", 5001),
		ManageOSRMRouted:    getBool("MANAGE_OSRM_ROUTED", true),
		OSRMRoutedURL:       getStr("OSRM_ROUTED_URL", ""),
		OSRMRoutedVerbosity: getStr("OSRM_ROUTED_VERBOSITY", "WARNING"),
		OSRMRoutedMmap:      getBool("OSRM_ROUTED_MMAP", true),
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
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}

func getStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func getInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func getFloat(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return n
		}
	}
	return def
}

func getBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return def
}
