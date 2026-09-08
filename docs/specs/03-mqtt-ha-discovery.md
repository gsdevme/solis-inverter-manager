# 03 — MQTT & Home Assistant discovery

> **Status: skeleton.** Full content authored in a later phase.

`internal/homeassistant` is a **pure payload builder**; `internal/mqtt` is the
MQTT5 transport; `internal/publisher` glues them together.

## Topics (TODO)

TODO: base topic (`<MQTT_TOPIC_PREFIX>/...`), retained JSON state document(s),
service-wide availability topic (LWT), discovery topics
`<HA_DISCOVERY_PREFIX>/<component>/.../config`.

## Entities (TODO)

TODO: **read** sensors — battery SOC/SOH, battery/grid/PV/AC power, energy totals,
temperature, frequency, work-mode, RTC-drift sensor.

TODO: **writable controls** — timed charge current (43141), timed discharge current
(43142), work-mode / "optimal income" toggle (43110), and optional RTC auto-sync
(43000–43005). Each maps to an HA `number`/`select`/`switch`.

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

## Transport & availability (TODO)

TODO: MQTT5, LWT retained `offline`, `online` on connect, retained + QoS 1,
republish availability + discovery + state on every (re)connection.
