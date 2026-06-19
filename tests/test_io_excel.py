"""Roundtrip: Excel lesen, Mapping vorschlagen, Ergebnisse anhängen, schreiben."""

import pandas as pd

from backend.io_excel import read_workbook, suggest_mapping, write_results
from backend.routing import OK, NO_ROUTE, RouteResult


def _sample_df() -> pd.DataFrame:
    return pd.DataFrame(
        {
            "id": [1, 2],
            "origin_lat": [52.52, 48.13],
            "origin_lon": [13.40, 11.58],
            "dest_lat": [50.11, 53.55],
            "dest_lon": [8.68, 9.99],
        }
    )


def test_suggest_mapping_case_insensitive():
    sm = suggest_mapping(["ID", "Origin_Lat", "origin_lon", "DEST_LAT", "dest_lon"])
    assert sm["origin_lat"] == "Origin_Lat"
    assert sm["dest_lat"] == "DEST_LAT"


def test_roundtrip(tmp_path):
    src = tmp_path / "in.xlsx"
    _sample_df().to_excel(src, index=False)

    df, columns = read_workbook(src)
    assert "origin_lat" in columns and len(df) == 2

    results = [
        RouteResult(12.34, 5.6, OK, snap_m=3.0),
        RouteResult(None, None, NO_ROUTE, snap_m=None),
    ]
    out = write_results(df, results, tmp_path / "out.xlsx")

    back = pd.read_excel(out)
    assert list(back["distance_km"]) [0] == 12.34
    assert back["status"].tolist() == [OK, NO_ROUTE]
    assert "duration_min" in back.columns
