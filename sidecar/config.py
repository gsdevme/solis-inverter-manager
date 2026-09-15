"""Environment parsing and fail-fast validation for the sidecar.

Env var names and semantics mirror the Go ``internal/config`` so the two
processes read the same ``.env``. Validation collects every problem and raises a
single :class:`ConfigError`, matching the Go ``errors.Join`` fail-fast style.
"""

from __future__ import annotations

import os
import re
from dataclasses import dataclass
from pathlib import Path

_FIXTURE_DIR = Path(__file__).resolve().parent.parent / "docs" / "phase0" / "fixtures"

# Default seed: two complementary captures from the same Phase 0 session, resolved
# relative to the repo root so they work both from a checkout and from the Docker
# image (which copies docs/phase0/fixtures alongside the sidecar package).
#
# The full sweep deliberately skips the ranges the other snapshots already cover,
# so on its own it leaves holes — the whole battery block (33121-33180) reads zero,
# which makes a mock run look like a dead inverter. Seeding the comprehensive
# snapshot over it fills those holes. The two overlap on 22 registers and agree on
# every one, so the merged device stays internally consistent.
_DEFAULT_FIXTURES = (
    _FIXTURE_DIR / "live-snapshot-full-sweep.json",
    _FIXTURE_DIR / "live-snapshot-comprehensive.json",
)

_GO_DURATION_RE = re.compile(r"(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)")
_GO_DURATION_UNITS = {
    "ns": 1e-9,
    "us": 1e-6,
    "µs": 1e-6,
    "ms": 1e-3,
    "s": 1.0,
    "m": 60.0,
    "h": 3600.0,
}


class ConfigError(Exception):
    """Raised when required config is missing or invalid.

    Aggregates every validation failure so a single run surfaces all problems.
    """


@dataclass(frozen=True)
class Config:
    """Resolved, validated sidecar configuration."""

    mode: str
    inverter_ip: str
    inverter_serial: str  # secret: datalogger serial, never logged verbatim
    inverter_port: int
    socket_timeout: float  # seconds
    listen_host: str
    listen_port: int
    # One or more seed snapshots, applied in order: a later fixture wins where two
    # define the same register.
    mock_fixtures: tuple[str, ...]

    def redacted_serial(self) -> str:
        """The datalogger serial masked for logging."""
        return _redact(self.inverter_serial)


def parse_duration_or_seconds(value: str, default_seconds: float) -> float:
    """Parse a Go-style duration (``10s``, ``1m30s``, ``500ms``) or bare seconds.

    Mirrors the Go ``parseDurationOrSeconds`` helper: a bare integer/float is
    treated as seconds; otherwise a Go duration string is parsed. Returns the
    value in seconds. Raises :class:`ValueError` on an unparseable string.
    """
    value = value.strip()
    if value == "":
        return default_seconds
    try:
        return float(value)
    except ValueError:
        pass
    total, pos, matched = 0.0, 0, False
    negative = value.startswith("-")
    body = value[1:] if negative else value
    for m in _GO_DURATION_RE.finditer(body):
        if m.start() != pos:
            break
        total += float(m.group(1)) * _GO_DURATION_UNITS[m.group(2)]
        pos = m.end()
        matched = True
    if not matched or pos != len(body):
        raise ValueError(f"invalid duration or seconds: {value!r}")
    return -total if negative else total


def _parse_listen_addr(addr: str) -> tuple[str, int]:
    """Split a ``host:port`` / ``:port`` listen address into (host, port).

    An empty host binds all interfaces, matching Go's ``:8081`` convention.
    """
    host, sep, port = addr.rpartition(":")
    if sep == "":
        raise ValueError(f"SIDECAR_LISTEN_ADDR must be host:port, got {addr!r}")
    return host, int(port)


def _redact(secret: str) -> str:
    if secret == "":
        return ""
    if len(secret) <= 4:
        return "****"
    return secret[:2] + "****" + secret[-2:]


def load(env: dict[str, str] | None = None) -> Config:
    """Load and validate config from the environment (or ``env`` for tests)."""
    env = os.environ if env is None else env
    errs: list[str] = []

    mode = env.get("MODE", "").strip().lower()
    if mode == "":
        errs.append("MODE is required (mock|live)")
    elif mode not in ("mock", "live"):
        errs.append(f"MODE must be mock or live, got {mode!r}")

    inverter_ip = env.get("INVERTER_IP", "").strip()
    inverter_serial = env.get("INVERTER_SERIAL", "").strip()

    if mode == "live":
        if inverter_ip == "":
            errs.append("INVERTER_IP is required when MODE=live")
        if inverter_serial == "":
            errs.append("INVERTER_SERIAL is required when MODE=live")

    inverter_port = 8899
    port_raw = env.get("INVERTER_PORT", "").strip()
    if port_raw != "":
        try:
            inverter_port = int(port_raw)
        except ValueError:
            errs.append(f"INVERTER_PORT must be an integer, got {port_raw!r}")

    socket_timeout = 10.0
    try:
        socket_timeout = parse_duration_or_seconds(env.get("INVERTER_SOCKET_TIMEOUT", ""), 10.0)
        if socket_timeout <= 0:
            errs.append("INVERTER_SOCKET_TIMEOUT must be > 0")
    except ValueError as e:
        errs.append(str(e))

    listen_host, listen_port = "", 8081
    try:
        listen_host, listen_port = _parse_listen_addr(
            env.get("SIDECAR_LISTEN_ADDR", ":8081").strip() or ":8081"
        )
    except ValueError as e:
        errs.append(str(e))

    raw_fixtures = env.get("MOCK_FIXTURE", "").strip()
    if raw_fixtures:
        mock_fixtures = tuple(p.strip() for p in raw_fixtures.split(",") if p.strip())
    else:
        mock_fixtures = tuple(str(p) for p in _DEFAULT_FIXTURES)
    if mode == "mock":
        if not mock_fixtures:
            errs.append("MOCK_FIXTURE must name at least one fixture")
        for path in mock_fixtures:
            if not Path(path).is_file():
                errs.append(f"MOCK_FIXTURE not found: {path}")

    if errs:
        raise ConfigError("; ".join(errs))

    return Config(
        mode=mode,
        inverter_ip=inverter_ip,
        inverter_serial=inverter_serial,
        inverter_port=inverter_port,
        socket_timeout=socket_timeout,
        listen_host=listen_host,
        listen_port=listen_port,
        mock_fixtures=mock_fixtures,
    )
