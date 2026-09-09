---
name: home-assistant-mqtt-discovery
description: Use when building or reviewing MQTT payloads and topics for Home Assistant autodiscovery (discovery configs, device blocks, availability/LWT, device_class/state_class, shared-state-topic pattern). Source of truth for internal/homeassistant.
---

# Home Assistant MQTT autodiscovery

Home Assistant (HA) can create entities automatically from **retained** MQTT
"discovery" config messages. This skill captures the rules this project follows.

## Topic structure

Per-entity discovery topic:

```
<discovery_prefix>/<component>/<node_id>/<object_id>/config
```

- `discovery_prefix` — default `homeassistant`.
- `component` — `sensor`, `binary_sensor`, `number`, `select`, `switch`, etc.
- We use `<discovery_prefix>/<component>/<serial>_<key>/config` (object_id only form),
  where `<serial>` identifies the inverter (datalogger serial, or the inverter serial
  read as ASCII from input `33004–33011`).
- Publish **retained, QoS 1** so HA rebuilds entities after a broker restart with no
  producer restart. An empty retained payload on the same topic **deletes** the entity.

## Shared state topic pattern (preferred)

Instead of one topic per value, publish **one retained JSON document** per device and
point every entity at it with a `value_template`:

```json
{ "state_topic": "solis/<serial>/state",
  "value_template": "{{ value_json.battery_soc }}" }
```

Benefits: fewer topics, atomic multi-value updates, one retained message to restore
full state. Booleans render via `{{ 'ON' if value_json.battery_charging else 'OFF' }}`
with `payload_on: ON` / `payload_off: OFF`.

## The `~` base-topic abbreviation

Set `~` once and reference it with a leading `~`:

```json
{ "~": "solis/<serial>",
  "state_topic": "~/state",
  "availability_topic": "~/availability" }
```

## Shared device block

Give every entity the **same** `device` block so HA groups them under one device:

```json
{ "device": {
    "identifiers": ["<serial>"],
    "manufacturer": "Solis",
    "model": "RHI-3.6K-48ES-5G",
    "name": "Solis Inverter" } }
```

`unique_id` and `object_id` = `<serial>_<key>` (stable, unique). `unique_id` is required
for the entity to be editable in the HA UI.

## Availability (LWT)

- Register a **Last Will** on `~/availability` = `offline`, retained.
- Publish `online` (retained) after connect.
- On graceful stop, publish `offline` (retained) **explicitly**, then disconnect.
- Every entity sets `availability_topic: ~/availability`, `payload_available: online`,
  `payload_not_available: offline`. All entities go unavailable together on disconnect.
- Distinguish MQTT availability from inverter reachability: if the sidecar poll fails,
  values go stale — consider a separate "last update" timestamp entity rather than
  flapping availability on every laggy read.

## device_class / state_class cheatsheet (solis entities)

- **Power** (grid, battery, PV, AC, house load, W): `device_class: power`,
  `state_class: measurement`. Grid/battery power are **signed** (see the register map);
  sign encodes direction — expose the signed value, don't split into two entities.
- **Energy** (kWh): lifetime counters (`33161/33165/33169/33173`) →
  `device_class: energy`, `state_class: total_increasing`. **Daily** counters
  (generation/charge/discharge/import/export today) **reset at midnight** — like an
  odometer that resets, use `state_class: total` (with `last_reset` if modelled) to
  avoid spurious "reset" spikes, not `total_increasing`.
- **Voltage / current / temperature / frequency**: matching `device_class`
  (`voltage`, `current`, `temperature`, `frequency`), `state_class: measurement`.
- **Battery SOC / SOH** (%): `device_class: battery`, `state_class: measurement`.
- **RTC drift** (seconds): `device_class: duration`, `unit_of_measurement: s`. RTC itself
  is a `timestamp` sensor (RFC3339 string, no unit).
- **binary_sensor**: `battery_charging` from the direction flag (`33135`: 0=charge);
  `problem` from status.
- **Controls** (Phase 5): charge/discharge amps as `number` (min 0, max 60, step 0.1,
  `unit_of_measurement: A`); work mode as `select` or `switch` (the 43110 bitfield —
  35 "optimal income ON" / 33 OFF, flipping bit 1); each writes back through the
  read-before-write guard.
- `entity_category: diagnostic` demotes non-primary entities (SOH, BMS voltage,
  RTC drift, last-updated) out of the main area.

## Common abbreviations (optional, compact payloads)

`~`, `stat_t` (state_topic), `avty_t` (availability_topic), `json_attr_t`
(json_attributes_topic), `dev` (device), `uniq_id`, `obj_id`, `name`, `dev_cla`
(device_class), `stat_cla` (state_class), `unit_of_meas`, `val_tpl` (value_template),
`ent_cat` (entity_category), `pl_on`/`pl_off`, `pl_avail`/`pl_not_avail`. Full keys are
equally valid; don't mix half-and-half within a payload confusingly.

## Pitfalls

- Forgetting `retain=true` → entities vanish after a broker restart.
- `total_increasing` on a **daily** counter that resets at midnight → false "reset"
  energy spikes; use `total`.
- Splitting a signed S32 (grid/battery power) into two entities → the legacy app's bug;
  publish one signed value.
- Reusing a `unique_id` across entities → HA rejects/merges them.
- Not setting `availability_topic` on entities → they never show "unavailable".
- Changing a discovery topic's `<object_id>` orphans the old retained config (publish
  an empty payload to the old topic to clean it up).
