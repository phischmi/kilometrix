"""Signierte Tokens: mint/verify, falsche Signatur, Ablauf."""

import pytest

from backend.tokens import TokenError, mint, verify

SECRET = "test-secret-xyz"


def test_mint_verify_roundtrip():
    token = mint(SECRET, "philipp", 3600)
    payload = verify(SECRET, token)
    assert payload["sub"] == "philipp"
    assert payload["exp"] > payload["iat"]


def test_wrong_secret_rejected():
    token = mint(SECRET, "philipp", 3600)
    with pytest.raises(TokenError):
        verify("anderes-secret", token)


def test_tampered_rejected():
    token = mint(SECRET, "philipp", 3600)
    with pytest.raises(TokenError):
        verify(SECRET, token[:-2] + ("aa" if not token.endswith("aa") else "bb"))


def test_expired_rejected():
    with pytest.raises(TokenError):
        verify(SECRET, mint(SECRET, "philipp", -1))


def test_empty_secret_rejected():
    with pytest.raises(TokenError):
        verify("", mint(SECRET, "philipp", 3600))


def test_malformed_rejected():
    with pytest.raises(TokenError):
        verify(SECRET, "not-a-token")
