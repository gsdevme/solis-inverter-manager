"""HTTP API tests against the fixture-seeded MockTransport.

Each test starts the real :class:`SidecarHTTPServer` on an ephemeral port with a
:class:`MockTransport` seeded from the Phase 0 comprehensive snapshot (which
carries both the RTC input block at 33022 and the setpoint holdings at 43141),
then exercises the endpoints over the wire.
"""

from __future__ import annotations

import json
import threading
import urllib.error
import urllib.request
from pathlib import Path

import pytest

from sidecar.http_api import Handler, SidecarHTTPServer
from sidecar.mock import MockTransport

FIXTURE = (
    Path(__file__).resolve().parent.parent.parent
    / "docs"
    / "phase0"
    / "fixtures"
    / "live-snapshot-comprehensive.json"
)


@pytest.fixture()
def base_url():
    transport = MockTransport(str(FIXTURE))
    server = SidecarHTTPServer(("127.0.0.1", 0), Handler, transport, "mock")
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    host, port = server.server_address
    try:
        yield f"http://{host}:{port}"
    finally:
        server.shutdown()
        server.server_close()


def _post(base_url: str, path: str, payload: dict):
    req = urllib.request.Request(
        base_url + path,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    return _send(req)


def _get(base_url: str, path: str):
    return _send(urllib.request.Request(base_url + path, method="GET"))


def _send(req):
    try:
        with urllib.request.urlopen(req) as resp:
            return resp.status, json.loads(resp.read())
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read())


def test_health(base_url):
    status, body = _get(base_url, "/health")
    assert status == 200
    assert body == {"ok": True, "inverter_reachable": True, "mode": "mock"}


def test_read_input_returns_fixture_words(base_url):
    status, body = _post(base_url, "/read_input", {"addr": 33022, "count": 6})
    assert status == 200
    # RTC block: year, month, day, hour, minute, second.
    assert body == {"addr": 33022, "count": 6, "regs": [26, 9, 8, 15, 38, 59]}


def test_read_holding_returns_fixture_words(base_url):
    status, body = _post(base_url, "/read_holding", {"addr": 43141, "count": 3})
    assert status == 200
    assert body == {"addr": 43141, "count": 3, "regs": [350, 600, 23]}


def test_read_beyond_fixture_zero_fills(base_url):
    status, body = _post(base_url, "/read_input", {"addr": 39000, "count": 2})
    assert status == 200
    assert body["regs"] == [0, 0]


def test_write_then_read_round_trip(base_url):
    status, body = _post(base_url, "/write_holding", {"addr": 43141, "value": 340})
    assert status == 200
    assert body == {"addr": 43141, "value": 340, "ok": True}

    status, body = _post(base_url, "/read_holding", {"addr": 43141, "count": 1})
    assert status == 200
    assert body["regs"] == [340]


@pytest.mark.parametrize(
    "path,payload",
    [
        ("/read_input", {"addr": 33022, "count": 0}),
        ("/read_input", {"addr": 33022, "count": 126}),
        ("/read_input", {"addr": 70000, "count": 1}),
        ("/read_input", {"addr": 33022}),
        ("/write_holding", {"addr": 43141, "value": 70000}),
        ("/write_holding", {"addr": 43141, "value": -1}),
        ("/write_holding", {"addr": 43141, "value": True}),
    ],
)
def test_validation_returns_bad_request(base_url, path, payload):
    status, body = _post(base_url, path, payload)
    assert status == 400
    assert body["error"]["code"] == "bad_request"
    assert isinstance(body["error"]["message"], str)


def test_invalid_json_body(base_url):
    req = urllib.request.Request(base_url + "/read_input", data=b"{not json", method="POST")
    status, body = _send(req)
    assert status == 400
    assert body["error"]["code"] == "bad_request"


def test_unknown_route_404(base_url):
    status, body = _post(base_url, "/nope", {})
    assert status == 404
    assert body["error"]["code"] == "not_found"

    status, body = _get(base_url, "/nope")
    assert status == 404
    assert body["error"]["code"] == "not_found"
