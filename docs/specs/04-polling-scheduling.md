# 04 — Polling & scheduling

`internal/scheduler` owns the poll loop. It is modelled on the sibling services
(`unifi-ha-presence-mqtt`, `hyundai-bluelink-mqtt`): a single goroutine with a
ticker, an immediate first poll, an injectable clock for `testing/synctest`, and a
`HealthReporter` seam into `internal/server`.

## Loop

- Poll at `POLL_INTERVAL` (default `60s`, floor `5s`), with an **immediate first
  poll** before the ticker starts. "Immediate" is relative to the scheduler
  starting: `serve` waits for the sidecar to serve before it starts the scheduler
  at all (`REQ-LC-11`).
- Ticks are **serialised** on a single mutex (`apiMu`) so two polls never overlap —
  the sidecar's single socket can only carry one Modbus frame at a time.
- The loop is a **single goroutine**. RTC auto-sync is folded into the poll (below),
  so there is no second goroutine.
- `Run(ctx)` blocks until `ctx` is cancelled; `PollNow(ctx)` runs exactly one cycle
  and is used by the acceptance suite.

## Per tick

1. Read telemetry + writable-control setpoints via the sidecar (through the
   `Locking` wrapper), decode (`internal/inverter`). Telemetry and setpoints are read
   in **separate holding frames**: a setpoints sub-read failure is non-fatal and
   reuses the **last-known** setpoints rather than blanking the control state; only a
   telemetry read failure fails the poll.
2. On success: cache the last-good `(telemetry, setpoints)`, publish the retained
   state document to MQTT (or, with no broker configured, log decoded telemetry), and
   `MarkSuccess()`.
3. On failure (telemetry read after retries, or a publish error): `MarkFailure()`.
   The cache is **not** cleared, so Home Assistant keeps showing the last-good values.

## Retries & backoff

- A telemetry read is retried up to `POLL_MAX_RETRIES` times (default `3`) with
  **exponential backoff** — `1s, 2s, 4s, …` — before the poll counts as failed.
- Backoff waits honour `ctx` cancellation and use an injectable `After` so
  `testing/synctest` drives them on a fake clock.
- A transient failure that a retry absorbs never reaches `MarkFailure()`, so a single
  flaky read does not flip readiness.

## Readiness (health reporting)

- `MarkSuccess()` flips `/readyz` to **ready** (`200`) and resets the consecutive-
  failure counter.
- `MarkFailure()` increments the counter; readiness flips to **not-ready** (`503`)
  once it reaches `FAILURE_THRESHOLD` (default `3`) **consecutive** failures. Failures
  below the threshold hold the current readiness, so a brief outage does not blank HA.
- The counter lives in `internal/server`; the scheduler only reports outcomes.

## Command serialization (mutex-on-demand)

Inbound MQTT commands are dispatched through `Scheduler.ApplyCommand`, which takes
the **same `apiMu`** the poll uses. A whole guarded write + re-read + refresh is
therefore **atomic against a poll** — never interleaved on the shared sidecar socket.
This is the "one in-flight request" decision: on-demand mutex acquisition rather than
an actor/queue. paho invokes `ApplyCommand` on its publish-routing goroutine; while a
poll holds `apiMu`, the command waits, and vice versa.

## Writes (read-before-write)

Every write the scheduler path issues — setpoints and RTC — goes through the
READ-BEFORE-WRITE guard (`internal/controls`, see `03-mqtt-ha-discovery.md`): read the
current value, issue `fc06` only when it differs, then re-read to confirm. Holding
registers are flash-backed, so a no-op write is skipped.

## RTC auto-sync (opt-in, threshold-gated)

Periodic clock correction is **folded into the poll**, not a separate schedule:

- Off by default. `RTC_SYNC_ENABLED=true` turns it on; `RTC_DRIFT_THRESHOLD`
  (default `60s`, must be `> 0`) is the drift above which it acts.
- Each poll already decodes the inverter RTC (input `33022–33027`). When enabled and
  `|Drift(rtc, now)| > RTC_DRIFT_THRESHOLD`, the scheduler runs the guarded
  `43000–43005` write (still under `apiMu`, so it never overlaps another frame).
- Self-limiting: after a sync the drift is ≈ 0, so it will not fire again until the
  clock drifts anew. The guard skips already-correct registers, honouring the
  flash-wear guardrail.
- Requires the write path: it is disabled (with a startup warning) when
  `CONTROLS_ENABLED=false`.

## Testability

Two clock seams are injected (defaults `time.Now` / `time.After`), each with a
distinct job — and the **poll cadence is neither of them**:

- **`Now func() time.Time`** supplies the current time for **RTC drift**
  (`inverter.Drift(tel.Time, s.now())`, feeding both the `rtc_drift` sensor and the
  auto-sync threshold) and for **boost planning**. The scheduler, the controls handler
  and the publisher share one `Now`, so a single fake clock covers all three and the
  drift a test asserts is the drift the sync decision used.
- **`After func(time.Duration) <-chan time.Time`** supplies the **retry backoff waits
  only** (`1s, 2s, 4s, …`), so a test can collapse the backoff without collapsing
  anything else.
- **The cadence uses `time.NewTicker(PollInterval)`**, deliberately not a seam. A
  ticker fires on a fixed period, so a poll that runs long does **not** push the
  schedule out — the next tick still arrives on the original cadence — whereas a
  sleep-after-work loop would drift by the duration of every slow poll. The immediate
  first poll runs before the ticker is created.

Both seams are deterministic under **`testing/synctest`**, which also drives the
ticker on its fake clock, so the timed loop and the backoff are tested without real
sleeps. The godog acceptance suite (`features/polling.feature`) instead drives
`PollNow` explicitly with an immediate fake backoff clock, asserting retry, the
retained cache and readiness transitions one cycle at a time.

See `REQ-SC-07` (seams), `REQ-SC-09` (`PollNow`) and `REQ-TS-04` (determinism).
