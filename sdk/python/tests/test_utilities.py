"""Unit tests for generated provider utilities."""

from pulumi_labs_nvidia_aicr._utilities import get_env_float, get_env_int


def test_get_env_int_returns_none_for_non_numeric_value(monkeypatch):
    monkeypatch.setenv("TEST_INT_VAR", "not-an-int")
    assert get_env_int("TEST_INT_VAR") is None


def test_get_env_float_returns_none_for_non_numeric_value(monkeypatch):
    monkeypatch.setenv("TEST_FLOAT_VAR", "not-a-float")
    assert get_env_float("TEST_FLOAT_VAR") is None


def test_get_env_int_parses_valid_integer(monkeypatch):
    monkeypatch.setenv("TEST_INT_VAR", "42")
    assert get_env_int("TEST_INT_VAR") == 42


def test_get_env_float_parses_valid_float(monkeypatch):
    monkeypatch.setenv("TEST_FLOAT_VAR", "3.14")
    assert get_env_float("TEST_FLOAT_VAR") == 3.14
