# 01 — Sidecar contract

The Go manager (`internal/sidecarclient`, a later phase) talks to the thin Python
sidecar (`sidecar/`) over **localhost HTTP**. The sidecar owns the
Solarman-V5/Modbus transport to the datalogger (TCP `:8899`, `mb_slave_id=1`) and
exposes only **generic, dumb register RPCs** — no decode, no scaling, no sign, no
MQTT, no Home Assistant. Reads return raw `uint16` words; the Go manager does all
interpretation and owns the read-before-write guard.

Implemented by `sidecar/` (Phase 2, issue #19); consumed by `internal/sidecarclient`
(Phase 3+).

## Endpoints

Listens on `:8081` to match the Go default `SIDECAR_URL=http://127.0.0.1:8081`.
All request and response bodies are JSON.

| Method | Path             | Request         | Success response                            |
|--------|------------------|-----------------|---------------------------------------------|
| POST   | `/read_input`    | `{addr, count}` | `{addr, count, regs:[u16,...]}` (fc04)      |
| POST   | `/read_holding`  | `{addr, count}` | `{addr, count, regs:[u16,...]}` (fc03)      |
| POST   | `/write_holding` | `{addr, value}` | `{addr, value, ok:true}` (fc06)             |
| GET    | `/health`        | —               | `{ok:bool, inverter_reachable:bool, mode}`  |

The read shape mirrors the Phase 0 fixture block format
(`docs/phase0/fixtures/*.json`: `{"kind","addr","count","regs":[...]}`), so a
fixture block is directly comparable to a live read of the same range.

### `POST /read_input` (fc04) and `POST /read_holding` (fc03)

Block read of `count` registers starting at `addr`.

```jsonc
// request
{ "addr": 33022, "count": 6 }
// response
{ "addr": 33022, "count": 6, "regs": [26, 9, 8, 15, 38, 59] }
```

`regs` is `count` raw `uint16` words (0..65535), in register order, undecoded.

### `POST /write_holding` (fc06)

Single-register write.

```jsonc
// request
{ "addr": 43141, "value": 340 }
// response
{ "addr": 43141, "value": 340, "ok": true }
```

The write is **unconditional** — the sidecar always issues the fc06. The
read-before-write guard (only write when a read shows the value differs, to avoid
flash wear) is the **Go manager's** responsibility, not the sidecar's.

### `GET /health`

```jsonc
{ "ok": true, "inverter_reachable": true, "mode": "mock" }
```

- `ok` — sidecar process liveness (always `true` while serving).
- `inverter_reachable` — a cheap single-register probe of the datalogger
  (`MODE=mock` always reports `true`; the probe never raises). Consumed by the Go
  `/readyz`.
- `mode` — `"mock"` or `"live"`.

## Validation

- `count` in `1..125` (the Modbus per-request register cap).
- `addr` in `0..65535`.
- `value` in `0..65535`.
- Body must be a JSON object; booleans are not accepted where an integer is
  required.

Any violation → `400` with the error envelope below.

## Error model

Every non-2xx response carries:

```jsonc
{ "error": { "code": "…", "message": "…" } }
```

| Code               | HTTP | Meaning                                                        |
|--------------------|------|---------------------------------------------------------------|
| `bad_request`      | 400  | Malformed body or out-of-range argument.                      |
| `timeout`          | 504  | Socket timeout (`INVERTER_SOCKET_TIMEOUT`).                   |
| `illegal_address`  | 502  | Modbus exception from the inverter (e.g. illegal data addr).  |
| `frame_error`      | 502  | Solarman V5 frame / CRC / decode error.                      |
| `connection_error` | 503  | Socket dead or a fresh connection failed.                    |
| `not_found`        | 404  | No route matched.                                            |
| `internal_error`   | 500  | Unexpected server-side failure (catch-all). Also the client fallback for any unrecognised code. |

The Go manager maps `timeout`/`connection_error`/`frame_error` to retryable poll
failures; `bad_request`/`illegal_address` indicate a programming/addressing bug
and should not be retried blindly.

## Behaviour guarantees

- **Single persistent socket.** One `pysolarmanv5` connection is reused across
  requests, opened lazily on first use.
- **Serialized.** One global lock ensures **exactly one Modbus frame in flight**;
  concurrent HTTP requests queue on the lock.
- **Reconnect on error.** On any transport exception the socket is marked dead
  (closed) and reconnected lazily on the next call.
  This includes a request that gets **no reply** within the socket timeout
  (`timeout`, 504): a silent peer — e.g. a firewall that dropped the idle TCP
  state — must never leave a half-open session in place, or every later call
  burns the full timeout while holding the lock and the sidecar stalls.
- **Per-call timeout.** Each Solarman call uses the socket timeout from
  `INVERTER_SOCKET_TIMEOUT`.
- **Mock mode.** `MODE=mock` serves an in-memory register map seeded from a Phase
  0 fixture — no `pysolarmanv5`, no socket — so the whole dev/CI loop runs with no
  hardware. Reads return stored (or zero-filled) words; writes update the map.

## Config (sidecar-owned env)

Env names mirror the Go `internal/config` so both processes read the same `.env`.

| Var                       | Default              | Notes                                                        |
|---------------------------|----------------------|--------------------------------------------------------------|
| `MODE`                    | — (required)         | `mock` \| `live`.                                            |
| `INVERTER_IP`             | —                    | Required when `MODE=live`.                                    |
| `INVERTER_SERIAL`         | —                    | Datalogger serial, numeric; **secret** (redacted in logs). Required when `MODE=live`. |
| `INVERTER_PORT`           | `8899`               | Solarman V5 TCP port.                                        |
| `INVERTER_SOCKET_TIMEOUT` | `10s`                | Bare seconds or a Go-style duration (`10`, `10s`, `1m30s`).  |
| `SIDECAR_LISTEN_ADDR`     | `:8081`              | `host:port`; empty host binds all interfaces.               |
| `MOCK_FIXTURE`            | full sweep + comprehensive | Seed fixture(s) for `MODE=mock`, comma-separated and applied in order; default `docs/phase0/fixtures/live-snapshot-full-sweep.json,docs/phase0/fixtures/live-snapshot-comprehensive.json` (the sweep alone leaves the battery block zero). |

Config is fail-fast: every problem is aggregated and reported at once (mirrors the
Go `errors.Join` style). `MODE=live` requires `INVERTER_IP` + `INVERTER_SERIAL`;
`MODE=mock` needs neither.

## Non-goals

The sidecar never decodes, scales, applies sign conventions, talks MQTT/HA, or
guards writes. See `02-register-map.md` (decode) and `03-mqtt-ha-discovery.md`
(write guard) for where that lives.
