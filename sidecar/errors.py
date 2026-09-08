"""Sidecar error taxonomy shared by the transports and the HTTP layer.

Each error carries the wire ``code`` and HTTP ``status`` documented in
``docs/specs/01-sidecar-contract.md``. Keeping them here (rather than in
``transport.py``) lets ``mock.py`` and ``http_api.py`` import the taxonomy
without pulling in ``pysolarmanv5``.
"""

from __future__ import annotations


class SidecarError(Exception):
    """Base class for every error surfaced on the wire.

    ``code`` and ``status`` map onto the JSON error envelope
    ``{"error": {"code", "message"}}`` and the HTTP status line respectively.
    """

    code = "internal_error"
    status = 500

    def __init__(self, message: str):
        super().__init__(message)
        self.message = message


class BadRequestError(SidecarError):
    """Malformed body or out-of-range argument."""

    code = "bad_request"
    status = 400


class RequestTimeoutError(SidecarError):
    """Socket timed out waiting on the datalogger (INVERTER_SOCKET_TIMEOUT)."""

    code = "timeout"
    status = 504


class IllegalAddressError(SidecarError):
    """The inverter returned a Modbus exception (e.g. illegal data address)."""

    code = "illegal_address"
    status = 502


class FrameError(SidecarError):
    """A Solarman V5 frame or CRC error while decoding the response."""

    code = "frame_error"
    status = 502


class TransportConnectionError(SidecarError):
    """The socket is dead or a fresh connection to the datalogger failed."""

    code = "connection_error"
    status = 503


class NotFoundError(SidecarError):
    """No route matched the request."""

    code = "not_found"
    status = 404
