# Requirements

> **Status: skeleton.** Stable, traceable requirement IDs are enumerated in a later
> phase. Prefix meanings: `SD` sidecar contract/client, `RM` register map/decode,
> `HA` MQTT/Home Assistant, `SC` scheduling, `CF` config, `LC` lifecycle/health,
> `TS` testing, `DP` deployment.

## Sidecar (`sidecar/`, `01-sidecar-contract.md`)

The thin Python transport sidecar (Phase 2, #19). See
[`01-sidecar-contract.md`](01-sidecar-contract.md) for the full wire contract.

- **REQ-SD-01** Localhost HTTP on `:8081` (matches Go `SIDECAR_URL` default),
  JSON request/response bodies. → `sidecar/http_api.py`, `sidecar/__main__.py`
- **REQ-SD-02** `POST /read_input` (fc04) and `POST /read_holding` (fc03) block
  reads of `{addr,count}` return raw `uint16` words `{addr,count,regs}` — no
  decode/scale/sign. → `sidecar/http_api.py`, `sidecar/transport.py`
- **REQ-SD-03** `POST /write_holding` (fc06) is an **unconditional** single-register
  write; the read-before-write guard is the Go manager's job, not the sidecar's.
  → `sidecar/http_api.py`, `sidecar/transport.py`
- **REQ-SD-04** `GET /health` reports `{ok, inverter_reachable, mode}` via a cheap
  reachability probe (never raises). → `sidecar/http_api.py`, `sidecar/transport.py`
- **REQ-SD-05** Single persistent socket, one global lock (exactly one Modbus frame
  in flight), reconnect-on-error between calls, per-call timeout from
  `INVERTER_SOCKET_TIMEOUT`. → `sidecar/transport.py`
- **REQ-SD-06** `MODE=mock` serves an in-memory register map seeded from a Phase 0
  fixture (no `pysolarmanv5`, no socket). → `sidecar/mock.py`, `sidecar/config.py`
- **REQ-SD-07** Input validation (`count` 1..125, `addr`/`value` 0..65535) and the
  error envelope `{"error":{code,message}}` with codes `bad_request`/`timeout`/
  `illegal_address`/`frame_error`/`connection_error`/`not_found`.
  → `sidecar/http_api.py`, `sidecar/errors.py`

> Note: the Go **client** (`internal/sidecarclient`) that consumes this contract,
> and wiring `/health` into `/readyz`, are a later phase (see `REQ-LC-*`).

## Register map / decode (`internal/inverter`, `02-register-map.md`)

- TODO **REQ-RM-\***: decode/encode per `docs/phase0/findings.md` (widths, word
  order, sign conventions, work-mode bitfield). → `inverter/*`

## MQTT & Home Assistant (`internal/mqtt`, `internal/homeassistant`, `internal/publisher`)

- TODO **REQ-HA-\***: discovery + state + availability, retained/QoS1, writable
  controls, and the **READ-BEFORE-WRITE write-guard** on every setpoint (flash-wear
  avoidance). → `homeassistant/*`, `mqtt/*`, `publisher/*`

## Scheduling (`internal/scheduler`, `04-polling-scheduling.md`)

- TODO **REQ-SC-\***: serialised poll loop; retries; readiness reporting; guarded
  writes. → `scheduler/*`

## Config (`internal/config`, `05-config.md`)

- **REQ-CF-01** All env vars in `05-config.md` bound with defaults. → `config.go`
- **REQ-CF-02** Fail-fast validation via `errors.Join`. → `config.go`
- **REQ-CF-03** Secrets (`INVERTER_SERIAL`, `MQTT_PASSWORD`) redacted in
  `String()`/`LogValue()`. → `config.go`
- **REQ-CF-04** `MODE` (`live`|`mock`): `live` requires inverter identity + MQTT
  broker; `mock` drops them. → `config.go`, `.env.dist`

## Lifecycle & health (`internal/server`, `cmd`, `main.go`, `06-lifecycle-health.md`)

- **REQ-LC-01** `/healthz` liveness always-ok while running. → `internal/server`
- **REQ-LC-02** `/readyz` `503` until ready, `200` once `SetReady(true)`. →
  `internal/server`
- TODO **REQ-LC-\***: not-ready after `FAILURE_THRESHOLD` failures; sidecar probe on
  `/readyz`; graceful `offline` publish + clean disconnect. → `cmd/serve.go`
- **REQ-LC-08** `cmd/main.go` reports errors to stderr and exits non-zero. →
  `cmd/main.go`

## Testing (`internal/mock`, `features`, `07-testing.md`)

- TODO **REQ-TS-\***: mock sidecar; unit tests; godog scenarios; `golangci-lint` +
  `go vet`. Scaffold ships `features/health.feature`. → `*_test.go`, `features/*`

## Deployment (`08-deployment.md`)

- TODO **REQ-DP-\***: two-container pod; manager + sidecar images; CI/CD; module
  path `github.com/gsdevme/solis-inverter-manager`, `go 1.27`. → `go.mod`, CI
