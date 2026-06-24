package geocode

import (
	"strings"
	"testing"
)

func TestNormCountry(t *testing.T) {
	if got := NormCountry(" de "); got != "DE" {
		t.Fatalf("NormCountry = %q, want DE", got)
	}
}

func TestNormPLZ(t *testing.T) {
	cases := []struct{ country, in, want string }{
		{"DE", "1067", "01067"},
		{"DE", " 80331 ", "80331"},
		{"AT", "1010", "1010"}, // nur DE auffüllen
		{"DE", "AB12", "AB12"}, // nicht-numerisch unverändert
	}
	for _, c := range cases {
		if got := NormPLZ(c.country, c.in); got != c.want {
			t.Errorf("NormPLZ(%q,%q) = %q, want %q", c.country, c.in, got, c.want)
		}
	}
}

func TestResolveNormalizesLookup(t *testing.T) {
	g := New(map[key]Coord{MakeKey("DE", "01067"): {51.05, 13.74}})
	if c, ok := g.Resolve("de", "1067"); !ok || c.Lat != 51.05 || c.Lon != 13.74 {
		t.Fatalf("Resolve(de,1067) = %v,%v", c, ok)
	}
	if _, ok := g.Resolve("DE", "99999"); ok {
		t.Fatal("Resolve(DE,99999) sollte nicht gefunden werden")
	}
}

func TestLoadTable(t *testing.T) {
	csv := "country,plz,lat,lon\nDE,80331,48.137,11.575\nDE,10115,52.53,13.38\n"
	table, err := LoadTable(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(table) != 2 {
		t.Fatalf("len = %d, want 2", len(table))
	}
	if c := table[MakeKey("DE", "80331")]; c.Lat != 48.137 || c.Lon != 11.575 {
		t.Fatalf("80331 = %v", c)
	}
}
