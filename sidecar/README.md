# Sidecar — thin Solarman V5 transport

A dumb Modbus/Solarman-V5 transport for the Go manager. It exposes generic
register RPCs over localhost HTTP and does **no** decode, scale, sign, MQTT or
Home Assistant work — the Go manager owns all of that. Reads return raw uint16
words; `write_holding` is unconditional (the read-before-write guard is the
manager's job).

The wire contract, error model and behaviour guarantees are the source of truth
in [`../docs/specs/01-sidecar-contract.md`](../docs/specs/01-sidecar-contract.md).

## Endpoints

| Method | Path             | Request         | Success                                     |
|--------|------------------|-----------------|---------------------------------------------|
| POST   | `/read_input`    | `{addr, count}` | `{addr, count, regs:[u16,...]}` (fc04)      |
| POST   | `/read_holding`  | `{addr, count}` | `{addr, count, regs:[u16,...]}` (fc03)      |
| POST   | `/write_holding` | `{addr, value}` | `{addr, value, ok:true}` (fc06)             |
| GET    | `/health`        | —               | `{ok, inverter_reachable, mode}`            |

## Run

```sh
# Mock: serve the Phase 0 fixtures, no hardware needed.
MODE=mock python -m sidecar            # listens on :8081

# Live: talk to the real datalogger.
MODE=live INVERTER_IP=192.168.1.50 INVERTER_SERIAL=1234567890 python -m sidecar
```

Config env vars mirror the Go `internal/config` names: `MODE` (`mock|live`,
required), `INVERTER_IP`, `INVERTER_SERIAL` (datalogger serial, secret),
`INVERTER_PORT` (default 8899), `INVERTER_SOCKET_TIMEOUT` (bare seconds or a Go
duration, default 10s), `SIDECAR_LISTEN_ADDR` (default `:8081`), `MOCK_FIXTURE`
(default the bundled full-sweep snapshot).

## Develop

```sh
pip install -r requirements-dev.txt
pytest                                  # or: make sidecar-test
ruff check . && ruff format --check .   # or: make sidecar-lint
```

## Docker

Build from the repo root so the fixtures are copied alongside the package:

```sh
docker build -f sidecar/Dockerfile -t solis-sidecar .
```
