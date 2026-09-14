# 03 — MQTT & Home Assistant discovery

Phase 4 (#20/#21) publishes the inverter's decoded telemetry to MQTT with Home
Assistant autodiscovery. This spec is the source of truth for the topic scheme,
the discovery payload shape, the state document, and the availability lifecycle.
It matches the committed implementation in `internal/homeassistant`,
`internal/mqtt`, `internal/publisher`, and `internal/cmd/serve.go`.

## Overview

The work is split across three packages plus the serve wiring:

- **`internal/homeassistant`** — a **pure payload builder**. It has no I/O and
  never reads the wall clock. `Config` derives the topics; `Entities()` is the
  stably ordered 41-entity catalogue (36 read-only entities plus the five
  command entities, which `BuildDiscovery` skips when controls are disabled);
  `BuildDiscovery()` returns one discovery `Message` per entity; `BuildState()`
  marshals a decoded `inverter.Telemetry` (plus an externally supplied clock
  drift) into the retained state document.
- **`internal/mqtt`** — the MQTT5 transport, wrapping `paho.golang/autopaho`. It
  owns the connection manager, the retained `offline` Last Will, QoS-1 publish,
  the `OnConnectionUp` reconnect hook, and clean disconnect.
- **`internal/publisher`** — the glue. `Service` depends on a `Publisher`
  interface (satisfied by `*mqtt.Client` in production, a recording fake in
  tests) and exposes `PublishDiscovery`, `PublishDiscoveryRemovals`,
  `PublishAvailability`, `PublishState`.
  `Collect` is the reusable poll-and-decode unit that turns sidecar reads into an
  `inverter.Telemetry`.

**Phase 4 is read-only:** it emits sensors and one binary_sensor only. Writable
controls (`number`/`select`/`button`) and the READ-BEFORE-WRITE write-guard are
**Phase 5** — see the CRITICAL section below, retained here as the forward
reference.

## Topic scheme

- **Base topic** = `<MQTT_TOPIC_PREFIX>/<INVERTER_SERIAL>` (`Config.BaseTopic()`).
  `MQTT_TOPIC_PREFIX` defaults to `solis`. `INVERTER_SERIAL` is the **datalogger
  (Solarman) serial** — the device identifier in HA. The serial therefore appears
  in retained broker payloads (topics and the device block); this is accepted as
  a local trust boundary (a private broker on the LAN), not a secret to redact on
  the wire.
- **State topic** = `<base>/state` (`Config.StateTopic()`) — a single retained
  JSON document.
- **Availability topic** = `<base>/availability` (`Config.AvailabilityTopic()`) —
  the LWT + explicit availability topic.
- **Discovery topic** =
  `<HA_DISCOVERY_PREFIX>/<component>/<serial>_<key>/config`. `HA_DISCOVERY_PREFIX`
  defaults to `homeassistant`; `<component>` is the entity's component —
  `sensor` or `binary_sensor` for the read-only catalogue, `number`, `select` or
  `button` for the controls. This is the **object_id form** of the discovery
  topic (`<serial>_<key>` is the node segment, not a `node_id/object_id` pair).

In every discovery payload `~` is set to `<base>`, and `~` is the **only**
abbreviated key: `state_topic` and `availability_topic` are written as `~/state`
and `~/availability`, every other key is spelled out in full.

## Device block

Every entity carries the **identical** device block, so Home Assistant groups all
41 entities under one device:

```json
{
  "identifiers": ["<serial>"],
  "manufacturer": "Solis",
  "model": "RHI-3.6K-48ES-5G",
  "name": "Solis Inverter"
}
```

The constants are `Manufacturer`, `Model`, `DeviceName` in `entities.go`;
`identifiers` is `[<serial>]`.

## Discovery config shape

Keys present in every discovery payload (`buildEntityPayload`):

| Key | Value |
| --- | --- |
| `~` | `<base>` (the only abbreviated key) |
| `name` | entity `Name` |
| `unique_id` | `<serial>_<key>` |
| `object_id` | `<serial>_<key>` |
| `state_topic` | `~/state` |
| `availability_topic` | `~/availability` |
| `payload_available` | `online` |
| `payload_not_available` | `offline` |
| `device` | the shared device block |
| `value_template` | see below |

Emitted only when non-empty for the entity: `device_class`, `state_class`,
`unit_of_measurement` (from `Unit`), `entity_category` (from `Category`,
`diagnostic`).

`value_template`:
- sensor: `{{ value_json.<key> }}`
- binary_sensor: `{{ 'ON' if value_json.<key> else 'OFF' }}` and the payload adds
  `payload_on: "ON"` / `payload_off: "OFF"`.

**Deletion semantics:** publishing an empty retained payload to a discovery topic
removes the entity in Home Assistant. The manager uses this only to clear configs
it no longer publishes — see *Discovery removal* under the write path; every
entity in the catalogue always gets a full config.

## State document

One retained JSON object at `<base>/state`, built by `BuildState` from
`internal/homeassistant/state.go`. Its json tags are the 36 read-only entity
keys plus the four control-readback fields (`set_charge_current`,
`set_discharge_current`, `optimal_income`, `boost_select`) — 40 tags (the
`State` struct is the contract: no entity without a field, no field without an
entity). Each entity reads its own field via `value_template`:

- sensor: `{{ value_json.<key> }}`
- binary_sensor: `{{ 'ON' if value_json.<key> else 'OFF' }}`

Value encodings of note:
- `rtc` is an RFC3339 timestamp string (`device_class: timestamp`).
- `rtc_drift` is the clock drift in **seconds** (float; `device_class: duration`,
  unit `s`). Drift is computed by the caller (`Drift(t.Time, time.Now())` in the
  publisher) and passed in, because the builder is pure and must not read the
  clock.
- `work_mode` is the energy-storage mode label derived from the 43110 bitfield
  (`workModeLabel`): bit 0 renders as `Self Use`, the only storage mode observed
  on this unit and the label for both observed register values (35 and 33, which
  differ only in the Optimal Income bit `select.optimal_income` owns). Any other
  combination joins the active flags (`timed`, `allow_grid_charge`) with `+`,
  falling back to the raw register value when no known flag is set, so an
  unknown mode is never mislabelled.
- `boost_select` is the `select.boost_select` option derived from slot 3 — `Off`,
  one of the eight `Charge|Discharge NN min` options, or JSON `null` for a window
  that matches no option (see `09-schedule-controls.md`).
- `tou_window`, `boost`, `boost_ends_at` are the derived schedule fields, e.g.
  `tou_window: "23:31–05:29"` — see the "State document additions" table below
  for their shape and source.

## Entity table

All 36 read-only entities, mirrored row-for-row from
`internal/homeassistant/entities.go`; the five command entities that follow them
in the same catalogue are tabled under *Control entities* below. Blank cells mean
the field is empty in the code and the key is therefore omitted from that
entity's discovery payload.

| # | Key | Component | device_class | state_class | unit | category |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | `battery_voltage` | sensor | voltage | measurement | V | |
| 2 | `battery_current` | sensor | current | measurement | A | |
| 3 | `battery_power` | sensor | power | measurement | W | |
| 4 | `battery_charging` | binary_sensor | battery_charging | | | |
| 5 | `battery_soc` | sensor | battery | measurement | % | |
| 6 | `battery_soh` | sensor | battery | measurement | % | diagnostic |
| 7 | `bms_voltage` | sensor | voltage | measurement | V | diagnostic |
| 8 | `bms_current` | sensor | current | measurement | A | diagnostic |
| 9 | `pv1_voltage` | sensor | voltage | measurement | V | |
| 10 | `pv1_current` | sensor | current | measurement | A | |
| 11 | `pv2_voltage` | sensor | voltage | measurement | V | |
| 12 | `pv2_current` | sensor | current | measurement | A | |
| 13 | `pv_total_power` | sensor | power | measurement | W | |
| 14 | `grid_power` | sensor | power | measurement | W | |
| 15 | `grid_total_import` | sensor | energy | total_increasing | kWh | |
| 16 | `grid_import_today` | sensor | energy | total | kWh | |
| 17 | `grid_total_export` | sensor | energy | total_increasing | kWh | |
| 18 | `grid_export_today` | sensor | energy | total | kWh | |
| 19 | `ac_active_power` | sensor | power | measurement | W | |
| 20 | `inverter_temperature` | sensor | temperature | measurement | °C | |
| 21 | `grid_frequency` | sensor | frequency | measurement | Hz | |
| 22 | `house_load` | sensor | power | measurement | W | |
| 23 | `generation_today` | sensor | energy | total | kWh | |
| 24 | `generation_yesterday` | sensor | energy | | kWh | diagnostic |
| 25 | `battery_total_charge` | sensor | energy | total_increasing | kWh | |
| 26 | `battery_charge_today` | sensor | energy | total | kWh | |
| 27 | `battery_total_discharge` | sensor | energy | total_increasing | kWh | |
| 28 | `battery_discharge_today` | sensor | energy | total | kWh | |
| 29 | `status` | sensor | | | | diagnostic |
| 30 | `operating_status` | sensor | | | | diagnostic |
| 31 | `work_mode` | sensor | | | | diagnostic |
| 32 | `rtc` | sensor | timestamp | | | diagnostic |
| 33 | `rtc_drift` | sensor | duration | | s | diagnostic |
| 34 | `tou_window` | sensor | | | | |
| 35 | `boost` | sensor | | | | |
| 36 | `boost_ends_at` | sensor | timestamp | | | |

Notes:
- **`work_mode` is named "Energy storage mode"** — the Solis app's own term for
  the `43110` mode — and reports `Self Use`; the key is unchanged.
- **`tou_window`, `boost`, and `boost_ends_at` are primary entities** (no
  `entity_category`), unlike the diagnostic `rtc`/`status`/`rtc_drift` sensors:
  they surface the derived, actionable schedule (the joined ToU window and the
  current boost slot), not raw diagnostics, so do not reclassify them as
  `diagnostic`.
- **`total` vs `total_increasing`.** Daily counters that reset at midnight use
  `state_class: total` (`grid_import_today`, `grid_export_today`,
  `generation_today`, `battery_charge_today`, `battery_discharge_today`).
  Lifetime counters that only ever climb use `total_increasing`
  (`grid_total_import`, `grid_total_export`, `battery_total_charge`,
  `battery_total_discharge`). `generation_yesterday` is a fixed daily figure with
  no meaningful `state_class`, so it carries none and is marked `diagnostic`.
- **Signed power is one entity.** `battery_power` and `grid_power` are single
  **signed** power sensors (positive/negative encodes direction), not split into
  separate charge/discharge or import/export entities. `battery_charging` is a
  derived binary_sensor for the charge/discharge direction.

## QoS / retain per message type

The transport fixes **QoS 1** on every publish (`Client.Publish`), so only the
retain flag varies:

| Message | Topic | Retain | QoS |
| --- | --- | --- | --- |
| Discovery config | `<discovery_prefix>/<component>/<serial>_<key>/config` | yes | 1 |
| State | `<base>/state` | yes | 1 |
| Availability | `<base>/availability` | yes | 1 |
| LWT (Will) | `<base>/availability` (`offline`) | yes | 1 |

## Availability / LWT lifecycle

- The MQTT connection registers a **retained `offline` Last Will** on
  `<base>/availability` at QoS 1 (`buildClientConfig` `WillMessage`), with a will
  delay interval of 2× keepalive.
- After a successful connect the manager publishes **`online` retained** to the
  same topic.
- On **graceful shutdown** the manager publishes **`offline` retained**
  explicitly, then **disconnects cleanly** — a clean disconnect suppresses the
  Will, so the explicit `offline` is what remains retained. `Disconnect` first
  drains any in-flight reconnect-republish goroutine (see below), so nothing
  republishes `online` after the explicit `offline`.

### Will delay is an intentional flap debounce

The Will has a **delay interval of 2× keepalive** (`keepAliveSeconds` = 20, so
**~40s**). This is deliberate: on an *unclean* loss (crash, network partition, pod
OOM-kill) the broker waits ~40s before publishing the retained `offline` Will,
debouncing brief flaps so HA does not churn every entity to `unavailable` and back
on a transient blip. The trade-off — accepted by the owner — is that HA may show a
genuinely dead pod as `online` for up to ~40s after it dies. A *graceful* shutdown
does not pay this cost: the explicit `offline` is published immediately and the clean
disconnect suppresses the Will entirely.

## Reconnect republish

The MQTT client fires an `OnConnectionUp` hook on **every** (re)connection. The
hook (`republish` in `serve.go`) republishes, in order:

1. discovery (all entities),
2. availability = `online`,
3. the last cached state, if a poll has succeeded.

This makes HA rebuild its entities and restore last values after a broker
restart. State is cached under a mutex (`lastState`) because the poll goroutine
writes it and the transport goroutine reads it.

The hook runs in a goroutine under a **serve-lifetime context** owned by the client
(`hookCtx`), tracked by a `WaitGroup`. `Disconnect` cancels that context and drains
the goroutine (bounded by the shutdown context) *before* the clean disconnect, so a
slow republish is torn down cleanly rather than leaked or left publishing against a
closing transport.

## Readiness contract

Readiness (`/readyz`, see `06-lifecycle-health.md`) is coupled to this publish path:
the manager is **ready only after a poll whose `Collect` (telemetry read) *and*
`PublishState` both succeed** — a decode that never reaches the broker does not count
as ready. It flips **not-ready after `FAILURE_THRESHOLD` consecutive poll failures**,
recovering on the next fully-successful poll. Because the ~40s Will delay can leave a
dead pod's availability showing `online`, `/readyz` — not the MQTT availability topic
— is the authoritative signal for orchestrators (Kubernetes probes).

## MODE gating

`internal/config` resolves a single `MODE` switch, and MQTT is gated on whether a
broker URL is configured:

- **`live`** requires an MQTT broker (`config` fail-fast). The manager connects,
  publishes discovery + availability eagerly, and publishes state each poll.
- **`mock`** runs the same `Collect` poll unit. If a broker **is** configured it
  publishes exactly as in live; if **no** broker is configured it skips MQTT
  entirely and **logs decoded telemetry** each poll instead, so the pipeline stays
  observable with no broker required.

Poll cadence is the **interim** fixed-interval ticker at `POLL_INTERVAL`
(default 60s, floored by `MinPollInterval`) with no backoff. The real scheduler
(retries, backoff, guarded writes) is **Phase 6** (`internal/scheduler`).

## Read-only note (Phase 4)

Phase 4 emits **read-only** sensors only. The writable controls and the
READ-BEFORE-WRITE write-guard are **Phase 5**, specified in the sections below
(CRITICAL write-guard, then the write path). Do not treat them as implemented in
Phase 4.

## CRITICAL design constraint — READ-BEFORE-WRITE write-guard

> This is a **first-class requirement** and governs every write the manager ever
> issues (REQ-HA-10).

The manager MUST **always read a register's current value first and only issue a
Modbus write (fc06) if the desired value differs** from what is already stored.
Never write unconditionally.

**Why:** the inverter's holding registers are **flash-backed** (see
[`docs/phase0/findings.md`](../phase0/findings.md) — the write-path findings and
RTC-drift notes). Every needless write causes **flash wear** and shortens the
datalogger/inverter's life. A no-op write is **never** harmless, even though the
inverter acknowledges it: the Phase 0 probe confirmed a no-op `350→350` fc06 on
`43141` is accepted silently, so the inverter will not protect us — the manager
must.

**The guard algorithm — run on every write, no exceptions:**

1. **Read** the register's current value (fc03).
2. **Compare** to the desired value.
3. **Write** (fc06) **only if they differ**.
4. **Re-read** to confirm the stored value now equals what was written.

- A write where **desired == current is SKIPPED** — **no fc06 is issued** — and
  the skip is **logged at `info`** (flash-wear avoidance is the whole point, so
  the skip is a first-class, observable outcome, not a silent short-circuit).
- A re-read that **does not match** the written value is an **error**: it is
  **logged** and is **non-fatal** (the manager keeps running and the next poll/
  command re-evaluates).

**Scope:** applies to **all** setpoints —
- timed charge current `43141` (REQ-HA-08),
- timed discharge current `43142` (REQ-HA-08),
- work-mode `43110` (REQ-HA-09, read-modify-write — see below),
- the RTC block `43000–43005` (REQ-HA-13), guarded **per register**: the
  button writes only the registers of the six whose value differs,
- and the hour/minute registers of the three timed slots, guarded **per
  register**: slot 3 (`43163`–`43170`) by `select.boost_select` and by the
  per-poll schedule reconcile, slots 1–2 (`43143`–`43150`, `43153`–`43160`) by
  the reconcile alone (`09-schedule-controls.md`, REQ-HA-16/REQ-HA-17).
  The slot leading pairs `43151`/`43152` and `43161`/`43162` are **never**
  written; `43141`/`43142` are written only by the two amp controls above, never
  by the schedule path.

## Write path (Phase 5) — command topics, controls, state, RTC

Phase 5 adds the **write half** of the manager: native Home Assistant controls
that write setpoints back to the inverter, each one gated by the READ-BEFORE-WRITE
guard above. Phase 5 lives in a new `internal/controls` package (the guard +
validation + command handlers); `internal/homeassistant` gains the control
entities in its catalogue; `internal/mqtt` gains command-topic subscription; and
`internal/cmd/serve.go` wires the subscription into the reconnect hook.

### Command topics

- **Command topic** = `~/<key>/set`, where `~` is the per-inverter base topic
  (`<MQTT_TOPIC_PREFIX>/<INVERTER_SERIAL>`, as for state/availability) and
  `<key>` is the entity key (e.g. `<base>/set_charge_current/set`). Each control's
  discovery payload carries its own `command_topic: ~/<key>/set`.
- The manager **subscribes to `<base>/+/set`** (single wildcard) at connect and
  **re-subscribes on every reconnect**, alongside the existing discovery /
  availability / state republish on `OnConnectionUp` (REQ-HA-11). The single
  subscription covers all five controls; the `<key>` segment is routed to the
  matching handler.

### Control entities

Five writable entities join the catalogue (the read-only table above is unchanged
at 36; with controls enabled the device carries 41). Registers are the Phase 0
confirmed addresses — never invent them.

| Key | Component | Range / payloads | Register | Encoding |
| --- | --- | --- | --- | --- |
| `set_charge_current` | `number` | 0–60 A, step 0.1 | `43141` (RegTimedChargeCurrent) | U16, `÷10` A |
| `set_discharge_current` | `number` | 0–60 A, step 0.1 | `43142` (RegTimedDischargeCurrent) | U16, `÷10` A |
| `optimal_income` | `select` | `"Run"` / `"Stop"` | `43110` (RegWorkMode) bit 1 | read-modify-write; `33`↔`35` |
| `boost_select` | `select` | `Off`, `Charge 15\|30\|45\|60 min`, `Discharge 15\|30\|45\|60 min` | slot 3 H/M `43163–43170` | all eight slot registers pass the guard; the unused direction is asserted empty and is always cleared first (charge block first only when both directions are unset or both are set); within a direction the order is start hour, start minute, end hour, end minute |
| `rtc_sync` | `button` | press (any payload) | `43000–43005` (RegRTCSet) | U16×6 local datetime; `entity_category: diagnostic` |

- **Amp numbers** (REQ-HA-08) write the scaled integer `round(amps, 1) * 10` via
  fc06, behind the guard. The `0–60 A` range is the deliberate HA clamp — the unit
  physically accepts up to 100 A (Phase 0 ambiguity #9), but the control is capped.
- **Optimal-income select** (REQ-HA-09, REQ-HA-15) is a **read-modify-write that
  flips ONLY bit 1** of `43110`, preserving every other bit: read `43110`,
  set/clear bit 1 per the `Run`/`Stop` option, write back only if the result
  differs. On this unit that is the `35` (Run) ↔ `33` (Stop) transition, but the
  implementation toggles the bit rather than writing the literals, so the other
  flags (self-use bit 0, grid-charge bit 5) are never clobbered. Its two options
  are the Solis app's own vocabulary, and the state document carries the same two
  strings.
- **Boost select** (REQ-HA-16) programs timed slot 3 — the start/end hour and
  minute of one direction, the other direction asserted empty — through the
  guard, one register at a time. The planning rules (quarter-hour end, the
  midnight and ToU-overlap refusals) and the per-poll reconcile that clears an
  expired boost are specified in
  [`09-schedule-controls.md`](09-schedule-controls.md).
- **RTC button** (REQ-HA-13) — see the RTC section below.

### Discovery removal

`BuildDiscoveryRemovals` lists the discovery topics of entities this version no
longer publishes, each with an **empty retained payload** — the deletion
semantics above. `PublishDiscoveryRemovals` sends them immediately after every
`PublishDiscovery`, at startup and on every reconnect, and is **not** gated on
`CONTROLS_ENABLED`: a stale entity must go either way. Publishing an empty
payload to a topic that is already clear is a no-op, so the pass is idempotent.
The only removal today is `<prefix>/switch/<serial>_optimal_income/config`, the
topic the pre-B2 on/off switch used before `select.optimal_income` took the key
(REQ-HA-15).

### State document additions

Four fields are added to the **same** retained `~/state` JSON document (not a
separate topic) so the controls read their current value back through the existing
`value_template {{ value_json.<key> }}` mechanism, exactly as the sensors do. Each
control's discovery payload therefore sets `state_topic: ~/state`.

| Field | Type | Source |
| --- | --- | --- |
| `set_charge_current` | float (amps) | `43141 ÷ 10` |
| `set_discharge_current` | float (amps) | `43142 ÷ 10` |
| `optimal_income` | string `"Run"`/`"Stop"` | bit 1 of `43110` |
| `boost_select` | string option or `null` | slot 3 (`43163–43170`) via `schedule.BoostSelectState` |

The derived schedule sensors `tou_window`, `boost`, and `boost_ends_at` are
**not** control-readback fields — they carry no command topic and exist
whether or not `CONTROLS_ENABLED` is set. They are three of the 36 entities in
the read-only entity table above (rows 34–36), not an addition beyond it; they
are listed here only because they share the same `~/state` document and the
same extended holding-bank read as the four control fields:

| Field | Type | Source |
| --- | --- | --- |
| `tou_window` | string `"HH:MM–HH:MM"` or `null` | `internal/schedule.ToU` + `FormatWindow` — see REQ-HA-14 for the join/fallback/null cases |
| `boost` | string | `internal/schedule.Boost.String()`, derived from slot 3 |
| `boost_ends_at` | RFC3339 string or `null` | `internal/schedule.Boost.EndsAt`, computed in `serve.go` with the shared clock |

`boost` reflects the slot-3 *configuration*, not whether the window is
currently running; a `boost_ends_at` in the past means the configured window
has elapsed and slot 3 has not been cleared (B2 adds the write path that
clears it).

The values come from **one extra holding-bank read per poll**: a single
`ReadHolding(43110, 61)` — one fc03 frame now covers `43110` through the three
timed slots at `43141`–`43170`, and `61` registers is well under the
125-register-per-frame cap (REQ-SD-02). The four control-readback fields above
extend the `State` DTO without adding entities — the read-only 36-entity table
is unchanged by them. `BuildState` stays pure — the holding words are decoded
and passed in, as with `rtc_drift`; `boost_ends_at` alone needs a clock, so it
is likewise computed by the caller (`serve.go`) rather than by `BuildState`
itself.

`tou_window` and `boost_ends_at` are JSON `null` when unset (pointer fields, no
`omitempty` — the keys are always present, never omitted); see REQ-HA-14 for
exactly when `tou_window` is the joined window, slot 1 alone, or `null`. Home
Assistant renders a JSON `null` through `{{ value_json.boost_ends_at }}` as the
Jinja value `None`, which a `device_class: timestamp` sensor then shows as
state *unknown* — this is the intended behaviour when there is no boost
window, and needs no custom `value_template`.

### Server-side validation

Every inbound command is validated before the guard runs (REQ-HA-12); a bad
command is **logged and dropped** — it never crashes the manager and never reaches
fc06:

- **Amp numbers** are parsed as float. `NaN`, `±Inf`, or unparseable payloads are
  **rejected outright** (logged and dropped, no write). Any successfully-parsed
  **finite** float is **clamped to the 0–60 A** control range (`ClampHAChargeAmps`)
  and then written under the read-before-write guard — **no numeric value is ever
  rejected for being out of range; only a value that fails to parse is rejected**
  (Ruling R6, matching the controls action `EncodeAmps(ClampHAChargeAmps(parse))`).
- **The selects** accept only their own discovery `options`, surrounding space
  trimmed: `optimal_income` takes `Run` or `Stop` (case-insensitive),
  `boost_select` takes one of its nine option strings exactly. Anything else is
  logged and dropped. A valid `boost_select` option the manager refuses to
  program (Optimal Income is `Stop`, the window would reach midnight, or it
  overlaps the ToU window) is likewise never written; the state document is
  republished so the select snaps back — see
  [`09-schedule-controls.md`](09-schedule-controls.md).

### RTC sync button

`rtc_sync` is a **manual button only** this phase (REQ-HA-13). A press triggers a
**guarded write of the six RTC holding registers `43000–43005`** to the current
wall-clock time (naive local datetime — the inverter has no timezone register, per
Phase 0). The guard applies **per register**: read the current six words, compare
component-by-component, and write via fc06 **only the registers that differ**
(skips logged at `info`, mismatched re-reads logged as non-fatal errors, as for
every guarded write).

**Periodic / threshold-gated RTC auto-sync is explicitly DEFERRED to the Phase 6
scheduler** (`internal/scheduler`) — this phase ships the manual button only. The
drift-threshold logic flagged in `docs/phase0/findings.md` belongs there, not here.

### Kill-switch — `CONTROLS_ENABLED` (Ruling R1)

Config exposes `CONTROLS_ENABLED` (bool, **default `true`**). **RULING R1 —
disabled behaviour is exactly:** when `CONTROLS_ENABLED=false` the manager

- **OMITS the five command entities from discovery** (it does **not**
  publish-them-unavailable — the entities simply never appear),
- does **not** subscribe to the command topic `<base>/+/set`, and
- runs **no per-poll schedule reconcile** and no RTC auto-sync — both need the
  write path (REQ-HA-17, REQ-SC-06).

The four `~/state` setpoint fields **may still be published** in the disabled case
— they are read-only telemetry and harmless without their controls. Discovery
removals are published either way (see *Discovery removal*).
