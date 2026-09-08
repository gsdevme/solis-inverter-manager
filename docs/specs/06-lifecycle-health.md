# 06 — Lifecycle & health

> **Status: skeleton.** Full content authored in a later phase. The HTTP surface
> below is implemented in the scaffold.

`internal/server` exposes the daemon's HTTP surface; `internal/cmd` is the
composition root and process lifecycle.

## HTTP surface (`internal/server`)

- **`GET /healthz`** — liveness, always `200 ok` while the process runs.
- **`GET /readyz`** — readiness:
  - `503` until the readiness flag is set.
  - `200 ready` once `SetReady(true)` is called (the scheduler does this after the
    first successful poll — TODO, later phase).
  - TODO: flip back to `503` after `FAILURE_THRESHOLD` consecutive poll failures;
    recover on the next success.
  - TODO: also probe the sidecar so `/readyz` reflects sidecar reachability.
- **`GET /{$}`** — a minimal HTML status page: service name, readiness, uptime, poll
  interval, Go version. Always `200`, never leaks secrets. Unknown paths `404`.
  TODO: surface the latest inverter reading and sidecar/MQTT connection state.

`server.Config` is decoupled from `internal/config`; `serve.go` maps domain values
in. Readiness is a single atomic flag flipped via `SetReady`.

## Process lifecycle (`internal/cmd`)

Startup (`serve.go`):

1. Load + validate config; build the logger; warn if `MODE=mock`.
2. Start the status/health server **immediately** (listens during init so probes
   work at startup).
3. TODO: build the sidecar client, inverter adapter, MQTT client + publisher; publish
   discovery + `online`; start the scheduler; block on `ctx` (SIGTERM/SIGINT).

Graceful shutdown: TODO — publish retained `offline`, `Disconnect()` the broker
cleanly, stop the scheduler, then shut the health server down. The scaffold already
shuts the health server down cleanly on signal.

`cmd/main.go` maps a returned error to stderr + a non-zero exit code.

Logging is structured `log/slog`; `LOG_LEVEL`/`LOG_FORMAT` configurable; the config
`String()`/`LogValue()` redact secrets so credentials never reach the logs.
