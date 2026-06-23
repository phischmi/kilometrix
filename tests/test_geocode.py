"""Geocoding: Normalisierung, Zentroid-Laden, Lookup und Endpunkt-Auflösung."""

from pathlib import Path

from backend.geocode import Geocoder, load_table, norm_country, norm_plz
from backend.main import _resolve_endpoint


def test_norm_country_uppercases_and_strips():
    assert norm_country(" de ") == "DE"


def test_norm_plz_zfills_de_numeric():
    assert norm_plz("DE", "1067") == "01067"
    assert norm_plz("DE", " 80331 ") == "80331"
    # nur DE auffüllen, nicht-numerische unverändert lassen
    assert norm_plz("AT", "1010") == "1010"
    assert norm_plz("DE", "AB12") == "AB12"


def test_geocoder_resolve_normalizes_lookup():
    geo = Geocoder({("DE", "01067"): (51.05, 13.74)})
    assert geo.resolve("de", "1067") == (51.05, 13.74)  # case + zfill
    assert geo.resolve("DE", "99999") is None


def test_load_table_reads_csv(tmp_path: Path):
    csv = tmp_path / "plz.csv"
    csv.write_text(
        "country,plz,lat,lon\nDE,80331,48.137,11.575\nDE,10115,52.53,13.38\n",
        encoding="utf-8",
    )
    table = load_table(csv)
    assert table[("DE", "80331")] == (48.137, 11.575)
    assert len(table) == 2


def test_resolve_endpoint_prefers_coords():
    cache: dict = {}
    assert _resolve_endpoint(48.1, 11.5, None, None, None, cache) == (48.1, 11.5)


def test_resolve_endpoint_uses_geocoder_and_dedupes():
    calls = {"n": 0}

    class FakeGeo:
        def resolve(self, lkz, plz):
            calls["n"] += 1
            return (1.0, 2.0) if plz == "80331" else None

    cache: dict = {}
    assert _resolve_endpoint(None, None, "DE", "80331", FakeGeo(), cache) == (1.0, 2.0)
    # zweiter Aufruf gleicher Schlüssel → aus dem Cache, kein erneuter resolve
    assert _resolve_endpoint(None, None, "DE", "80331", FakeGeo(), cache) == (1.0, 2.0)
    assert calls["n"] == 1
    assert _resolve_endpoint(None, None, "DE", "00000", FakeGeo(), cache) is None
