package tokens

import (
	"testing"
	"time"
)

func TestMintVerifyRoundtrip(t *testing.T) {
	tok := Mint("secret", "philipp", 24*time.Hour)
	c, err := Verify("secret", tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.Sub != "philipp" {
		t.Fatalf("sub = %q", c.Sub)
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	tok := Mint("secret", "x", time.Hour)
	if _, err := Verify("other", tok); err == nil {
		t.Fatal("falsches Secret sollte abgelehnt werden")
	}
}

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
