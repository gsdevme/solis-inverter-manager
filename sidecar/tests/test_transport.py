"""LiveTransport failure handling against local fake dataloggers.

The Solarman stick sits behind a firewall that silently drops idle TCP state, so
the transport must treat "request sent, no reply" as a timeout that resets the
session; otherwise the half-open socket is reused forever and every call burns
the full socket timeout while holding the transport lock.
"""

from __future__ import annotations

import socket
import threading
import time

import pytest

from sidecar.errors import RequestTimeoutError
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
