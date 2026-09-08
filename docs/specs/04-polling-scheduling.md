# 04 — Polling & scheduling

> **Status: skeleton.** Full content authored in a later phase.

`internal/scheduler` owns the poll loop.

## Loop (TODO)

TODO: poll at `POLL_INTERVAL` (default 60s, floor 5s), immediate first run,
serialised ticks (never overlap).

## Per tick (TODO)

TODO: read register blocks via the sidecar -> decode (`internal/inverter`) ->
publish state to MQTT -> report success/failure to the health server
(`server.SetReady` after the first success; not-ready after `FAILURE_THRESHOLD`
consecutive failures).

## Writes (TODO)

TODO: apply any pending setpoint writes under the READ-BEFORE-WRITE write-guard (see
`03-mqtt-ha-discovery.md`) — read current value, skip if unchanged.

## Retries & testability (TODO)

TODO: retry transient errors up to `POLL_MAX_RETRIES` with backoff; injectable
`Now`/`After` for deterministic `testing/synctest` tests.
