# 05 — Configuration

> **Status: skeleton.** Full content authored in a later phase. The variable
> catalogue and validation rules below are authoritative for the scaffold.

All configuration is via **environment variables**, bound in `internal/config` with
stdlib `os.Getenv` + validation. For local dev, `godotenv` loads a `.env` file (a
no-op in prod). `.env.dist` is the committed template; `.env` is git-ignored and
holds real secrets.

## Variables

| Env var | Default | Required | Description |
| --- | --- | --- | --- |
| `MODE` | `live` | no | `live` talks to the real inverter via the sidecar and MQTT; `mock` runs the pipeline against canned data (no inverter/MQTT values required). |
| `INVERTER_IP` | — | if `live` | Datalogger IP address. |
| `INVERTER_SERIAL` | — | if `live` | Datalogger serial. **Secret — never logged.** |
| `INVERTER_PORT` | `8899` | no | Solarman V5 datalogger TCP port. |
| `INVERTER_SOCKET_TIMEOUT` | `10s` | no | Per-call socket timeout (Go duration). |
| `SIDECAR_URL` | `http://127.0.0.1:8081` | no | Localhost HTTP endpoint of the Python sidecar. |
| `POLL_INTERVAL` | `60s` | no | Poll cadence (`time.ParseDuration`). Floor `5s`. |
| `POLL_MAX_RETRIES` | `3` | no | Transient-error retries per poll before it counts as failed. Must be `>= 0`. |
| `FAILURE_THRESHOLD` | `3` | no | Consecutive poll failures before `/readyz` flips not-ready. Must be `>= 1`. |
| `MQTT_BROKER_URL` | — | if `live` | e.g. `mqtt://mosquitto:1883` or `tls://host:8883`. |
| `MQTT_USERNAME` | — | no | MQTT auth username. |
| `MQTT_PASSWORD` | — | no | MQTT auth password. **Secret — never logged.** |
| `MQTT_CLIENT_ID` | `solis-inverter-manager` | no | MQTT client id. |
| `MQTT_TOPIC_PREFIX` | `solis` | no | Base topic prefix. |
| `HA_DISCOVERY_PREFIX` | `homeassistant` | no | HA discovery prefix. |
| `HEALTH_ADDR` | `:8080` | no | Listen address for `/`, `/healthz` and `/readyz`. |
| `LOG_LEVEL` | `info` | no | `debug`/`info`/`warn`/`error` (`log/slog`). |
| `LOG_FORMAT` | `json` | no | `json` or `text`. |

## Validation rules

- Fail fast via `errors.Join` (exit non-zero with a clear, aggregated message) when a
  required var is missing or a duration/URL fails to parse.
- `MODE` must be `live` or `mock`. In `mock`, no live/MQTT values are required. In
  `live`, `INVERTER_IP`, `INVERTER_SERIAL` and `MQTT_BROKER_URL` are required.
- `POLL_INTERVAL` below the `5s` floor is rejected; `INVERTER_PORT` in `1–65535`;
  `INVERTER_SOCKET_TIMEOUT > 0`; `FAILURE_THRESHOLD >= 1`; `POLL_MAX_RETRIES >= 0`.
- **Secrets** (`INVERTER_SERIAL`, `MQTT_PASSWORD`) are never logged: the config's
  `String()` and `LogValue()` replace them with a redaction placeholder.

## CRITICAL design constraint — READ-BEFORE-WRITE write-guard

> Recorded here as a first-class configuration/behaviour note; fully specified in a
> later phase (see `03-mqtt-ha-discovery.md`).

The manager MUST **always read a register's current value first and only issue a
Modbus write (fc06) when the desired value differs**. Holding registers are
**flash-backed**, so needless writes cause **flash wear**. This governs every
setpoint — timed charge current `43141`, timed discharge current `43142`, work-mode
`43110` — and any RTC auto-sync, which may only rewrite `43000–43005` when clock
**drift exceeds a threshold**. TODO: a future `RTC_SYNC_*` / write-tolerance config
knob may be added when the writable-controls phase lands.
