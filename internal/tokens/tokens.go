// Package tokens erzeugt und prüft signierte, zustandslose Zugangstokens (HMAC-SHA256).
// Format kompatibel zur früheren Python-Variante: base64url(payload).base64url(hmac),
// payload-JSON mit sortierten Keys und kompakten Separatoren {"exp":..,"iat":..,"sub":..}.
package tokens

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Claims sind die im Token kodierten Felder.
type Claims struct {
	Sub string `json:"sub"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
}

var b64 = base64.RawURLEncoding

func sign(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return b64.EncodeToString(mac.Sum(nil))
}

// Mint erzeugt ein Token für name mit Gültigkeit ttl.
func Mint(secret, name string, ttl time.Duration) string {
	now := time.Now().Unix()
	// Reihenfolge exp,iat,sub = sortierte Keys (kompatibel zu Python json sort_keys).
	body := b64.EncodeToString([]byte(fmt.Sprintf(
		`{"exp":%d,"iat":%d,"sub":%s}`, now+int64(ttl.Seconds()), now, jsonString(name),
	)))
	return body + "." + sign(secret, body)
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Verify prüft Signatur und Ablauf und liefert die Claims.
func Verify(secret, token string) (Claims, error) {
	if secret == "" {
		return Claims{}, errors.New("AUTH_SECRET nicht gesetzt")
	}
	body, sig, ok := strings.Cut(token, ".")
	if !ok {
		return Claims{}, errors.New("Token-Format ungültig")
	}
	if !hmac.Equal([]byte(sig), []byte(sign(secret, body))) {
		return Claims{}, errors.New("Signatur ungültig")
	}
	raw, err := b64.DecodeString(body)
	if err != nil {
		return Claims{}, errors.New("Payload ungültig")
	}
	var c Claims
	if err := json.Unmarshal(raw, &c); err != nil {
		return Claims{}, errors.New("Payload ungültig")
	}
	if c.Exp < time.Now().Unix() {
		return Claims{}, errors.New("Token abgelaufen")
	}
	return c, nil
}
