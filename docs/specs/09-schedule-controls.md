# 09 — Schedule controls (Tariff & Boost, Stage B2)

Stage B2 (#28) adds the **write side** of the timed schedule that B1 (REQ-HA-14)
made readable: a `select.boost` that programs slot 3, manager-owned assertion of
the time-of-use (ToU) window in slots 1–2, and a per-poll reconcile that clears an
expired boost. It also reshapes the work-mode controls to match the Solis app's
own vocabulary. This spec is the source of truth for that behaviour; `REQ-HA-15`
to `REQ-HA-17` and `REQ-CF-07` in `REQUIREMENTS.md` carry the stable IDs.

Register ground truth is `docs/phase0/findings.md` (Stage A section) and
`02-register-map.md`. The READ-BEFORE-WRITE guard in `03-mqtt-ha-discovery.md`
(REQ-HA-10) governs **every** write described here.

## Vocabulary (matches the Solis app)

| App setting | Register | Values | HA entity after B2 |
|---|---|---|---|
| Energy storage mode | `43110` bit 0 (+ unconfirmed bits for the other modes) | Self Use · Feed In Priority · Backup · Off Grid | `sensor.work_mode` "Energy storage mode", **read-only**, shows `Self Use` |
| Optimal Income | `43110` bit 1 | Run · Stop | `select.optimal_income` **Run / Stop** (replaces the on/off switch) |
| Timed charge/discharge slots 1–3 | `43141`–`43170` | H/M windows | `select.boost`, `tou_window`, `boost`, `boost_ends_at` |

"Optimal Income: Run" is the master enable for the whole timed schedule. Only
two values of `43110` have ever been observed on this inverter (35 = Run, 33 =
Stop); the other three storage modes are **unconfirmed** and are never written
(see *Deferred* at the end).

## Design: declarative reconcile (approach A)

The manager holds a **desired schedule** and makes the inverter match it:

- slots 1–2 = the configured ToU window (`TOU_WINDOW`) split at midnight;
- slot 3 = the active boost, or empty.

There are **no timers, no time-of-day job and no persisted boost state**. Every
poll already reads `43110`–`43170` in one frame (REQ-HA-14); a reconcile step
runs immediately after the poll — the same hook as the RTC auto-sync
(`REQ-SC-06`) — and guard-writes only the registers that differ. All HA state
(`select.boost`, `boost`, `boost_ends_at`, `tou_window`) is **derived from the
registers**, so a restart mid-boost simply adopts the running boost, and a
missed poll cannot leave a stale window to re-fire tomorrow: the next poll,
whenever it is, fixes it.

**Contract:** slots 1–3 are manager-owned. Edits made in the Solis app are
reverted within one poll interval (a not-yet-expired slot-3 window is adopted
as a boost, not reverted).

## Configuration (`REQ-CF-07`)

| Env | Default | Meaning |
|---|---|---|
| `TOU_WINDOW` | `23:30-05:30` | `HH:MM-HH:MM`, local time. Crossing midnight is allowed and is the normal case. Empty string disables ToU assertion (slots 1–2 are left untouched). Invalid values fail config validation (`errors.Join`). |
| `CONTROLS_ENABLED` | `true` (existing) | Kill switch, Ruling R1: when `false`, no `select` entities are discovered, no command subscription, **no reconcile** — B2 is fully read-only. |

Times are the deployment's local zone (`TZ`), the same clock the RTC code and
`boost_ends_at` already use.

## Desired schedule

`TOU_WINDOW=S-E` maps to:

- `S > E` (crosses midnight): slot 1 charge = `S→00:00`, slot 2 charge = `00:00→E`;
- `S < E`: slot 1 charge = `S→E`, slot 2 charge empty;
- slot 1 and slot 2 **discharge** windows are asserted empty.

Slot 3 desired = the current slot-3 window while `now < end`, otherwise empty.
"Empty" is all four H/M registers of a window = 0 (see *Open item* on how a
clear is written).

Charge/discharge current is **global** (`43141`/`43142`, the existing `number`
entities) and is not part of the schedule.

## `select.boost` (`REQ-HA-16`)

Options, in order:

```
Off
Charge 15 min | Charge 30 min | Charge 45 min | Charge 60 min
Discharge 15 min | Discharge 30 min | Discharge 45 min | Discharge 60 min
```

The entity key is `boost_select` (the key `boost` belongs to the B1 sensor, and
an entity's key, state tag and command key must coincide).

**Selecting a boost** (command `~/boost_select/set` with one of the option
strings):

1. `start` = `now` truncated to the minute.
2. `end` = the N‑th quarter-hour boundary (`:00/:15/:30/:45`) **strictly after**
   `now`, where N = minutes / 15. Example: 14:07 + "Charge 30 min" → 14:07→14:30
   (boundaries strictly after 14:07 are 14:15, 14:30). Duration therefore lies in
   `((N‑1)·15, N·15]` minutes.
3. **Rejected** (state snaps back to `Off`, warning logged, nothing written) when:
   - `43110` bit 1 is Stop (Optimal Income off) — the user flips
     `select.optimal_income` and retries; the manager never changes the mode
     implicitly;
   - `end` would fall on or after midnight (a window cannot cross midnight);
   - `[start, end)` overlaps the configured ToU window.
4. Otherwise the four H/M registers of the chosen direction in slot 3 are written
   through the guard, **one register at a time, in this order**: start hour,
   start minute, end hour, end minute. The other direction's window is asserted
   empty. Then the state document is refreshed (existing command path).

**Selecting `Off`** clears slot 3 immediately (same ordering).

**Expiry:** any poll where `now ≥ end` clears slot 3. The inverter stops the
window at `end` by itself; clearing only prevents the window firing again
tomorrow, so lagging by up to one poll interval is acceptable and documented.

**Why that write order.** Writes take effect immediately and there is no
multi-register write in the stack (sidecar is fc06-only), so a window is set
one word at a time. Writing start before end makes the one-second transient
either a same-direction superset of the target (on set: `14:07→00:00` before
the end lands) or an already-past window (on clear: `00:00→14:30` after 14:30).
Neither transient can charge or discharge in the wrong direction.

**Never written:** the slot leading pairs `43151`/`43152` and `43161`/`43162`
(unconfirmed meaning; currents are global). Only offsets `+2..+9` of a slot.

**State** (`~/state` key `boost_select`, and the select's `value_template`):
`Off` when slot 3 is empty; otherwise the option whose direction matches and
whose duration is `15·⌈minutes/15⌉` when that is one of 15/30/45/60; otherwise
JSON `null` (HA "unknown") — `sensor.boost` still shows the real window.

## ToU assertion and reconcile (`REQ-HA-17`)

After every successful poll, when controls are enabled:

1. Build the desired schedule (above) from `TOU_WINDOW` and the just-read slots.
2. For every H/M register of slots 1–3 whose desired value differs from the
   read value, `Guard`-write it (read → compare → write → re-read). Equal
   values cost nothing: the guard skips them with no fc06 (REQ-HA-10), so the
   steady state is **zero writes per poll**.
3. Log one line per reconcile that wrote anything (`schedule: reconciled`,
   registers old→new); log nothing when nothing changed.
4. A reconcile error is non-fatal (warn, retry next poll), exactly like RTC
   auto-sync.

The reconcile never touches `43110`, `43141`, `43142` or the leading pairs.

## Work-mode controls reshaped (`REQ-HA-15`)

- `select.optimal_income` with options `Run` / `Stop` **replaces** the
  `switch.optimal_income`. Same key, same guarded read‑modify‑write of **bit 1
  only** (35 ↔ 33, REQ-HA-09), state derived from `43110` (`Run` when bit 1 set).
  Commands other than the two option strings are logged and dropped (REQ-HA-12).
- On startup the publisher sends **one empty retained payload** to the old
  switch discovery topic (`<prefix>/switch/<device>/optimal_income/config`) so
  HA removes the stale switch entity; this is idempotent.
- `sensor.work_mode` keeps its key and diagnostic category, is renamed
  **"Energy storage mode"**, and reports `Self Use` when bit 0 is set. Any bit
  combination outside the observed values falls back to the existing flag-list
  rendering so nothing is silently mislabelled.

## `select` discovery component

`internal/homeassistant` gains `Component: Select` with an `Options []string`
field. The payload adds `options`, `state_topic: ~/state`,
`value_template: {{ value_json.<key> }}` and (with `Command: true`)
`command_topic: ~/<key>/set`. Command entities remain omitted when
`CONTROLS_ENABLED=false`. Entity counts after B2: 36 sensors, 41 entities
(the switch becomes a select, `boost_select` is added).

## Package layout

- `internal/schedule` (pure): `ParseToUWindow`, `Desired(tou, slots, now)`,
  `PlanBoost(mode, minutes, now, tou)` (snap + rejection rules),
  `Expired(slots, now)`, `BoostSelectState(slots)`.
- `internal/inverter`: `TimedSlotWriteRegisters(i, slot) []Register` — the
  ordered addr/value list for one slot, modelled on `RTCWriteRegisters`.
- `internal/controls`: command keys `boost_select` and `optimal_income` (select
  payloads); `Reconciler` (one-method interface, sibling of `RTCSyncer`) with
  the guarded per-register loop; nil-interface wiring when controls are off.
- `internal/scheduler`: calls the reconciler after each poll, under the same
  mutex as commands.
- `internal/homeassistant`: `Select` component, the two selects, renamed
  sensor, `State.BoostSelect *string`.
- `internal/config`: `TOU_WINDOW`.

## Testing

- **Unit** (`internal/schedule`): quarter-hour snap for all four `:NN` offsets ×
  4 durations; midnight and ToU-overlap rejections; `Desired` for crossing and
  non-crossing windows and for `TOU_WINDOW=`; expiry; select-state mapping
  including the `null` case. `TimedSlotWriteRegisters` order.
- **godog** (`features/schedule_controls.feature`): a boost writes exactly the
  four registers in order and each is confirmed by re-read; `Off` clears; an
  expired slot is cleared on poll; ToU drift is re-asserted and an equal ToU
  produces `no holding register is written`; a boost while Optimal Income is
  Stop is rejected with no write; `optimal_income` `Run`/`Stop` flips bit 1
  only; the retained state carries `boost_select`.
- **Live smoke (exit criterion):** one 15-min boost lands on the correct
  quarter-hour and the sensors follow; after the end the next poll clears slot
  3; the ToU window is restored after an app-side edit; steady-state polls issue
  no writes (sidecar log shows no `write_holding`).

## How a clear is written (confirmed)

Stage A confirmed non-zero H/M writes only, so the clear mechanic was probed
live before this spec was finalised (`docs/phase0/findings.md`, "Stage B2
pre-design probe"; `fixtures/write-probe-clear-slot3.json`): writing `0` to
`43163`–`43166` one register at a time left the slot all-zero at 0 s and 60 s,
and the restore read back correctly. **"Empty" is all four H/M registers = 0**,
the same encoding the B1 decoders already treat as unset. A clear therefore
writes zeros in the standard order (start hour, start minute, end hour, end
minute) through the guard, so an already-cleared slot costs no fc06.

## Deferred (probe-first Stage C, not in B2)

A writable energy-storage-mode dropdown (Self Use / Feed In Priority / Backup /
Off Grid). Only Self Use has been observed; the register values of the other
modes are unconfirmed on this unit and writing unprobed `43110` values is
forbidden by the guardrails. Requires a read-back-verified write probe of each
mode value first.
