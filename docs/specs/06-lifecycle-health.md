# 06 — Lifecycle & health

`internal/server` exposes the daemon's HTTP surface; `internal/cmd` is the
composition root and process lifecycle.

## HTTP surface (`internal/server`)

- **`GET /healthz`** — liveness, always `200 ok` while the process runs.
- **`GET /readyz`** — readiness, driven by the scheduler (not a manual `SetReady`):
  - `503` at startup, before the first successful poll.
  - `200 ready` after the first poll whose telemetry read **and** state publish both
    succeed (`MarkSuccess`).
  - Flips back to `503` after `FAILURE_THRESHOLD` **consecutive** poll failures
    (`MarkFailure`); the failure counter resets on the next success, and failures
    below the threshold hold the current state rather than immediately un-readying.
  - Reflects sidecar reachability **transitively**: an unreachable sidecar fails the
    poll's telemetry read, which (after the threshold) fails readiness. There is no
    separate sidecar probe on `/readyz`.
- **`GET /{$}`** — a minimal HTML status page: service name, readiness, uptime, poll
  interval, Go version. Always `200`, never leaks secrets. Unknown paths `404`.

`server.Config` is decoupled from `internal/config`; `serve.go` maps domain values
in (`FailureThreshold`, `PollInterval`). Readiness is an atomic flag the scheduler
drives via `MarkSuccess`/`MarkFailure`; the server owns the consecutive-failure
counter and the `FAILURE_THRESHOLD` comparison (see `REQ-LC-09`, `REQ-SC-04`).

## Process lifecycle (`internal/cmd`)

Startup (`serve.go`):

1. Load + validate config; build the logger; warn if `MODE=mock`.
2. Start the status/health server **immediately** (listens during init so probes
   work at startup).
3. Build the sidecar client + serialising `controls` wrapper; connect MQTT and
   publish discovery + `online` (skipped when no broker URL is configured); build and
   start the scheduler goroutine; block on `ctx` (SIGTERM/SIGINT) or a health-server
   error.

Graceful shutdown is a **single authoritative teardown path** (`shutdown()` in
`serve.go`) that both exit branches funnel through, so teardown runs exactly once in
one place:

1. **Drain the scheduler first** — `<-schedDone`. This guarantees no in-flight
   poll/command is still using the sidecar/MQTT client when the broker disconnects.
2. **Publish retained `offline`** on the availability topic.
3. **`mc.Disconnect`** — this now *drains any in-flight reconnect-republish
   goroutine* (cancelling its context and waiting for it, bounded by the shutdown
   context) **before** the clean disconnect, which suppresses the Will. So no
   republish can publish after the transport is torn down.
4. **Stop the health server.**

Both exit branches reach this path: `ctx.Done()` (SIGTERM/SIGINT) calls `shutdown()`
and returns nil; a health-server listen/serve error cancels the run context (so the
scheduler drains), calls the same `shutdown()`, then returns the health error. `ctx`
is a cancellable child of the incoming command context precisely so the health-error
branch can stop the scheduler even though the parent was never cancelled.

The `OnConnectionUp` republish hook runs under a serve-lifetime context owned by the
MQTT client (`mqtt.Client.hookCtx`), cancelled on `Disconnect` — it is no longer a
detached, uncancellable `context.Background()` goroutine.

`cmd/main.go` maps a returned error to stderr + a non-zero exit code.

Logging is structured `log/slog`; `LOG_LEVEL`/`LOG_FORMAT` configurable; the config
`String()`/`LogValue()` redact secrets so credentials never reach the logs.
