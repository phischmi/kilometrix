// Package tlscert erzeugt bei Bedarf ein selbstsigniertes localhost-Zertifikat für
// das HTTPS-Backend (Office.js verlangt HTTPS). Existiert bereits eines (z. B. von
// mkcert), wird es wiederverwendet.
package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Ensure stellt sicher, dass cert/key in certDir existieren, und liefert ihre Pfade.
// Fehlen sie, wird ein selbstsigniertes Zertifikat (127.0.0.1, localhost, ::1) erzeugt.
func Ensure(certDir string) (certPath, keyPath string, err error) {
	certPath = filepath.Join(certDir, "localhost.pem")
	keyPath = filepath.Join(certDir, "localhost-key.pem")
	if fileExists(certPath) && fileExists(keyPath) {
		return certPath, keyPath, nil
	}
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		return "", "", err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "localhost", Organization: []string{"Kilometrix"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(825 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}

	if err := writePEM(certPath, "CERTIFICATE", der); err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", err
	}
	if err := writePEM(keyPath, "EC PRIVATE KEY", keyDER); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

func writePEM(path, typ string, der []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: typ, Bytes: der})
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
