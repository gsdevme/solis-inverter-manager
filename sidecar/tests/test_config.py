"""Config defaults, fail-fast validation, redaction and duration parsing.

``load()`` takes the environment as an argument, so every case here passes an
explicit mapping rather than mutating ``os.environ``; the single test that
covers the ``env=None`` branch uses ``monkeypatch``. Expected defaults are the
ones published in ``docs/specs/01-sidecar-contract.md`` — the Go
``internal/config`` reads the same ``.env``, so drift here is drift between the
two processes.
"""

from __future__ import annotations

import pytest

from sidecar.config import (
    _DEFAULT_FIXTURES,
    ConfigError,
    _redact,
    load,
    parse_duration_or_seconds,
)

LIVE_ENV = {"MODE": "live", "INVERTER_IP": "10.0.0.5", "INVERTER_SERIAL": "1234567890"}


def test_defaults_for_mock_mode():
    cfg = load({"MODE": "mock"})
    assert cfg.mode == "mock"
    assert cfg.inverter_ip == ""
    assert cfg.inverter_serial == ""
    assert cfg.inverter_port == 8899
    assert cfg.socket_timeout == 10.0
    assert (cfg.listen_host, cfg.listen_port) == ("", 8081)
    assert cfg.mock_fixtures == tuple(str(p) for p in _DEFAULT_FIXTURES)


def test_mode_is_required():
    with pytest.raises(ConfigError) as e:
        load({})
    assert "MODE is required" in str(e.value)


def test_mode_must_be_mock_or_live():
    with pytest.raises(ConfigError) as e:
        load({"MODE": "staging"})
    assert "MODE must be mock or live" in str(e.value)


@pytest.mark.parametrize("raw", ["mock", "MOCK", "  Mock  "])
def test_mode_is_normalised(raw):
    assert load({"MODE": raw}).mode == "mock"


def test_live_mode_requires_ip_and_serial():
    with pytest.raises(ConfigError) as e:
        load({"MODE": "live"})
    assert "INVERTER_IP is required when MODE=live" in str(e.value)
    assert "INVERTER_SERIAL is required when MODE=live" in str(e.value)


def test_every_problem_is_reported_at_once():
    """Validation is fail-fast but aggregated, mirroring Go's errors.Join."""
    with pytest.raises(ConfigError) as e:
        load(
            {
                "MODE": "live",
                "INVERTER_PORT": "eight-eight-nine-nine",
                "INVERTER_SOCKET_TIMEOUT": "soon",
                "SIDECAR_LISTEN_ADDR": "8081",
            }
        )
    message = str(e.value)
    for expected in (
        "INVERTER_IP is required",
        "INVERTER_SERIAL is required",
        "INVERTER_PORT must be an integer",
        "invalid duration or seconds",
        "SIDECAR_LISTEN_ADDR must be host:port",
    ):
        assert expected in message, f"missing {expected!r} from aggregated error: {message}"
    assert len(message.split("; ")) == 5


def test_overrides_are_honoured(tmp_path):
    fixture = tmp_path / "seed.json"
    fixture.write_text("{}")
    cfg = load(
        {
            "MODE": "mock",
            "INVERTER_PORT": "9999",
            "INVERTER_SOCKET_TIMEOUT": "1m30s",
            "SIDECAR_LISTEN_ADDR": "127.0.0.1:9090",
            "MOCK_FIXTURE": str(fixture),
        }
    )
    assert cfg.inverter_port == 9999
    assert cfg.socket_timeout == 90.0
    assert (cfg.listen_host, cfg.listen_port) == ("127.0.0.1", 9090)
    assert cfg.mock_fixtures == (str(fixture),)


def test_invalid_inverter_port():
    with pytest.raises(ConfigError) as e:
        load({"MODE": "mock", "INVERTER_PORT": "1.5"})
    assert "INVERTER_PORT must be an integer" in str(e.value)


@pytest.mark.parametrize("addr", ["8081", "no-port-here"])
def test_invalid_listen_addr(addr):
    with pytest.raises(ConfigError) as e:
        load({"MODE": "mock", "SIDECAR_LISTEN_ADDR": addr})
    assert "SIDECAR_LISTEN_ADDR must be host:port" in str(e.value)


@pytest.mark.parametrize("raw", ["0", "-5s"])
def test_socket_timeout_must_be_positive(raw):
    with pytest.raises(ConfigError) as e:
        load({"MODE": "mock", "INVERTER_SOCKET_TIMEOUT": raw})
    assert "INVERTER_SOCKET_TIMEOUT must be > 0" in str(e.value)


def test_default_fixtures_resolve_to_complementary_phase0_captures():
    names = [p.name for p in _DEFAULT_FIXTURES]
    assert names == ["live-snapshot-full-sweep.json", "live-snapshot-comprehensive.json"]
    for path in _DEFAULT_FIXTURES:
        assert path.is_file(), f"the default MOCK_FIXTURE {path.name} must ship with the repo"


def test_mock_fixture_accepts_a_comma_separated_list(tmp_path):
    a = tmp_path / "a.json"
    b = tmp_path / "b.json"
    for f in (a, b):
        f.write_text("{}")
    cfg = load({"MODE": "mock", "MOCK_FIXTURE": f"{a}, {b}"})
    assert cfg.mock_fixtures == (str(a), str(b))


def test_every_listed_mock_fixture_is_validated(tmp_path):
    present = tmp_path / "present.json"
    present.write_text("{}")
    missing = tmp_path / "nope.json"
    with pytest.raises(ConfigError) as e:
        load({"MODE": "mock", "MOCK_FIXTURE": f"{present},{missing}"})
    assert f"MOCK_FIXTURE not found: {missing}" in str(e.value)


def test_missing_mock_fixture_fails(tmp_path):
    missing = tmp_path / "nope.json"
    with pytest.raises(ConfigError) as e:
        load({"MODE": "mock", "MOCK_FIXTURE": str(missing)})
    assert f"MOCK_FIXTURE not found: {missing}" in str(e.value)


def test_mock_fixture_is_not_validated_in_live_mode(tmp_path):
    missing = str(tmp_path / "nope.json")
    cfg = load({**LIVE_ENV, "MOCK_FIXTURE": missing})
    assert cfg.mock_fixtures == (missing,)


def test_load_falls_back_to_the_process_environment(monkeypatch):
    monkeypatch.setenv("MODE", "live")
    monkeypatch.setenv("INVERTER_IP", "10.0.0.5")
    monkeypatch.setenv("INVERTER_SERIAL", "1234567890")
    monkeypatch.delenv("INVERTER_PORT", raising=False)
    cfg = load()
    assert (cfg.mode, cfg.inverter_ip, cfg.inverter_port) == ("live", "10.0.0.5", 8899)


def test_redacted_serial_masks_the_datalogger_serial():
    assert load(LIVE_ENV).redacted_serial() == "12****90"


@pytest.mark.parametrize(
    "secret,expected",
    [("", ""), ("1", "****"), ("1234", "****"), ("12345", "12****45"), ("1234567890", "12****90")],
)
def test_redact(secret, expected):
    assert _redact(secret) == expected


@pytest.mark.parametrize(
    "raw,expected",
    [
        ("30s", 30.0),
        ("1m30s", 90.0),
        ("500ms", 0.5),
        ("1h", 3600.0),
        ("1h2m3s", 3723.0),
        ("1.5s", 1.5),
        ("100us", 0.0001),
        ("-30s", -30.0),
        ("10", 10.0),
        ("2.5", 2.5),
        ("  10s  ", 10.0),
    ],
)
def test_parse_duration_or_seconds(raw, expected):
    assert parse_duration_or_seconds(raw, 10.0) == pytest.approx(expected)


@pytest.mark.parametrize("raw", ["", "   "])
def test_parse_duration_falls_back_to_the_default(raw):
    assert parse_duration_or_seconds(raw, 7.5) == 7.5


@pytest.mark.parametrize("raw", ["soon", "10x", "1m30", "s10", "30 s", "m"])
def test_parse_duration_rejects_garbage(raw):
    with pytest.raises(ValueError, match="invalid duration or seconds"):
        parse_duration_or_seconds(raw, 10.0)
