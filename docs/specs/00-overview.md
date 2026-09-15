# 00 — Overview

## Purpose

A small, stdlib-first Go **manager** that polls a Solis hybrid inverter and
republishes its state to **MQTT** with **Home Assistant (HA) MQTT autodiscovery**,
and exposes writable setpoint controls back to HA.

- **Hardware:** Solis **RHI-3.6K-48ES-5G** hybrid inverter, reached via its
  **Solarman V5 datalogger** (gen2 WiFi stick) over TCP **port 8899**.
- **Architecture:** a **two-container pod** — a Go **manager** (this repo:
  orchestration, decode, MQTT, HA discovery, scheduling, health) plus a **thin
  Python sidecar** that owns only the Modbus/Solarman-V5 transport
  (`pysolarmanv5`). The manager talks to the sidecar over **localhost HTTP**; the
  sidecar never touches MQTT or HA.

## Principles

- **Spec-driven.** `docs/specs/` is the source of truth for behaviour and
  `REQUIREMENTS.md` holds stable `REQ-*` ids. A behaviour change updates the spec and
  its requirement in the **same commit** as the code; ids are never renumbered,
  because they are cited from code comments.
- **The manager owns all register semantics.** Decode, scale, sign conventions,
  bitfields and the write guard live in Go (`internal/inverter`). The Python sidecar
  is a dumb Solarman-V5 transport that moves raw `uint16` words and nothing else.
- **Trust no unconfirmed register.** Every address, width, scale and sign traces to a
  probe-confirmed row in `docs/phase0/findings.md`, with the raw captures in
  `docs/phase0/fixtures/` driving the decode tests.
- **stdlib-first, on go 1.27.** A deliberately small dependency set — cobra, autopaho,
  godotenv, and godog for tests — mirroring the sibling services
  `gsdevme/hyundai-bluelink-mqtt` and `gsdevme/unifi-ha-presence-mqtt`.
- **Read-only by default**, with a small, explicitly guarded set of writable
  setpoints. `CONTROLS_ENABLED=false` (`REQ-CF-10`) makes the service fully read-only:
  no command entities, no subscription, no reconcile, no RTC sync.
- **READ-BEFORE-WRITE on every holding register.** Read, compare, and issue fc06
  **only** when the value differs — holding registers are flash-backed and needless
  writes wear flash. See the write-guard in `03-mqtt-ha-discovery.md` and
  `05-config.md`.
- **Conservative on the wire.** The Modbus link is genuinely laggy: one persistent
  socket, exactly one frame in flight, a serialised ~60 s poll with backoff, and a
  retained last-good cache so Home Assistant never blanks during an outage.
- **Kube-friendly.** Env-var config with fail-fast validation, `log/slog` with secret
  redaction, `/healthz` + `/readyz`, and a single authoritative graceful-shutdown path.

## High-level flow

**Startup** — load and validate config (`errors.Join` fail-fast, `REQ-CF-02`) → start
the health server → build the sidecar client → **wait for the sidecar to serve**
(`/health` polled up to `SIDECAR_STARTUP_TIMEOUT`, `REQ-LC-11`) → connect MQTT with a
retained `offline` Will → publish retained discovery + `online` → subscribe to the
command topic → start the scheduler.

**Per poll** (`POLL_INTERVAL`, default 60 s, immediate first poll, single goroutine,
serialised on one mutex):

1. Read telemetry (two input blocks of ≤ 100 registers, `REQ-RM-12`) and the setpoint
   holding block, with retry + exponential backoff.
2. Decode (`internal/inverter`), cache the last-good `(telemetry, setpoints)`.
3. Build **one** state document and fan it out — retained JSON to MQTT, and in-process
   to the status page.
4. Mark readiness success/failure.
5. Opt-in, threshold-gated RTC auto-sync, then the declarative schedule reconcile —
   both under the same mutex, both through the write guard, and both skipped when the
   cycle reused cached setpoints (`REQ-SC-08`).

**On a command** — Home Assistant publishes to `<base>/<key>/set` → parse and validate
server-side → take the same mutex the poll uses → read-compare-write-verify → refresh
state.

**On reconnect** — republish discovery, `online`, the cached state, and re-subscribe.

**On shutdown** — one authoritative teardown path: drain the scheduler → publish
retained `offline` → drain the reconnect hook and disconnect cleanly (suppressing the
Will) → stop the health server.

## Non-goals

- **No horizontal scaling.** One inverter, one datalogger, one persistent socket — a
  single replica is correct and more would contend for the same socket.
- **No Kubernetes manifests in this repo.** Cluster deployment is GitOps (Helm/Flux)
  in a separate infrastructure repo; `08-deployment.md` documents the intended shape
  as reference examples (`REQ-DP-07`).
- **No decode, scaling, sign handling, MQTT/HA knowledge or write guarding in the
  sidecar.** It never interprets a register.
- **No complete Modbus coverage yet.** The full sweep reads cleanly, but only
  probe-confirmed registers are modelled; the rest is an explicit deferred scope
  (`REQ-RM-14`) and each addition lands with its own requirement.
- **No writable storage-mode dropdown.** `select.optimal_income` flips bit 1 of the
  43110 bitfield only; the other work modes are unprobed on this unit, and the
  guardrail is to trust nothing unconfirmed.
- **No production MQTT broker.** The `docker-compose` mosquitto is anonymous and
  non-persistent, for local development and the live smoke only (`REQ-DP-09`).
- **Not a fast poller.** The link is laggy; the design optimises for resilience and
  flash longevity, not latency.

## Reference

See the sibling specs:
- `01-sidecar-contract.md` — the localhost HTTP contract between manager and sidecar.
- `02-register-map.md` — the confirmed Solis register map (source: `docs/phase0/findings.md`).
- `03-mqtt-ha-discovery.md` — MQTT topics, HA discovery, writable controls, write-guard.
- `04-polling-scheduling.md` — the poll loop.
- `05-config.md` — environment variables.
- `06-lifecycle-health.md` — probes, readiness, graceful shutdown, status page.
- `07-testing.md` — unit tests, live-capture fixtures, the godog suite, the sidecar
  pytest/ruff suite, lint and CI.
- `08-deployment.md` — the two-container pod, images, the local compose stack, CI/CD.
- `09-schedule-controls.md` — Time-of-Use windows, the boost slot, and the
  manager-owned schedule reconcile.
- `REQUIREMENTS.md` — traceable requirement IDs (`REQ-*`).

Background, not a spec: `docs/plans/rebuild.md` (roadmap and locked decisions) and
`docs/phase0/findings.md` (the probe-confirmed register map and its raw fixtures).
