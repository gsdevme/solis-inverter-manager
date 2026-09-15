# 06 — Lifecycle & health

`internal/server` exposes the daemon's HTTP surface; `internal/cmd` is the
composition root and process lifecycle.

## HTTP surface (`internal/server`)

- **`GET /healthz`** — liveness, always `200 ok` while the process runs.
- **`GET /readyz`** — readiness, driven by the scheduler in production
  (`MarkSuccess`/`MarkFailure`; nothing in `cmd` sets it directly):
  - `503` at startup, before the first successful poll.
  - `200 ready` after the first poll whose telemetry read **and** state publish both
    succeed (`MarkSuccess`).
  - Flips back to `503` after `FAILURE_THRESHOLD` **consecutive** poll failures
    (`MarkFailure`); the failure counter resets on the next success, and failures
    below the threshold hold the current state rather than immediately un-readying.
  - Reflects sidecar reachability **transitively**: an unreachable sidecar fails the
    poll's telemetry read, which (after the threshold) fails readiness. There is no
    separate sidecar probe on `/readyz`.
  - `Server.SetReady(bool)` exists alongside those two as a **test/godog seam**
    (`REQ-LC-13`): it sets the flag directly and bypasses the consecutive-failure
    counter (setting ready also resets it), so a scenario can assert a readiness
    transition without driving whole poll cycles. It is never called on the
    production path.
- **`GET /{$}`** — a minimal HTML status page: service name, readiness, uptime, poll
  interval, Go version, and the last inverter values: every key of the state
  document (`03-mqtt-ha-discovery.md`) as read on the most recent poll or command
  refresh, with its age; "no readings yet" before the first successful read. The
  page is handed (`RecordReading`) the *same built document* the publisher sends to
  Home Assistant — `cmd`'s `statePublisher` builds it once and fans it out to both —
  so `/` and HA can never disagree, and the page works without a broker
  (`MODE=mock`, which publishes into `publisher.Discard`). A reading whose setpoints
  were reused from cache because the holding-register read failed is flagged: the
  page dates those setpoints from their last successful read instead of stamping
  them with the telemetry's age. A document that is not a single JSON object is
  refused and the previous reading is kept. The page auto-refreshes at the poll
  interval — `POLL_INTERVAL` validation (`REQ-SC-01`) already enforces the 5 s floor,
  and an interval of zero (only reachable in tests) emits no refresh tag at all.
  Always `200`, never leaks secrets. Unknown paths `404`.

`server.Config` is decoupled from `internal/config`; `serve.go` maps domain values
in (`FailureThreshold`, `PollInterval`). Readiness is an atomic flag the scheduler
drives via `MarkSuccess`/`MarkFailure`; the server owns the consecutive-failure
counter and the `FAILURE_THRESHOLD` comparison (see `REQ-LC-09`, `REQ-SC-04`).

## Process lifecycle (`internal/cmd`)

Startup (`serve.go`):

1. Load + validate config; build the logger; warn if `MODE=mock`.
2. Start the status/health server **immediately** (listens during init so probes
   work at startup).
3. Build the sidecar client + serialising `controls` wrapper.
4. **Wait for the sidecar to serve** — poll its `/health` every `500ms` for up to
   `SIDECAR_STARTUP_TIMEOUT` (default `30s`; `0` disables the wait). The manager
   starts before the sidecar in the two-container pod, so without this the first
   poll fails with `connection refused` and logs a spurious `poll failed` warning
   on every start. Any `200` ends the wait — `inverter_reachable` is deliberately
   ignored here, since inverter reachability is the poll loop's and `/readyz`'s
   concern. Success logs `sidecar serving` with the elapsed time. A sidecar that
   never answers is **not** fatal: startup logs `sidecar not serving after startup
   timeout, continuing` at WARN and carries on, because the scheduler's retries and
   the `/readyz` gate already cover a sidecar that is still down. A SIGTERM during
   the wait falls through silently to the shutdown path. Discovery, `online` and
   the first poll are not published until the sidecar answers or the timeout
   elapses (see `REQ-LC-11`).
5. Connect MQTT and publish discovery + `online` (skipped when no broker URL is
   configured); build and start the scheduler goroutine; block on `ctx`
   (SIGTERM/SIGINT) or a health-server error.

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
