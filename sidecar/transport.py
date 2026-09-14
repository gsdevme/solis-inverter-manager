"""Live Solarman V5 / Modbus transport.

:class:`LiveTransport` wraps :class:`pysolarmanv5.PySolarmanV5` and carries
forward the proven behaviour of the legacy ``solis/modbus.py``: a single
persistent socket, every request serialized behind one lock (exactly one Modbus
frame in flight), and reconnect-on-error — on any transport exception the socket
is marked dead and reconnected lazily on the next call. Unlike the legacy code it
does no decode, scale or sign work: reads return raw uint16 words and
``write_holding`` is unconditional (the read-before-write guard is the Go
manager's job).
"""

from __future__ import annotations

import queue
import socket
import struct
import threading

from pysolarmanv5 import NoSocketAvailableError, PySolarmanV5, V5FrameError

from .errors import (
    FrameError,
    IllegalAddressError,
    RequestTimeoutError,
    TransportConnectionError,
)

try:  # umodbus ships with pysolarmanv5; guard the import defensively.
    from umodbus.exceptions import ModbusError as _ModbusError
except ImportError:  # pragma: no cover - pysolarmanv5 always brings umodbus
    _ModbusError = ()

# A cheap register known to exist on the RHI-3.6K (system-time year, input fc04),
# used only as a reachability probe for /health.
_HEALTH_PROBE_ADDR = 33022


class LiveTransport:
    """Persistent, serialized Solarman V5 transport to the datalogger."""

    def __init__(self, ip: str, serial: str, port: int, socket_timeout: float):
        self._ip = ip
        self._serial = int(serial)
        self._port = port
        self._socket_timeout = socket_timeout
        self._lock = threading.Lock()
        self._solarman: PySolarmanV5 | None = None

    def read_input(self, addr: int, count: int) -> list[int]:
        with self._lock:
            return self._call(lambda s: s.read_input_registers(addr, count))

    def read_holding(self, addr: int, count: int) -> list[int]:
        with self._lock:
            return self._call(lambda s: s.read_holding_registers(addr, count))

    def write_holding(self, addr: int, value: int) -> int:
        with self._lock:
            return self._call(lambda s: s.write_holding_register(addr, value))

    def health(self) -> bool:
        """Probe reachability with a single-register read. Never raises."""
        with self._lock:
            try:
                self._call(lambda s: s.read_input_registers(_HEALTH_PROBE_ADDR, 1))
                return True
            except Exception:
                return False

    def _call(self, fn):
        """Run one Solarman call, mapping transport failures to sidecar errors.

        On any failure the socket is disconnected so the next call reconnects.
        """
        solarman = self._connect_if_needed()
        try:
            return fn(solarman)
        except (socket.timeout, TimeoutError, queue.Empty) as e:
            # pysolarmanv5 3.x surfaces "request sent, no reply" as the bare
            # queue.Empty from its reader queue rather than TimeoutError.
            self._disconnect()
            raise RequestTimeoutError(str(e) or "no reply within socket timeout") from e
        except V5FrameError as e:
            self._disconnect()
            raise FrameError(str(e)) from e
        except struct.error as e:
            self._disconnect()
            raise FrameError(str(e)) from e
        except _ModbusError as e:  # type: ignore[misc]
            self._disconnect()
            raise IllegalAddressError(str(e)) from e
        except NoSocketAvailableError as e:
            self._disconnect()
            raise TransportConnectionError(str(e)) from e
        except (OSError, ConnectionError) as e:
            self._disconnect()
            raise TransportConnectionError(str(e)) from e

    def _connect_if_needed(self) -> PySolarmanV5:
        if self._solarman is not None:
            return self._solarman
        try:
            self._solarman = PySolarmanV5(
                address=self._ip,
                serial=self._serial,
                port=self._port,
                mb_slave_id=1,
                verbose=False,
                socket_timeout=self._socket_timeout,
            )
        except NoSocketAvailableError as e:
            raise TransportConnectionError(str(e)) from e
        except (OSError, ConnectionError) as e:
            raise TransportConnectionError(str(e)) from e
        return self._solarman

    def _disconnect(self) -> None:
        solarman, self._solarman = self._solarman, None
        if solarman is None:
            return
        try:
            solarman.sock.close()
        except Exception:
            pass
