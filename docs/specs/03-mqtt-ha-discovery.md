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
  stably ordered 33-entity catalogue; `BuildDiscovery()` returns one discovery
  `Message` per entity; `BuildState()` marshals a decoded `inverter.Telemetry`
  (plus an externally supplied clock drift) into the retained state document.
- **`internal/mqtt`** — the MQTT5 transport, wrapping `paho.golang/autopaho`. It
  owns the connection manager, the retained `offline` Last Will, QoS-1 publish,
  the `OnConnectionUp` reconnect hook, and clean disconnect.
- **`internal/publisher`** — the glue. `Service` depends on a `Publisher`
  interface (satisfied by `*mqtt.Client` in production, a recording fake in
  tests) and exposes `PublishDiscovery`, `PublishAvailability`, `PublishState`.
  `Collect` is the reusable poll-and-decode unit that turns sidecar reads into an
  `inverter.Telemetry`.

**Phase 4 is read-only:** it emits sensors and one binary_sensor only. Writable
controls (`number`/`switch`/`select`) and the READ-BEFORE-WRITE write-guard are
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
  defaults to `homeassistant`; `<component>` is `sensor` or `binary_sensor`. This
  is the **object_id form** of the discovery topic (`<serial>_<key>` is the node
  segment, not a `node_id/object_id` pair).

In every discovery payload `~` is set to `<base>`, and `~` is the **only**
abbreviated key: `state_topic` and `availability_topic` are written as `~/state`
and `~/availability`, every other key is spelled out in full.

## Device block

Every entity carries the **identical** device block, so Home Assistant groups all
33 entities under one device:

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
removes the entity in Home Assistant. The manager does not do this in normal
operation — it only ever publishes full configs — but the convention is noted so
retained-topic cleanup is unambiguous.

## State document

One retained JSON object at `<base>/state`, built by `BuildState` from
`internal/homeassistant/state.go`. Its json tags are exactly the 33 entity keys
(the `State` struct is the contract: no entity without a field, no field without
an entity). Each entity reads its own field via `value_template`:

- sensor: `{{ value_json.<key> }}`
- binary_sensor: `{{ 'ON' if value_json.<key> else 'OFF' }}`

Value encodings of note:
- `rtc` is an RFC3339 timestamp string (`device_class: timestamp`).
- `rtc_drift` is the clock drift in **seconds** (float; `device_class: duration`,
  unit `s`). Drift is computed by the caller (`Drift(t.Time, time.Now())` in the
  publisher) and passed in, because the builder is pure and must not read the
  clock.
- `work_mode` is a human label derived from the 43110 bitfield
  (`workModeLabel`): the two named setpoints render as `Optimal income ON` /
  `Optimal income OFF`; otherwise the active flags (`self_use`, `timed`,
  `allow_grid_charge`) are joined with `+`, falling back to the raw register
  value when no known flag is set.

## Entity table

All 33 entities, mirrored row-for-row from `internal/homeassistant/entities.go`.
Blank cells mean the field is empty in the code and the key is therefore omitted
from that entity's discovery payload.

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

Notes:
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
  Will, so the explicit `offline` is what remains retained.

## Reconnect republish

The MQTT client fires an `OnConnectionUp` hook on **every** (re)connection. The
hook (`republish` in `serve.go`) republishes, in order:

1. discovery (all entities),
2. availability = `online`,
3. the last cached state, if a poll has succeeded.

This makes HA rebuild its entities and restore last values after a broker
restart. State is cached under a mutex (`lastState`) because the poll goroutine
writes it and the transport goroutine reads it.

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

## Read-only note (Phase 4) and Phase 5 forward reference

Phase 4 emits **read-only** sensors only. The writable controls
(`number`/`switch`/`select` for charge/discharge amps and work mode, plus
optional RTC auto-sync) and the READ-BEFORE-WRITE write-guard are **Phase 5**.
The CRITICAL section below is that requirement, retained here as the forward
reference; do not treat it as implemented in Phase 4.

## CRITICAL design constraint — READ-BEFORE-WRITE write-guard

> This is a first-class requirement, fully specified in a later phase.

The manager MUST **always read a register's current value first and only issue a
Modbus write (fc06) if the desired value differs** from what is already stored.
Never write unconditionally.

**Why:** the inverter's holding registers are **flash-backed**; every needless write
causes **flash wear** and shortens the datalogger/inverter's life. A no-op write is
never harmless.

**Scope:** applies to **all** setpoints —
- timed charge current `43141`,
- timed discharge current `43142`,
- work-mode `43110`,
- and any RTC auto-sync: only write `43000–43005` when measured clock **drift
  exceeds a threshold** (never on every poll).

TODO: specify the compare/skip logic, tolerance for the RTC drift threshold, and how
skipped writes are logged/surfaced.
