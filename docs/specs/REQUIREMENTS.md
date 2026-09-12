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

Phase 4 (#20/#21) is **read-only** discovery + state + availability. See
[`03-mqtt-ha-discovery.md`](03-mqtt-ha-discovery.md) for the full topic scheme,
payload shapes and the 33-entity table.

- **REQ-HA-01** Discovery: one **retained**, QoS-1 config per entity at
  `<HA_DISCOVERY_PREFIX>/<component>/<serial>_<key>/config` (object_id form);
  identical shared device block (`identifiers:[<serial>]`, `manufacturer:"Solis"`,
  `model:"RHI-3.6K-48ES-5G"`, `name:"Solis Inverter"`); `~` = base topic and the
  only abbreviated key; `unique_id` = `object_id` = `<serial>_<key>`.
  → `homeassistant/discovery.go`, `homeassistant/entities.go`, `publisher/publisher.go`
- **REQ-HA-02** State: a single **retained**, QoS-1 JSON document at `<base>/state`;
  every entity reads it via `value_template {{ value_json.<key> }}` (binary_sensor
  via `{{ 'ON' if value_json.<key> else 'OFF' }}`); the state DTO's json tags equal
  the 33-entity key set exactly. `rtc` is RFC3339; `rtc_drift` is seconds.
  → `homeassistant/state.go`, `homeassistant/entities.go`
- **REQ-HA-03** Entity classes per the `03` table; **daily** energy counters use
  `state_class: total`, **lifetime** counters `total_increasing`; signed
  battery/grid power is one signed entity (not split), with a derived
  `battery_charging` binary_sensor. → `homeassistant/entities.go`
- **REQ-HA-04** Availability + LWT: retained `offline` LWT on `<base>/availability`
  at QoS 1; `online` retained on connect; explicit `offline` retained + clean
  disconnect on graceful shutdown (clean disconnect suppresses the Will).
  → `mqtt/client.go`, `publisher/publisher.go`, `cmd/serve.go`
- **REQ-HA-05** Reconnect republish: on every (re)connection (`OnConnectionUp`),
  republish discovery + availability(`online`) + last cached state.
  → `cmd/serve.go`, `mqtt/client.go`
- **REQ-HA-06** MODE gating: publish only when an MQTT broker URL is configured;
  `live` requires it; `mock` without a broker runs the same `Collect` unit and logs
  decoded telemetry. Interim poll cadence = `POLL_INTERVAL` (default 60s; real
  scheduler is Phase 6). → `cmd/serve.go`, `config.go`
- **REQ-HA-07** Read-only in Phase 4; writable controls (`number`/`switch`/`select`)
  and the **READ-BEFORE-WRITE write-guard** on every setpoint (flash-wear avoidance)
  are **Phase 5** — see the CRITICAL write-guard section in
  [`03-mqtt-ha-discovery.md`](03-mqtt-ha-discovery.md). → `publisher/*` (Phase 5)

Phase 5 (#22) adds the **write half**: native HA controls with a guarded write path.
See the write-path sections of
[`03-mqtt-ha-discovery.md`](03-mqtt-ha-discovery.md).

- **REQ-HA-08** Number amp controls: `set_charge_current` (`43141`) and
  `set_discharge_current` (`43142`), HA `number`, 0–60 A step 0.1, U16 `÷10` A,
  written via fc06 behind the READ-BEFORE-WRITE guard.
  → `internal/homeassistant`, `internal/controls`
- **REQ-HA-09** Optimal-income switch (`"ON"`/`"OFF"`): **read-modify-write that
  flips ONLY bit 1** of `43110` (RegWorkMode), preserving all other bits (`33`↔`35`
  on this unit). → `internal/controls`, `internal/inverter`
- **REQ-HA-10** **READ-BEFORE-WRITE guard** on every write: read → compare →
  write-if-differs (fc06) → re-read to confirm; **desired == current is skipped
  with no fc06 and logged at `info`**; a mismatched re-read is a logged, non-fatal
  error. → `internal/controls`
- **REQ-HA-11** Command topic `~/<key>/set`; subscribe to `<base>/+/set` and
  **re-subscribe on every reconnect** (`OnConnectionUp`, alongside discovery/
  availability/state republish). → `internal/mqtt`, `internal/cmd`
- **REQ-HA-12** Server-side validation (Ruling R6): amps parsed as float — `NaN`,
  `±Inf`, or unparseable payloads **rejected outright** (logged and dropped, no
  write); any successfully-parsed **finite** float is **clamped to 0–60 A**
  (`ClampHAChargeAmps`) and written under the guard — **no value is rejected for
  being out of range**, only for failing to parse (matches
  `EncodeAmps(ClampHAChargeAmps(parse))`). Switch rejects anything not
  `"ON"`/`"OFF"`; bad commands **logged and dropped** (never crash).
  → `internal/controls`
- **REQ-HA-13** Manual **"Sync RTC now" button** (`rtc_sync`): guarded per-register
  write of the six RTC holding registers `43000–43005` to the current local
  datetime; periodic/threshold-gated auto-sync is **deferred to Phase 6**. Kill-switch
  `CONTROLS_ENABLED` (default true); **Ruling R1** — when false, the four command
  entities are **omitted from discovery** and the command topic is not subscribed
  (state setpoint fields may still publish).
  → `internal/homeassistant`, `internal/controls`

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
