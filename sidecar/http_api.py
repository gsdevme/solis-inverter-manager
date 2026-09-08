"""HTTP/JSON front end for the register transport.

A :class:`http.server.BaseHTTPRequestHandler` subclass that routes, parses and
validates requests, calls the transport, and maps :class:`sidecar.errors`
exceptions onto the JSON error envelope documented in
``docs/specs/01-sidecar-contract.md``. The same handler serves both the live and
mock transports — the transport interface is identical.

The transport and mode are read from the serving ``http.server`` instance
(:class:`SidecarHTTPServer`), so the handler class stays stateless.
"""

from __future__ import annotations

import json
import logging
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from .errors import BadRequestError, NotFoundError, SidecarError

logger = logging.getLogger("sidecar.http")

# Modbus per-request register-count cap and the uint16 value range.
_MAX_COUNT = 125
_U16_MAX = 0xFFFF


class SidecarHTTPServer(ThreadingHTTPServer):
    """Threading HTTP server holding the transport and mode for handlers.

    Threading lets ``/health`` and register calls be accepted concurrently; the
    transport's own lock still guarantees exactly one Modbus frame in flight.
    """

    daemon_threads = True

    def __init__(self, address, handler, transport, mode: str):
        super().__init__(address, handler)
        self.transport = transport
        self.mode = mode


class Handler(BaseHTTPRequestHandler):
    """Routes the sidecar's REST-ish endpoints to the transport."""

    server_version = "solis-sidecar/1.0"

    def do_GET(self) -> None:  # noqa: N802 - required by BaseHTTPRequestHandler
        try:
            if self.path == "/health":
                self._handle_health()
            else:
                raise NotFoundError(f"unknown route: GET {self.path}")
        except SidecarError as e:
            self._send_error(e)
        except Exception as e:  # pragma: no cover - defensive catch-all
            logger.exception("unhandled error")
            self._send_error(SidecarError(str(e)))

    def do_POST(self) -> None:  # noqa: N802 - required by BaseHTTPRequestHandler
        try:
            if self.path == "/read_input":
                self._handle_read(self.server.transport.read_input)
            elif self.path == "/read_holding":
                self._handle_read(self.server.transport.read_holding)
            elif self.path == "/write_holding":
                self._handle_write()
            else:
                raise NotFoundError(f"unknown route: POST {self.path}")
        except SidecarError as e:
            self._send_error(e)
        except Exception as e:  # pragma: no cover - defensive catch-all
            logger.exception("unhandled error")
            self._send_error(SidecarError(str(e)))

    def _handle_health(self) -> None:
        self._send_json(
            200,
            {
                "ok": True,
                "inverter_reachable": bool(self.server.transport.health()),
                "mode": self.server.mode,
            },
        )

    def _handle_read(self, read_fn) -> None:
        body = self._read_body()
        addr = _require_int(body, "addr", 0, _U16_MAX)
        count = _require_int(body, "count", 1, _MAX_COUNT)
        regs = read_fn(addr, count)
        self._send_json(200, {"addr": addr, "count": count, "regs": list(regs)})

    def _handle_write(self) -> None:
        body = self._read_body()
        addr = _require_int(body, "addr", 0, _U16_MAX)
        value = _require_int(body, "value", 0, _U16_MAX)
        self.server.transport.write_holding(addr, value)
        self._send_json(200, {"addr": addr, "value": value, "ok": True})

    def _read_body(self) -> dict:
        length = int(self.headers.get("Content-Length", 0) or 0)
        raw = self.rfile.read(length) if length > 0 else b""
        try:
            body = json.loads(raw or b"{}")
        except json.JSONDecodeError as e:
            raise BadRequestError(f"invalid JSON body: {e}") from e
        if not isinstance(body, dict):
            raise BadRequestError("request body must be a JSON object")
        return body

    def _send_json(self, status: int, payload: dict) -> None:
        data = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def _send_error(self, err: SidecarError) -> None:
        self._send_json(err.status, {"error": {"code": err.code, "message": err.message}})

    def log_message(self, fmt: str, *args) -> None:
        """Route access logging through the structured logger."""
        logger.info("%s - %s", self.address_string(), fmt % args)


def _require_int(body: dict, key: str, lo: int, hi: int) -> int:
    if key not in body:
        raise BadRequestError(f"missing field: {key!r}")
    value = body[key]
    if isinstance(value, bool) or not isinstance(value, int):
        raise BadRequestError(f"{key!r} must be an integer")
    if value < lo or value > hi:
        raise BadRequestError(f"{key!r} must be in {lo}..{hi}, got {value}")
    return value
