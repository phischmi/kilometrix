"""GeoNames-Postal-TSV → kompakte Zentroid-CSV (country,plz,lat,lon).

Pro PLZ listet GeoNames mehrere Ortsteile; der Zentroid ist der Mittelwert ihrer
Koordinaten. Aufgerufen von scripts/build_geocode.sh / .ps1.

GeoNames-Postal-Format (TSV, ohne Header):
    country, postal, place, admin1, code1, admin2, code2, admin3, code3, lat, lon, accuracy
"""

from __future__ import annotations

import csv
import sys
from collections import defaultdict


def main(src: str, out: str) -> None:
    points: dict[tuple[str, str], list[tuple[float, float]]] = defaultdict(list)
    with open(src, encoding="utf-8") as fh:
        for row in csv.reader(fh, delimiter="\t"):
            if len(row) < 11:
                continue
            country, plz, lat, lon = row[0], row[1], row[9], row[10]
            try:
                points[(country.strip().upper(), plz.strip())].append((float(lat), float(lon)))
            except ValueError:
                continue

    with open(out, "w", encoding="utf-8", newline="") as fh:
        writer = csv.writer(fh)
        writer.writerow(["country", "plz", "lat", "lon"])
        for (country, plz), pts in sorted(points.items()):
            lat = sum(p[0] for p in pts) / len(pts)
            lon = sum(p[1] for p in pts) / len(pts)
            writer.writerow([country, plz, round(lat, 6), round(lon, 6)])

    print(f"{out}: {len(points)} PLZ-Zentroide")


if __name__ == "__main__":
    if len(sys.argv) != 3:
        print("Usage: _geonames_centroids.py <src.txt> <out.csv>", file=sys.stderr)
        raise SystemExit(2)
    main(sys.argv[1], sys.argv[2])
