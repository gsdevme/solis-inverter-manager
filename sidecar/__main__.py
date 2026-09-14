"""Sidecar entrypoint: ``python -m sidecar``.

Loads config, picks the live or mock transport, and serves the HTTP API on the
configured address until interrupted. Logs structured lines to stdout with the
datalogger serial redacted.
"""

from __future__ import annotations

import logging
import sys

from .config import Config, ConfigError, load
from .http_api import Handler, SidecarHTTPServer


def _configure_logging() -> None:
    logging.basicConfig(
        level=logging.INFO,
        stream=sys.stdout,
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )


def _build_transport(cfg: Config):
    if cfg.mode == "mock":
        from .mock import MockTransport

        return MockTransport(cfg.mock_fixture)
    from .transport import LiveTransport

    return LiveTransport(
        ip=cfg.inverter_ip,
        serial=cfg.inverter_serial,
        port=cfg.inverter_port,
        socket_timeout=cfg.socket_timeout,
    )


def main() -> int:
    _configure_logging()
    log = logging.getLogger("sidecar")
    try:
        cfg = load()
    except ConfigError as e:
        log.error("config error: %s", e)
        return 1

    transport = _build_transport(cfg)
    server = SidecarHTTPServer((cfg.listen_host, cfg.listen_port), Handler, transport, cfg.mode)

    bind = f"{cfg.listen_host or '0.0.0.0'}:{cfg.listen_port}"
    if cfg.mode == "mock":
        log.info("starting sidecar mode=mock listen=%s fixture=%s", bind, cfg.mock_fixture)
    else:
        log.info(
            "starting sidecar mode=live listen=%s inverter=%s:%d serial=%s timeout=%ss",
            bind,
            cfg.inverter_ip,
            cfg.inverter_port,
            cfg.redacted_serial(),
            cfg.socket_timeout,
        )

    try:
        server.serve_forever()
    except KeyboardInterrupt:
        log.info("shutting down")
    finally:
        server.shutdown()
        server.server_close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
