"""Signierte Zugangstokens mit TTL — stateless (HMAC-SHA256, nur stdlib).

Ein Token kodiert `{sub, iat, exp}` und ist mit `AUTH_SECRET` signiert. Keine Datenbank,
keine Revocation-Liste (für 1–3 Nutzer ausreichend; Widerruf via Secret-Rotation).

CLI:
    python -m backend.tokens create --name philipp --days 90
    python -m backend.tokens verify <token>
"""

from __future__ import annotations

import base64
import hashlib
import hmac
import json
import time


class TokenError(Exception):
    pass


def _b64e(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode()


def _b64d(s: str) -> bytes:
    return base64.urlsafe_b64decode(s + "=" * (-len(s) % 4))


def _sign(secret: str, body: str) -> str:
    return _b64e(hmac.new(secret.encode(), body.encode(), hashlib.sha256).digest())


def mint(secret: str, name: str, ttl_seconds: int) -> str:
    now = int(time.time())
    payload = {"sub": name, "iat": now, "exp": now + int(ttl_seconds)}
    body = _b64e(json.dumps(payload, separators=(",", ":"), sort_keys=True).encode())
    return f"{body}.{_sign(secret, body)}"


def verify(secret: str, token: str) -> dict:
    if not secret:
        raise TokenError("AUTH_SECRET nicht gesetzt")
    try:
        body, sig = token.split(".", 1)
    except ValueError:
        raise TokenError("Token-Format ungültig")
    if not hmac.compare_digest(sig, _sign(secret, body)):
        raise TokenError("Signatur ungültig")
    try:
        payload = json.loads(_b64d(body))
    except Exception:
        raise TokenError("Payload ungültig")
    if int(payload.get("exp", 0)) < time.time():
        raise TokenError("Token abgelaufen")
    return payload


def _main() -> None:
    import argparse
    import sys
    from datetime import datetime, timezone

    from backend.config import get_settings

    ap = argparse.ArgumentParser(prog="python -m backend.tokens")
    sub = ap.add_subparsers(dest="cmd", required=True)
    c = sub.add_parser("create", help="Neues Token erzeugen")
    c.add_argument("--name", required=True, help="Name/Empfänger des Tokens")
    c.add_argument("--days", type=float, default=90.0, help="Gültigkeit in Tagen (TTL)")
    v = sub.add_parser("verify", help="Token prüfen")
    v.add_argument("token")
    args = ap.parse_args()

    secret = get_settings().auth_secret
    if not secret:
        sys.exit("Fehler: AUTH_SECRET ist nicht gesetzt (in .env / Umgebung).")

    if args.cmd == "create":
        token = mint(secret, args.name, int(args.days * 86400))
        exp = datetime.fromtimestamp(json.loads(_b64d(token.split(".")[0]))["exp"], tz=timezone.utc)
        print(token)
        print(f"# name={args.name}  gültig bis {exp:%Y-%m-%d %H:%M UTC}", file=sys.stderr)
    else:
        try:
            payload = verify(secret, args.token)
        except TokenError as e:
            sys.exit(f"ungültig: {e}")
        print(json.dumps(payload))


if __name__ == "__main__":
    _main()
