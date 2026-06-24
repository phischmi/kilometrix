// Package tokens erzeugt und prüft signierte, zustandslose Zugangstokens (HMAC-SHA256).
// Format kompatibel zur früheren Python-Variante: base64url(payload).base64url(hmac),
// payload-JSON mit sortierten Keys und kompakten Separatoren {"exp":..,"iat":..,"sub":..}.
package tokens

import (
	"crypto/hmac"     // HMAC = signierter Hash mit geheimem Schlüssel
	"crypto/sha256"   // die genutzte Hash-Funktion
	"encoding/base64" // URL-sichere Kodierung der Bytes -> Text
	"encoding/json"   // zum sicheren Quoten des Namens
	"errors"
	"fmt"
	"strings"
	"time"
)

// Claims sind die im Token kodierten Felder.
//
// GO-EINSTEIGER: Die Texte in Backticks hinter den Feldern sind "struct tags".
// Sie sind Metadaten, die das json-Paket per Reflection ausliest: `json:"sub"`
// heißt "dieses Feld heißt im JSON 'sub'". Ohne Tag würde Go den Feldnamen "Sub"
// verwenden. So bleibt das JSON-Format kompatibel zur alten Python-Version.
type Claims struct {
	Sub string `json:"sub"` // subject = Name/Empfänger
	Iat int64  `json:"iat"` // issued at = Ausstellungszeit (Unix-Sekunden)
	Exp int64  `json:"exp"` // expiry = Ablaufzeit (Unix-Sekunden)
}

// b64 ist die rohe URL-Variante von base64 (ohne "="-Padding). Eine Paket-weite
// Variable auf Modulebene; `var name = wert` deklariert sie (Typ wird abgeleitet).
var b64 = base64.RawURLEncoding

// sign berechnet die HMAC-Signatur über body mit dem geheimen secret.
func sign(secret, body string) string {
	// hmac.New braucht eine Hash-FUNKTION (sha256.New, ohne Klammern = die
	// Funktion selbst) und den Schlüssel als Bytes.
	// []byte(secret) wandelt einen String in ein Byte-Slice — in Go sind Strings
	// unveränderlich, Bytes nicht; viele Crypto-/IO-APIs arbeiten mit []byte.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))                 // die zu signierenden Daten einspeisen
	return b64.EncodeToString(mac.Sum(nil)) // Sum(nil) liefert die rohe Signatur als []byte
}

// Mint erzeugt ein Token für name mit Gültigkeit ttl.
// (ttl = "time to live" = Lebensdauer, vom Typ time.Duration.)
func Mint(secret, name string, ttl time.Duration) string {
	now := time.Now().Unix() // aktueller Zeitpunkt als Unix-Sekunden (int64)
	// Reihenfolge exp,iat,sub = sortierte Keys (kompatibel zu Python json sort_keys).
	// Wir bauen das JSON bewusst von Hand (statt json.Marshal), um exakt diese
	// Reihenfolge und kompakte Form zu garantieren. jsonString(name) sorgt fürs
	// korrekte Escapen/Quoten des Namens.
	body := b64.EncodeToString([]byte(fmt.Sprintf(
		`{"exp":%d,"iat":%d,"sub":%s}`, now+int64(ttl.Seconds()), now, jsonString(name),
	)))
	// Token = base64(payload) + "." + base64(signatur). String-Verkettung mit +.
	return body + "." + sign(secret, body)
}

// jsonString liefert s als JSON-String inkl. Anführungszeichen und Escaping.
func jsonString(s string) string {
	b, _ := json.Marshal(s) // kann bei einem String nicht fehlschlagen -> Fehler ignorieren
	return string(b)
}

// Verify prüft Signatur und Ablauf und liefert die Claims.
// Rückgabe (Claims, error): bei Fehlern wird ein leeres Claims{} + Fehler geliefert.
func Verify(secret, token string) (Claims, error) {
	if secret == "" {
		return Claims{}, errors.New("AUTH_SECRET nicht gesetzt")
	}
	// Token am "." in body und sig zerlegen. ok=false, wenn kein "." vorkommt.
	body, sig, ok := strings.Cut(token, ".")
	if !ok {
		return Claims{}, errors.New("Token-Format ungültig")
	}
	// WICHTIG: hmac.Equal vergleicht in konstanter Zeit (schützt vor Timing-
	// Angriffen). Niemals Signaturen mit == vergleichen! Wir signieren den body
	// erneut und prüfen, ob das Ergebnis mit der mitgelieferten Signatur übereinstimmt.
	if !hmac.Equal([]byte(sig), []byte(sign(secret, body))) {
		return Claims{}, errors.New("Signatur ungültig")
	}
	raw, err := b64.DecodeString(body) // base64 zurück in JSON-Bytes
	if err != nil {
		return Claims{}, errors.New("Payload ungültig")
	}
	var c Claims // Nullwert: alle Felder leer/0
	// Unmarshal füllt die Struct anhand der json-Tags aus den Bytes (Pythons
	// json.loads + Zuordnung auf ein Objekt in einem Schritt). `&c` = Adresse von
	// c, damit Unmarshal hineinschreiben kann.
	if err := json.Unmarshal(raw, &c); err != nil {
		return Claims{}, errors.New("Payload ungültig")
	}
	if c.Exp < time.Now().Unix() {
		return Claims{}, errors.New("Token abgelaufen")
	}
	return c, nil
}
