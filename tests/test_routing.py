"""Parsing-/Status-Logik der HttpEngine ohne echten osrm-routed.

Wir umgehen __init__ (object.__new__) und füttern _parse mit OSRM-Standard-JSON-Fixtures.
"""

from backend.routing import (
    ERROR,
    NO_ROUTE,
    OK,
    SNAPPED_FAR,
    HttpEngine,
    RouteResult,
    route_pairs,
)


def make_engine(snap_limit_m: float = 50.0) -> HttpEngine:
    eng = object.__new__(HttpEngine)
    eng._snap_limit_m = snap_limit_m
    return eng


def test_parse_ok():
    eng = make_engine()
    res = eng._parse(
        {
            "code": "Ok",
            "routes": [{"distance": 12345.0, "duration": 678.0}],
            "waypoints": [{"distance": 5.0}, {"distance": 12.0}],
        }
    )
    assert res.status == OK
    assert res.distance_km == 12.35
    assert res.duration_min == 11.3
    assert res.snap_m == 12.0


def test_parse_snapped_far():
    eng = make_engine(snap_limit_m=50.0)
    res = eng._parse(
        {
            "code": "Ok",
            "routes": [{"distance": 1000.0, "duration": 60.0}],
            "waypoints": [{"distance": 5.0}, {"distance": 250.0}],
        }
    )
    assert res.status == SNAPPED_FAR
    assert res.snap_m == 250.0


def test_parse_no_route():
    eng = make_engine()
    res = eng._parse({"code": "NoRoute", "routes": [], "waypoints": []})
    assert res.status == NO_ROUTE
    assert res.distance_km is None


def test_parse_error_on_garbage():
    eng = make_engine()
    res = eng._parse({"code": "Ok", "routes": [{"distance": "x"}], "waypoints": []})
    assert res.status == ERROR


class _OrderEngine:
    """Stub-Engine: kodiert die origin-lat als Distanz, um Reihenfolge zu prüfen."""

    def route(self, origin, dest):
        return RouteResult(origin[0], 1.0, OK, snap_m=0.0)


def test_route_pairs_preserves_order():
    pairs = [((float(i), 0.0), (0.0, 0.0)) for i in range(50)]
    results = route_pairs(_OrderEngine(), pairs, workers=8)
    assert [r.distance_km for r in results] == [float(i) for i in range(50)]


def test_route_pairs_empty():
    assert route_pairs(_OrderEngine(), [], workers=8) == []
