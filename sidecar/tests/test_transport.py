"""LiveTransport failure handling against local fake dataloggers.

The Solarman stick sits behind a firewall that silently drops idle TCP state, so
the transport must treat "request sent, no reply" as a timeout that resets the
session; otherwise the half-open socket is reused forever and every call burns
the full socket timeout while holding the transport lock.
"""

from __future__ import annotations

import queue
import socket
import struct
import threading
import time

import pytest
from pysolarmanv5 import NoSocketAvailableError, V5FrameError
from umodbus.exceptions import ModbusError

from sidecar.errors import (
    FrameError,
    IllegalAddressError,
    RequestTimeoutError,
    TransportConnectionError,
)
from sidecar.transport import LiveTransport


class SilentLogger:
    """Accepts connections and reads each request but never replies."""

    def __init__(self) -> None:
        self.srv = socket.socket()
        self.srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.srv.bind(("127.0.0.1", 0))
        self.srv.listen(5)
        self.port = self.srv.getsockname()[1]
        self.accepts = 0
        self._conns: list[socket.socket] = []
        threading.Thread(target=self._serve, daemon=True).start()

    def _serve(self) -> None:
        while True:
            try:
                conn, _ = self.srv.accept()
            except OSError:
                return
            self.accepts += 1
            self._conns.append(conn)
            threading.Thread(target=self._swallow, args=(conn,), daemon=True).start()

    @staticmethod
    def _swallow(conn: socket.socket) -> None:
        try:
            while conn.recv(4096):
                pass
        except OSError:
            pass

    def close(self) -> None:
        for c in self._conns:
            c.close()
        self.srv.close()


@pytest.fixture
def silent_logger():
    logger = SilentLogger()
    yield logger
    logger.close()


def test_silent_peer_times_out_and_reconnects(silent_logger: SilentLogger) -> None:
    transport = LiveTransport(
        ip="127.0.0.1", serial="1234567890", port=silent_logger.port, socket_timeout=0.5
    )

    started = time.monotonic()
    with pytest.raises(RequestTimeoutError):
        transport.read_input(33022, 1)
    assert time.monotonic() - started < 5, "timeout must be bounded by socket_timeout"

    with pytest.raises(RequestTimeoutError):
        transport.read_input(33022, 1)
    assert silent_logger.accepts == 2, "a timed-out session must be dropped and re-dialled"


class DeadSocket:
    """Minimal stand-in for ``PySolarmanV5.sock`` so ``_disconnect`` can close it."""

    def __init__(self) -> None:
        self.closed = False

    def close(self) -> None:
        self.closed = True


class FakeSolarman:
    """Stand-in for a connected ``PySolarmanV5`` that fails every call."""

    def __init__(self, exc: Exception) -> None:
        self._exc = exc
        self.sock = DeadSocket()

    def read_input_registers(self, addr: int, count: int):
        raise self._exc

    def read_holding_registers(self, addr: int, count: int):
        raise self._exc

    def write_holding_register(self, addr: int, value: int):
        raise self._exc


class FakeModbusError(ModbusError):
    """Concrete umodbus error; the real subclasses take varying constructors."""


def _transport_with(exc: Exception) -> LiveTransport:
    """A LiveTransport with a pre-injected connection that raises ``exc``."""
    transport = LiveTransport(ip="127.0.0.1", serial="1234567890", port=8899, socket_timeout=0.5)
    transport._solarman = FakeSolarman(exc)
    return transport


@pytest.fixture
def closed_port() -> int:
    """A loopback port with nothing listening, so connects are refused."""
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    return port


@pytest.mark.parametrize(
    "exc,expected",
    [
        (queue.Empty(), RequestTimeoutError),
        (TimeoutError("timed out"), RequestTimeoutError),
        (socket.timeout("timed out"), RequestTimeoutError),
        (V5FrameError("checksum mismatch"), FrameError),
        (struct.error("unpack requires a buffer of 4 bytes"), FrameError),
        (FakeModbusError("illegal data address"), IllegalAddressError),
        (NoSocketAvailableError("no socket"), TransportConnectionError),
        (ConnectionResetError("peer reset"), TransportConnectionError),
        (OSError("broken pipe"), TransportConnectionError),
    ],
)
def test_transport_errors_map_and_drop_the_session(exc, expected) -> None:
    transport = _transport_with(exc)
    solarman = transport._solarman

    with pytest.raises(expected):
        transport.read_input(33022, 1)

    assert transport._solarman is None, "a failed call must mark the socket dead"
    assert solarman.sock.closed, "the dead socket must be closed, not leaked"


def test_health_is_false_when_the_probe_fails() -> None:
    """``health()`` never raises — a failing probe is reported, not propagated."""
    transport = _transport_with(V5FrameError("checksum mismatch"))
    assert transport.health() is False
    assert transport._solarman is None


def test_health_is_false_when_the_peer_is_silent(silent_logger: SilentLogger) -> None:
    transport = LiveTransport(
        ip="127.0.0.1", serial="1234567890", port=silent_logger.port, socket_timeout=0.5
    )
    started = time.monotonic()
    assert transport.health() is False
    assert time.monotonic() - started < 5, "the probe must be bounded by socket_timeout"


def test_health_is_false_when_the_connection_is_refused(closed_port: int) -> None:
    transport = LiveTransport(
        ip="127.0.0.1", serial="1234567890", port=closed_port, socket_timeout=0.5
    )
    assert transport.health() is False


def test_refused_connection_is_a_connection_error(closed_port: int) -> None:
    transport = LiveTransport(
        ip="127.0.0.1", serial="1234567890", port=closed_port, socket_timeout=0.5
    )
    with pytest.raises(TransportConnectionError):
        transport.read_input(33022, 1)
