// Package tlscert erzeugt bei Bedarf ein selbstsigniertes localhost-Zertifikat für
// das HTTPS-Backend (Office.js verlangt HTTPS). Existiert bereits eines (z. B. von
// mkcert), wird es wiederverwendet.
package tlscert

import (
	"crypto/ecdsa"     // ECDSA-Schlüssel (modern, klein, schnell)
	"crypto/elliptic"  // die Kurve P-256
	"crypto/rand"      // KRYPTOGRAFISCHER Zufall (nicht math/rand!)
	"crypto/x509"      // Zertifikate erstellen
	"crypto/x509/pkix" // Namens-/Subject-Felder
	"encoding/pem"     // PEM-Textformat (-----BEGIN CERTIFICATE-----)
	"math/big"         // große Ganzzahlen (Seriennummer)
	"net"              // IP-Adressen
	"os"
	"path/filepath"
	"time"
)

// Ensure stellt sicher, dass cert/key in certDir existieren, und liefert ihre Pfade.
// Fehlen sie, wird ein selbstsigniertes Zertifikat (127.0.0.1, localhost, ::1) erzeugt.
//
// GO-EINSTEIGER: Die Rückgabewerte sind hier BENANNT (certPath, keyPath, err). Man
// kann sie im Funktionsrumpf wie normale Variablen nutzen und mit nacktem `return`
// zurückgeben — hier werden sie aber überall explizit zurückgegeben (gut lesbar).
func Ensure(certDir string) (certPath, keyPath string, err error) {
	certPath = filepath.Join(certDir, "localhost.pem")
	keyPath = filepath.Join(certDir, "localhost-key.pem")
	// Schon vorhanden? Dann nichts neu erzeugen.
	if fileExists(certPath) && fileExists(keyPath) {
		return certPath, keyPath, nil
	}
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		return "", "", err
	}

	// 1) Privaten Schlüssel erzeugen (P-256-Kurve, kryptografischer Zufall).
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	// 2) Zufällige Seriennummer (bis 2^128). big.Int rechnet mit beliebig großen Zahlen.
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}
	// 3) Die Zertifikats-"Vorlage" mit allen Eigenschaften beschreiben.
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "localhost", Organization: []string{"Kilometrix"}},
		NotBefore:             time.Now().Add(-time.Hour),                                   // 1h Toleranz in die Vergangenheit
		NotAfter:              time.Now().Add(825 * 24 * time.Hour),                         // ~825 Tage gültig
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, // | = Bit-ODER (Flags kombinieren)
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}, // 127.0.0.1 und ::1
	}
	// 4) Selbstsigniert: Vorlage als Zertifikat UND als Aussteller (2x &tmpl).
	//    der DER-Block ist die binäre Form des Zertifikats.
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}

	// 5) Zertifikat und Schlüssel als PEM-Dateien speichern.
	if err := writePEM(certPath, "CERTIFICATE", der); err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(key) // privaten Schlüssel in DER-Bytes
	if err != nil {
		return "", "", err
	}
	if err := writePEM(keyPath, "EC PRIVATE KEY", keyDER); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

// writePEM schreibt einen DER-Block als PEM-Datei (Textformat mit Kopf-/Fußzeile).
func writePEM(path, typ string, der []byte) error {
	// O_WRONLY|O_CREATE|O_TRUNC: nur schreiben, anlegen falls nötig, sonst leeren.
	// 0o600 = nur der Besitzer darf lesen/schreiben (wichtig für den privaten Schlüssel).
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: typ, Bytes: der})
}

// fileExists prüft, ob path existiert UND eine Datei (kein Verzeichnis) ist.
func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
