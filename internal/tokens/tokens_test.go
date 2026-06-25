package tokens

// GO-EINSTEIGER: Tests stehen in Dateien, die auf _test.go enden, und gehören
// (meist) ins gleiche Paket wie der getestete Code — dadurch sehen sie auch
// dessen private Funktionen. Es gibt KEIN externes Test-Framework wie pytest:
// das eingebaute `testing`-Paket reicht. Ausführen mit `go test ./...`.

import (
	"testing"
	"time"
)

// Eine Testfunktion beginnt mit `Test`, gefolgt von einem Großbuchstaben, und
// bekommt genau ein Argument *testing.T (das Test-Handle für Fehler/Logs).
func TestMintVerifyRoundtrip(t *testing.T) {
	tok := Mint("secret", "philipp", 24*time.Hour)
	c, err := Verify("secret", tok)
	if err != nil {
		// t.Fatal meldet den Test als fehlgeschlagen UND bricht ihn sofort ab.
		// (t.Error meldet nur, läuft aber weiter.)
		t.Fatal(err)
	}
	if c.Sub != "philipp" {
		t.Fatalf("sub = %q", c.Sub) // ...f-Varianten formatieren wie Printf
	}
}

// "Round-trip" mit falschem Secret muss scheitern -> err darf NICHT nil sein.
func TestVerifyRejectsWrongSecret(t *testing.T) {
	tok := Mint("secret", "x", time.Hour)
	if _, err := Verify("other", tok); err == nil {
		t.Fatal("falsches Secret sollte abgelehnt werden")
	}
}

// Negative TTL -> Token ist sofort abgelaufen -> Verify muss meckern.
func TestVerifyRejectsExpired(t *testing.T) {
	tok := Mint("secret", "x", -time.Second)
	if _, err := Verify("secret", tok); err == nil {
		t.Fatal("abgelaufenes Token sollte abgelehnt werden")
	}
}

func TestVerifyEmptySecret(t *testing.T) {
	if _, err := Verify("", "a.b"); err == nil {
		t.Fatal("leeres Secret sollte Fehler liefern")
	}
}
