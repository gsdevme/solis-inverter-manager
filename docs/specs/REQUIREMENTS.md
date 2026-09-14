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
payload shapes and the 38-entity table.

- **REQ-HA-01** Discovery: one **retained**, QoS-1 config per entity at
  `<HA_DISCOVERY_PREFIX>/<component>/<serial>_<key>/config` (object_id form);
  identical shared device block (`identifiers:[<serial>]`, `manufacturer:"Solis"`,
  `model:"RHI-3.6K-48ES-5G"`, `name:"Solis Inverter"`); `~` = base topic and the
  only abbreviated key; `unique_id` = `<serial>_<key>`; entity ids come from
  `HA_OBJECT_ID_PREFIX` (REQ-HA-18).
  → `homeassistant/discovery.go`, `homeassistant/entities.go`, `publisher/publisher.go`
- **REQ-HA-02** State: a single **retained**, QoS-1 JSON document at `<base>/state`;
  every entity reads it via `value_template {{ value_json.<key> }}` (binary_sensor
  via `{{ 'ON' if value_json.<key> else 'OFF' }}`); the state DTO's json tags are
  the 38 read-only entity keys plus the four control-readback fields
  (`set_charge_current`, `set_discharge_current`, `optimal_income`,
  `boost_select`) — 42 tags.
  `rtc` is RFC3339; `rtc_drift` is seconds.
  → `homeassistant/state.go`, `homeassistant/entities.go`
- **REQ-HA-03** Entity classes per the `03` table; **daily** energy counters use
  `state_class: total`, **lifetime** counters `total_increasing`; signed
  battery/grid power is one signed entity (not split), with a derived
  `battery_charging` binary_sensor. `battery_charge_power` and
  `battery_discharge_power` are **derived convenience sensors** for Home Assistant
  (`max(battery_power, 0)` / `max(-battery_power, 0)`, computed in `BuildState`
  from the one correctly-decoded signed S32 — not an independent decode of the
  register halves). → `homeassistant/entities.go`, `homeassistant/state.go`
- **REQ-HA-04** Availability + LWT: retained `offline` LWT on `<base>/availability`
  at QoS 1; `online` retained on connect; explicit `offline` retained + clean
  disconnect on graceful shutdown (clean disconnect suppresses the Will).
  → `mqtt/client.go`, `publisher/publisher.go`, `cmd/serve.go`
- **REQ-HA-05** Reconnect republish: on every (re)connection (`OnConnectionUp`),
  republish discovery + availability(`online`) + last cached state.
  → `cmd/serve.go`, `mqtt/client.go`
- **REQ-HA-06** MODE gating: publish only when an MQTT broker URL is configured;
  `live` requires it; `mock` without a broker runs the same `Collect` unit and logs
  decoded telemetry. Poll cadence = `POLL_INTERVAL` (default 60s), driven by the
  Phase-6 scheduler (`REQ-SC-01`). → `cmd/serve.go`, `config.go`
- **REQ-HA-07** Read-only in Phase 4; writable controls (`number`/`select`/`button`)
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
- **REQ-HA-09** Optimal-income control (`select.optimal_income`, `Run`/`Stop` —
  see REQ-HA-15): **read-modify-write that flips ONLY bit 1** of `43110`
  (RegWorkMode), preserving all other bits (`33`↔`35` on this unit).
  → `internal/controls`, `internal/inverter`
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
  `EncodeAmps(ClampHAChargeAmps(parse))`). A select accepts only its own options
  (space trimmed): `optimal_income` `Run`/`Stop` (case-insensitive),
  `boost_select` one of its nine option strings exactly; bad commands **logged
  and dropped** (never crash).
  → `internal/controls`
- **REQ-HA-13** Manual **"Sync RTC now" button** (`rtc_sync`): guarded per-register
  write of the six RTC holding registers `43000–43005` to the current local
  datetime, via the shared guarded loop (`writeRegisters`) that retries a
  transport-failed register once, in place, and aborts the sequence if it still
  fails — see `09-schedule-controls.md` **Partial writes**. Periodic/threshold-
  gated **auto-sync** is now available (Phase 6), **opt-in** via
  `RTC_SYNC_ENABLED` and folded into the poll — see `REQ-SC-06`.
  Kill-switch `CONTROLS_ENABLED` (default true); **Ruling R1** — when false, the five
  command entities are **omitted from discovery**, the command topic is not
  subscribed, and both the schedule reconcile (REQ-HA-17) and RTC auto-sync are
  disabled (they need the write path).
  → `internal/homeassistant`, `internal/controls`, `internal/scheduler`
- **REQ-HA-14** Derived schedule sensors `tou_window`, `boost`, `boost_ends_at`:
  read-only in B1 (no writes). The setpoint holding read grows to 61 registers
  (`43110`–`43170`) to also cover timed slots 1–3; slot 3 is reserved as the
  boost slot. `tou_window` is slots 1–2 joined at midnight when slot 2 has a
  charge window meeting slot 1's end there; slot 1 alone when slot 2's charge
  window is unset; and `null` when slot 1 is unset, or both are set but do not
  meet at midnight. `tou_window`/`boost_ends_at` are JSON `null` when unset;
  `boost` is `"Off"`/`"Charge until HH:MM"`/`"Discharge until HH:MM"`. `boost`
  reflects the slot-3 *configuration*, not whether the window is currently
  running; a `boost_ends_at` in the past means the configured window has
  elapsed and slot 3 has not been cleared (B2 adds the write path that clears
  it). An end time of `00:00` means end-of-day, so `boost_ends_at` resolves to
  the next midnight. →
  `internal/schedule`, `internal/homeassistant/state.go`,
  `internal/controls/setpoints.go`
- **REQ-HA-15** Work-mode controls use the Solis app's vocabulary (`09-schedule-controls.md`):
  `select.optimal_income` with options `Run`/`Stop` **replaces** the on/off switch —
  same key, same guarded read-modify-write of bit 1 only (35 ↔ 33), state derived
  from `43110`; `Config.BuildDiscoveryRemovals` names the old
  `switch.optimal_income` discovery topic and `publisher.PublishDiscoveryRemovals`
  clears it with one empty retained payload after every discovery publish
  (startup and each reconnect, ungated by `CONTROLS_ENABLED`) so HA drops the
  stale entity. `sensor.work_mode` is renamed "Energy storage mode" and shows
  `Self Use` (bit 0), falling back to the flag list for unobserved values. A
  writable storage-mode dropdown is deferred until the other modes are probed.
  → `internal/homeassistant/entities.go`, `state.go`, `discovery.go`,
  `internal/publisher`, `internal/controls/handler.go`
- **REQ-HA-16** `select.boost_select` (options `Off`, `Charge|Discharge 15|30|45|60 min`)
  programs slot 3: start = now truncated to the minute, end = the N-th quarter-hour
  boundary strictly after now (N = minutes/15). Rejected with a warning and no write
  when Optimal Income is `Stop`, the window would reach midnight, or it overlaps the
  ToU window. All eight slot registers go through the guard one at a time; the
  unused direction is asserted empty and is always cleared first, the charge block
  leading only when both directions are unset or both are set; each block is
  written in the order start hour, start minute, end hour, end minute; `Off` clears
  slot 3 at once. A command a failed register interrupts stops there and is
  reported; it is never completed around the failure (REQ-HA-13). Only offsets
  `+2..+9` of a slot are ever written (never `43151/43152`, `43161/43162`). State
  `boost_select` is derived from slot 3 (`Off`, the matching option via
  `15·⌈minutes/15⌉`, or `null` when unmappable).
  → `schedule.BoostOptions`/`ParseBoostOption`/`PlanBoost`/`BoostSelectState`,
  `inverter.TimedSlotWriteRegisters`, `controls.Handler.setBoost`
- **REQ-HA-17** Manager-owned schedule reconcile after every poll when controls are
  enabled: slots 1–2 are asserted to `TOU_WINDOW` split at midnight (discharge windows
  empty; skipped entirely when `TOU_WINDOW` is empty) and a slot-3 window that is not
  a **live boost** is cleared. Live means set, `start ≤ now < end` in minute-of-day
  terms, and `end ≠ 00:00`; it is judged **per direction**, so a slot left holding the
  remnant of a partly written command is healed rather than wiped along with the
  window still running. The adoption contract is exactly that narrow: a window that
  has not started, that ends at `00:00` (a shape `PlanBoost` never writes, so it can
  only be a half-programmed boost) or that belongs to a previous day is a remnant and
  is cleared rather than re-fired. `end = 00:00` still resolves to the next midnight
  for `boost_ends_at` (REQ-HA-14); the liveness test does not resolve it at all.
  Only registers that differ are written (guarded), so the steady state issues no
  fc06; one log line per reconcile that wrote; errors are
  non-fatal and retried next poll. Healing is in slot order — the reconcile writes
  slots 1–3 as one sequence, so a tariff-slot register that fails on every poll
  blocks the slot-3 clear behind it until it recovers. App-side slot edits are reverted within one poll
  (only a live slot-3 window is adopted). The reconcile never touches `43110`,
  `43141`, `43142` or the slot leading pairs. It runs from the poll, after the
  state publish, so a poll that fails to publish (broker down) reconciles
  nothing that cycle; a poll whose setpoints read failed — and therefore reused
  the cached slots — skips the reconcile the same way, so the manager never acts
  on a slot the cycle did not read. → `controls.Handler.Reconcile`,
  `scheduler.Reconciler` (the consumer-defined interface `Scheduler.maybeReconcile`
  calls), gated by `cmd.setpointFreshness`
- **REQ-HA-18** Entity-id derivation: `object_id` = `<HA_OBJECT_ID_PREFIX>_<key>`
  and `default_entity_id` = `<component>.<HA_OBJECT_ID_PREFIX>_<key>`, so Home
  Assistant assigns e.g. `sensor.solis_inverter_battery_soc`. `unique_id` stays
  `<serial>_<key>` and the discovery topic keeps its `<serial>_<key>` node segment,
  so changing the prefix renames entity ids without orphaning registry entries.
  **Both** id keys are published: current cores read `default_entity_id` and ignore
  a payload `object_id`, older cores read only `object_id`, and the MQTT platform
  discovery schemas drop unknown keys (`extra=vol.REMOVE_EXTRA`) rather than
  rejecting the config. → `homeassistant/discovery.go`, `homeassistant/entities.go`,
  `config.go`, `cmd/serve.go`

## Scheduling (`internal/scheduler`, `04-polling-scheduling.md`)

Phase 6 (#23) replaces the interim ticker with the resilient scheduler: a single
goroutine with an injectable clock, backoff, a retained last-good cache, and
health-driven readiness.

- **REQ-SC-01** Serialised poll loop at `POLL_INTERVAL` (default `60s`, floor `5s`)
  with an **immediate first poll**; ticks never overlap (`apiMu`). Single goroutine.
  → `scheduler/scheduler.go`, `cmd/serve.go`
- **REQ-SC-02** Transient telemetry-read failures retried up to `POLL_MAX_RETRIES`
  (default `3`) with **exponential backoff** (`1s, 2s, 4s, …`), honouring `ctx` and an
  injectable `After`. A retry-absorbed failure never flips readiness. → `scheduler/*`
- **REQ-SC-03** Retained last-good cache: the most recent successful
  `(telemetry, setpoints)` is cached and never cleared on failure, so HA holds
  last-good values; the reconnect republish hook reads it (`LastState`). A setpoints
  sub-read failure reuses last-known setpoints rather than blanking. → `scheduler/*`,
  `cmd/serve.go`
- **REQ-SC-04** Readiness reporting: `MarkSuccess` on a successful poll, `MarkFailure`
  on a failed one; readiness flips per `REQ-LC-09`. → `scheduler/*`, `internal/server`
- **REQ-SC-05** Command↔poll serialisation (**mutex-on-demand**): `ApplyCommand`
  takes the same `apiMu` as the poll, so a guarded write + re-read + refresh is atomic
  against a poll. → `scheduler/*`, `cmd/serve.go`, `internal/mqtt`
- **REQ-SC-06** Opt-in, threshold-gated **RTC auto-sync** folded into the poll: when
  `RTC_SYNC_ENABLED` and `|Drift| > RTC_DRIFT_THRESHOLD`, run the guarded
  `43000–43005` write under `apiMu` (no second goroutine); self-limiting, disabled
  when `CONTROLS_ENABLED=false`. → `scheduler/*`, `internal/controls`
- **REQ-SC-07** Injectable `Now`/`After` for deterministic `testing/synctest` tests;
  the scheduler and controls handler share one clock. → `scheduler/*`

## Config (`internal/config`, `05-config.md`)

- **REQ-CF-01** All env vars in `05-config.md` bound with defaults. → `config.go`
- **REQ-CF-02** Fail-fast validation via `errors.Join`. → `config.go`
- **REQ-CF-03** Secrets (`INVERTER_SERIAL`, `MQTT_PASSWORD`) redacted in
  `String()`/`LogValue()`. → `config.go`
- **REQ-CF-04** `MODE` (`live`|`mock`): `live` requires inverter identity + MQTT
  broker; `mock` drops them. → `config.go`, `.env.dist`
- **REQ-CF-05** `POLL_MAX_RETRIES` (default `3`, `>= 0`) and `FAILURE_THRESHOLD`
  (default `3`, `>= 1`) are consumed by the scheduler (backoff attempts) and the
  server (readiness threshold) respectively. → `config.go`, `scheduler/*`, `server`
- **REQ-CF-06** RTC auto-sync knobs: `RTC_SYNC_ENABLED` (bool, default `false`) and
  `RTC_DRIFT_THRESHOLD` (Go duration, default `60s`, must be `> 0`). Redacted-safe and
  logged like the rest of the config. → `config.go`, `.env.dist`, `scheduler/*`
- **REQ-CF-07** `TOU_WINDOW` (`HH:MM-HH:MM`, local time, default `23:30-05:30`; may cross
  midnight; anything else fails validation). Read with `os.LookupEnv`, so an
  explicitly empty value disables ToU assertion while an unset variable takes the
  default. Gated by `CONTROLS_ENABLED` like every write; set with controls off,
  it logs a startup warning. → `config.go`, `schedule.ParseToUWindow`, `.env.dist`,
  `09-schedule-controls.md`
- **REQ-CF-08** `HA_OBJECT_ID_PREFIX` (default `solis_inverter`) must be a non-empty
  slug matching `^[a-z0-9_]+$`; it is the entity-id prefix of REQ-HA-18 and is plumbed
  into `homeassistant.Config.ObjectIDPrefix`. → `config.go`, `.env.dist`, `cmd/serve.go`

## Lifecycle & health (`internal/server`, `cmd`, `main.go`, `06-lifecycle-health.md`)

- **REQ-LC-01** `/healthz` liveness always-ok while running. → `internal/server`
- **REQ-LC-02** `/readyz` `503` until ready, `200` once a poll succeeds. →
  `internal/server`
- **REQ-LC-09** Readiness is scheduler-driven via `MarkSuccess`/`MarkFailure`: ready
  after the first successful poll, not-ready after `FAILURE_THRESHOLD` **consecutive**
  failures (counter resets on success; failures below the threshold hold the current
  state). `/readyz` reflects sidecar reachability transitively (an unreachable sidecar
  fails the poll). → `internal/server`, `internal/scheduler`, `cmd/serve.go`
- **REQ-LC-10** Graceful shutdown is a **single authoritative teardown path** that
  **both** exit branches (SIGTERM/SIGINT via `ctx.Done()` **and** a health-server
  listen/serve error) funnel through, so teardown runs exactly once: drain the
  scheduler first → publish retained `offline` → `Disconnect` (which **drains the
  in-flight `OnConnectionUp` republish goroutine** — cancelling its serve-lifetime
  context and waiting, bounded by the shutdown context — before the clean disconnect
  that suppresses the Will) → stop the health server. The health-error branch cancels
  the run context so the scheduler drains, then returns the health error; the signal
  branch returns nil. → `cmd/serve.go`, `internal/mqtt/client.go`
- **REQ-LC-08** `cmd/main.go` reports errors to stderr and exits non-zero. →
  `cmd/main.go`

## Testing (`internal/mock`, `features`, `07-testing.md`)

- TODO **REQ-TS-\***: mock sidecar; unit tests; godog scenarios; `golangci-lint` +
  `go vet`. Scaffold ships `features/health.feature`. → `*_test.go`, `features/*`

## Deployment (`08-deployment.md`)

Phase 7 (#24) packages the two-container service. See
[`08-deployment.md`](08-deployment.md) for the full topology, probe wiring, and the
reference manifests.

- **REQ-DP-01** Two-container pod (manager + sidecar), **single replica**; the
  manager reaches the sidecar over loopback (`SIDECAR_URL=http://127.0.0.1:8081`),
  and only the sidecar opens the `:8899` datalogger socket. → `08-deployment.md`
- **REQ-DP-02** Manager image: two-stage `golang:1.27` → `distroless/static:nonroot`,
  static `CGO_ENABLED=0` binary, `EXPOSE 8080`, non-root, default `MODE=live`,
  `CMD serve`. → `Dockerfile`
- **REQ-DP-03** Sidecar image: `python:3.12-slim` + `pysolarmanv5`, localhost HTTP on
  `:8081`, no MQTT, built from the repo root (fixtures for `MODE=mock`).
  → `sidecar/Dockerfile`
- **REQ-DP-04** Probes: manager liveness `GET /healthz:8080` + readiness
  `GET /readyz:8080` (scheduler-driven, `REQ-LC-09`); sidecar liveness
  `GET /health:8081` (`REQ-SD-04`). `/readyz` is the authoritative readiness signal,
  not the ~40s-delayed MQTT Will. → `08-deployment.md`
- **REQ-DP-05** Config split: non-secret env in a ConfigMap, `INVERTER_SERIAL` +
  `MQTT_PASSWORD` in a Secret; both via `envFrom`. Hardened securityContext
  (`runAsNonRoot`, `allowPrivilegeEscalation:false`, drop `ALL` caps, seccomp
  `RuntimeDefault`; manager `readOnlyRootFilesystem:true`);
  `terminationGracePeriodSeconds: 30` covers the graceful-shutdown drain
  (`REQ-LC-10`). → `08-deployment.md`
- **REQ-DP-06** CI: PR gate builds **both** images build-only (`push: false`, amd64);
  release builds them multi-arch (amd64+arm64) via release-please, **also build-only
  pending owner approval** — the **no-image-publish guardrail** holds until the owner
  approves. → `.github/workflows/{ci,release}.yml`, `release-please-config.json`,
  `.release-please-manifest.json`
- **REQ-DP-07** Kubernetes manifests are **not** carried in this repo; cluster
  deployment is managed via GitOps (Helm/Flux) in a separate infrastructure repo.
  `08-deployment.md` documents the intended manifest shape as reference examples.
- **REQ-DP-08** Module path `github.com/gsdevme/solis-inverter-manager`, `go 1.27`
  (toolchain `go 1.27.0`). → `go.mod`
