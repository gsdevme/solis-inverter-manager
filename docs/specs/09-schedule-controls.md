# 09 — Schedule controls (Tariff & Boost, Stage B2)

Stage B2 (#28) adds the **write side** of the timed schedule that B1 (REQ-HA-14)
made readable: a `select.boost_select` that programs slot 3, manager-owned
assertion of the time-of-use (ToU) window in slots 1–2, and a per-poll reconcile
that clears an expired boost. It also reshapes the work-mode controls to match
the Solis app's own vocabulary. This spec is the source of truth for that
behaviour; `REQ-HA-15` to `REQ-HA-17` and `REQ-CF-07` in `REQUIREMENTS.md` carry
the stable IDs.

Register ground truth is `docs/phase0/findings.md` (Stage A section) and
`02-register-map.md`. The READ-BEFORE-WRITE guard in `03-mqtt-ha-discovery.md`
(REQ-HA-10) governs **every** write described here.

## Vocabulary (matches the Solis app)

| App setting | Register | Values | HA entity after B2 |
|---|---|---|---|
| Energy storage mode | `43110` bit 0 (+ unconfirmed bits for the other modes) | Self Use · Feed In Priority · Backup · Off Grid | `sensor.work_mode` "Energy storage mode", **read-only**, shows `Self Use` |
| Optimal Income | `43110` bit 1 | Run · Stop | `select.optimal_income` **Run / Stop** (replaces the on/off switch) |
| Timed charge/discharge slots 1–3 | `43141`–`43170` | H/M windows | `select.boost_select`, `tou_window`, `boost`, `boost_ends_at` |

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
(`select.boost_select`, `boost`, `boost_ends_at`, `tou_window`) is **derived
from the registers**, so a restart mid-boost simply adopts the running boost, and
a missed poll cannot leave a stale window to re-fire tomorrow: the next poll,
whenever it is, fixes it.

**Contract:** slots 1–3 are manager-owned. Edits made in the Solis app are
reverted within one poll interval, with one narrow exception: a slot-3 window
that reads as a **live boost** — started, not yet ended, and not ending at
midnight — is adopted rather than reverted. Any other set slot-3 window is
cleared.

## Configuration (`REQ-CF-07`)

| Env | Default | Meaning |
|---|---|---|
| `TOU_WINDOW` | `23:30-05:30` | `HH:MM-HH:MM`, local time. Crossing midnight is allowed and is the normal case. Empty string disables ToU assertion (slots 1–2 are left untouched). Invalid values fail config validation (`errors.Join`). |
| `CONTROLS_ENABLED` | `true` (existing) | Kill switch, Ruling R1: when `false`, no `select` entities are discovered, no command subscription, **no reconcile** — B2 is fully read-only. When `true` the command handler is built whether or not an MQTT broker is configured, so the reconcile (and the RTC auto-sync) also run in the no-broker mock path; only the command subscription needs a broker. A non-empty `TOU_WINDOW` with controls disabled logs a startup warning. |

Times are the deployment's local zone (`TZ`), the same clock the RTC code and
`boost_ends_at` already use.

## Desired schedule

`TOU_WINDOW=S-E` maps to:

- `S > E` (crosses midnight): slot 1 charge = `S→00:00`, slot 2 charge = `00:00→E`;
- `S < E`: slot 1 charge = `S→E`, slot 2 charge empty;
- slot 1 and slot 2 **discharge** windows are asserted empty.

Slot 3 desired is computed **per direction**, not per boost: each of the slot's
two windows is kept while it is **live** and cleared otherwise. A window is live
when it is set, `start ≤ now < end` in minute-of-day terms, and `end ≠ 00:00`.
"Empty" is all four H/M registers of a window = 0 (see *Open item* on how a clear
is written).

Clearing everything else is safe because the manager only ever writes one shape
into slot 3: `PlanBoost` starts a boost at the current minute and ends it on a
quarter-hour strictly before midnight (it refuses anything that would reach
`00:00`). So a slot-3 window that has not started, that ends at `00:00`, or whose
window sits wholly in the past is a **remnant** — a command whose end registers
never landed, or a boost programmed on an earlier day — and never a boost worth
keeping. Judging by `end` alone re-adopted both cases indefinitely: an `end` of
`00:00` resolves to a midnight that is always still ahead, and yesterday's
`23:40→23:45` reads as tonight's.

In the normal case only one direction is ever set and the rule reads as "the
boost while it runs"; the per-direction form matters when a partly written slot
holds a remnant of the window a command was clearing beside the one it
programmed — see *Partial writes* below.

**Known limitation.** While both directions of slot 3 are set, `BoostOf`
(`internal/schedule`) prefers the charge window, so the HA-facing `boost` sensor,
`boost_ends_at` and `boost_select` describe the charge window even when the
discharge window is the one actually running as the boost. That state is now
confined to the inside of a single command: a direction swap clears the old
window first and, when that clear fails, the command **aborts** (see *Partial
writes*) instead of arming the new direction beside the old one, so no poll finds
the slot holding both. The per-direction reconcile above is unaffected either
way: it judges each window on its own liveness, keeping the one that is running
and clearing the other in the same pass.

Charge/discharge current is **global** (`43141`/`43142`, the existing `number`
entities) and is not part of the schedule.

## `select.boost_select` (`REQ-HA-16`)

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
4. Otherwise **all eight** H/M registers of slot 3 go through the guard, one
   register at a time. The **unused direction is cleared first**: its four
   registers are written to zero before the four registers of the direction
   carrying the window. Within a direction the order is always start hour,
   start minute, end hour, end minute. Then the state document is refreshed
   (existing command path).

**Selecting `Off`** clears slot 3 immediately (same eight registers, same order).

**Expiry:** any poll at which a slot-3 window is not live — `now` outside
`[start, end)`, or `end = 00:00` — clears that window: that direction's four
registers only, never the other direction's. The inverter stops the window at
`end` by itself; clearing only prevents the window firing again tomorrow, so
lagging by up to one poll interval is acceptable and documented. The liveness
test is in minute-of-day terms and never resolves `end = 00:00` to the next
midnight — that resolution survives only in `boost_ends_at` (`REQ-HA-14`), which
still renders an end-of-day window as tomorrow's midnight.

**Why that write order.** Writes take effect immediately and there is no
multi-register write in the stack (sidecar is fc06-only), so a window is set
one word at a time. Writing start before end makes the one-second transient
either a same-direction superset of the target (on set: `14:07→00:00` before
the end lands) or an already-past window (on clear: `00:00→14:30` after 14:30).
Neither transient can charge or discharge in the wrong direction.

The **block order follows the desired slot**, for the boost writes and the
reconcile alike (`inverter.TimedSlotWriteRegisters`): **the unused direction is
always cleared first**. A direction whose desired window is unset is written
before a direction whose window is set; when both are unset (a clear) or both
are set, the charge block comes first. So a boost replacing one in the opposite
direction zeroes the old window before programming the new one, and slot 3 never
transits the both-windows-set state — a state the inverter has never been probed
in. Only registers whose value differs are written, so the zeros of a direction
that is already empty cost no fc06.

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
3. Log one line per reconcile that wrote anything (`schedule reconciled`); log
   nothing when nothing changed. The guard logs each register it skips or writes.
4. A reconcile error is non-fatal (`schedule reconcile failed` at warn, retry
   next poll), exactly like RTC auto-sync.

The reconcile never touches `43110`, `43141`, `43142` or the leading pairs.

`Reconcile` pre-filters the registers whose desired value differs before handing
them to the guard, so a schedule already in the desired shape issues **no Modbus
traffic at all** — not even the guard's own read.

The reconcile hangs off the poll **after** the state publish, the same place as
the RTC auto-sync: a poll that fails to publish (broker down) returns early and
reconciles nothing that cycle. The schedule is re-asserted on the first poll that
publishes again, and because the desired schedule is derived afresh from the
registers each time, a skipped cycle leaves nothing stale behind.

**Partial writes.** A slot is written one register at a time by any of the three
write paths — the boost command, the reconcile, and the RTC sync — all of which
share `writeRegisters` (`internal/controls`), so a transport failure can stop any
of them mid-sequence, not only "the same command". A register whose guard failed
without a confirmed write is retried **once, in place**, before the next register
is attempted: one retry only, no sleep, still under the same API mutex the caller
already holds. Such a failure does not prove the fc06 never reached the
inverter — the write can land and only the reply be lost — but the retry is safe
regardless: it goes through the full read-before-write guard again, which
re-reads first and skips a register that already holds the desired value, so a
retry after a landed write costs no extra flash wear.

A register that fails its retry **aborts the sequence**: the error is reported
and no later register is attempted. So does a write the re-read did not confirm,
which is not retried at all (the inverter took it). Aborting leaves the slot in
the last shape the manager fully wrote — an intact old window, which expires by
itself, or a completed clear, which stays clear — while carrying on past the
failure can compose a window out of two commands' registers, such as a new
direction armed beside an old one the clear never removed. Little is lost by
stopping: the reconcile is handed only the registers that differ, so the sequence
the next poll retries spends reads, not writes.

Whatever an aborted sequence left behind is healed by the next poll's reconcile,
which judges each direction by liveness — `start ≤ now < end` with `end ≠ 00:00`
(see *Desired schedule*). Healing is in slot order: the reconcile writes slots
1–3 as one sequence, so a register in the tariff slots that fails on every poll
blocks the slot-3 clear behind it until that register recovers. A remnant that
still reads as running is kept until its own `end`: a half-written clear leaving,
say, `00:00→14:00` behind is adopted as the boost until 14:00 and cleared on the
first poll after that, rather than lingering. A remnant whose end never landed — `14:07→00:00`, a command stopped
partway through programming an empty slot — is cleared by the liveness rule on
the next poll instead of reading as a window running to midnight. Together the
in-place retry, the abort and the reconcile are why a one-off sidecar timeout
leaves a boost either intact or gone, never stuck on a window the manager never
meant to hold.

**A poll whose setpoints read failed also skips the reconcile**, like a publish
failure. The state reader reuses the last-known setpoints when the setpoints
sub-read fails (so the published control state is never blanked), which means the
slots handed to the reconcile can be a whole poll interval old. Diffing against
them would let the manager act on a slot it never read — clearing a window the
owner set from the Solis app since the last good read, say. The reconcile
therefore runs only on cycles whose setpoints came from the inverter; a skipped
cycle is logged at debug and the next successful read re-asserts the schedule.

## Work-mode controls reshaped (`REQ-HA-15`)

- `select.optimal_income` with options `Run` / `Stop` **replaces** the
  `switch.optimal_income`. Same key, same guarded read‑modify‑write of **bit 1
  only** (35 ↔ 33, REQ-HA-09), state derived from `43110` (`Run` when bit 1 set).
  Commands other than the two option strings are logged and dropped (REQ-HA-12).
- `PublishDiscoveryRemovals` sends **one empty retained payload** to the old
  switch discovery topic (`<prefix>/switch/<serial>_optimal_income/config`) so
  HA removes the stale switch entity. It runs after every `PublishDiscovery` —
  startup and each reconnect — is ungated by `CONTROLS_ENABLED`, and is
  idempotent.
- `sensor.work_mode` keeps its key and diagnostic category, is renamed
  **"Energy storage mode"**, and reports `Self Use` when bit 0 is set. Any bit
  combination outside the observed values falls back to the existing flag-list
  rendering so nothing is silently mislabelled.

## `select` discovery component

`internal/homeassistant` gains `Component: Select` with an `Options []string`
field. The payload adds `options`, `state_topic: ~/state`,
`value_template: {{ value_json.<key> }}` and (with `Command: true`)
`command_topic: ~/<key>/set`. Command entities remain omitted when
`CONTROLS_ENABLED=false`. Entity counts: 42 read-only entities and five
command entities — 47 in all, the catalogue ending `…, optimal_income` (select),
`boost_select` (select), `rtc_sync`.

## Package layout

- `internal/schedule` (pure): `ParseToUWindow(s) (window, enabled, err)`,
  `ToUSlots(tou) [2]TimedSlot` (the midnight split), `Desired(tou, assertToU,
  slots, now)` (slot-3 liveness per direction), `BoostOptions()`,
  `ParseBoostOption(s)`,
  `PlanBoost(mode, minutes, now, tou, assertToU)` (snap + rejection rules,
  returning `ErrCrossesMidnight` / `ErrOverlapsToU`), `BoostSelectState(slots)`.
- `internal/inverter`: `Register{Addr, Value}` (`RTCRegister` is an alias of it),
  `TimedSlotWriteRegisters(i, slot) []Register` — the eight ordered addr/value
  pairs for one slot's offsets `+2..+9`, modelled on `RTCWriteRegisters` — and
  `ParseClock`.
- `internal/controls`: `WithToU(window, assert)` on the handler; command keys
  `boost_select` (`setBoost`) and `optimal_income` (`parseRunStop`);
  `writeRegisters`, the one guarded per-register loop shared by the RTC sync,
  the boost writes and the reconcile, retrying once each register the transport
  lost; `(*Handler).Reconcile(ctx, slots)` in
  `reconcile.go`, logging under the key `reconcile`.
- `internal/scheduler`: defines the `Reconciler` interface it consumes (a
  sibling of `RTCSyncer`, satisfied by `*controls.Handler`), takes it as the
  sixth argument of `New`, and calls `maybeReconcile` right after
  `maybeSyncRTC`, under the same mutex as commands. A nil interface — controls
  off — makes it a no-op.
- `internal/homeassistant`: `Select` component with `Options`, the two selects,
  `DiscoveryTopic(component, key)` and `BuildDiscoveryRemovals()`, the renamed
  `work_mode` sensor, `State.BoostSelect *string` (`boost_select`).
- `internal/publisher`: `PublishDiscoveryRemovals(ctx)`.
- `internal/config`: `TOU_WINDOW`, read with `os.LookupEnv` so an explicitly
  empty value disables ToU assertion while an unset variable takes the default.

## Testing

- **Unit** (`internal/schedule`): quarter-hour snap for all four `:NN` offsets ×
  4 durations; midnight and ToU-overlap rejections; `Desired` for crossing and
  non-crossing windows and for `TOU_WINDOW=`; per-direction expiry (a stale
  window beside a running one, both stale, both running); slot-3 liveness (a
  window kept in its first minute, and cleared when it has not started, when it
  ends at `00:00` — both a tariff-shaped and a half-programmed one — and when it
  is a previous day's boost read after midnight); select-state mapping including
  the `null` case. `TimedSlotWriteRegisters` order.
- **Unit** (`internal/controls`): a register lost to a one-off transport failure
  is retried in place and the sequence runs on to its end; a register that fails
  its retry aborts the sequence — a failed clear of the old direction writes no
  register of the new one and leaves the old window intact — and a write the
  re-read did not confirm aborts without being retried.
- **godog** (`features/schedule_controls.feature`): a boost writes exactly the
  four registers in order and each is confirmed by re-read; `Off` clears; an
  expired slot is cleared on poll; a slot-3 window ending at `00:00` and
  yesterday's boost read after midnight are both cleared; ToU drift is
  re-asserted and an equal ToU produces `no holding register is written`; a boost
  while Optimal Income is Stop is rejected with no write; a partially cleared
  slot is healed with one write rather than wiped; `optimal_income` `Run`/`Stop`
  flips bit 1 only; the retained state carries `boost_select`.
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
