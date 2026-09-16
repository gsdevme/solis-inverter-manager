# Phase 0 — Investigation & register confirmation (findings)

Load-bearing de-risking spike. The register map below is **confirmed against the
live inverter** on 2026-09-08, cross-referenced with the authoritative online
sources (see `docs/specs/02-register-map.md` for the annotated source list).
Raw live captures are in `docs/phase0/fixtures/` and become decode unit-test
fixtures in Phase 3.

- **Hardware:** Solis RHI-3.6K-48ES-5G hybrid inverter via Solarman V5 datalogger
  (gen2 WiFi stick), TCP port 8899, `mb_slave_id=1`.
- **Transport:** `pysolarmanv5==3.0.6` (sync `PySolarmanV5`), single persistent
  socket, explicit `disconnect()`, reconnect-on-error between calls.
- **Probe method:** bulk `read_input_registers` / `read_holding_registers` over
  the candidate blocks; values decoded and cross-checked for physical
  consistency (e.g. battery power = current × voltage). One minimal, restored
  write test on `43141`.

Only **confirmed facts** survive here. The probe harness itself was throwaway.

## Decode rules (confirmed)

- **Register width:** each Modbus register is a 16-bit unsigned word unless noted.
- **32-bit values (U32/S32):** two consecutive registers, **MSW at the lower
  address** (big-endian words): `value = (reg[addr] << 16) | reg[addr+1]`.
  - Documentary consensus is unanimous (Ginlong list order, Coghlan, SolarMark
    "U32 BE", the solarman parser). **Not** empirically forced on this unit: every
    32-bit total currently reads < 65536 (max ~21717 kWh), so the high word is
    always 0 today. Decode as MSW-first; revisit only if a total ever exceeds
    65535 and reads absurd.
- **Signed values (S16/S32):** two's complement. Grid power is signed and its sign
  encodes direction; battery power and battery current are read as magnitudes and
  signed from the `33135` direction flag (see below).

## Confirmed sign conventions

| Quantity | Register(s) | Sign | Confirmed by |
|---|---|---|---|
| Battery power | 33149·33150 (32-bit, W) | Register is a **magnitude**; published **+ = charge, − = discharge** from `33135` | live +802 W while `33135`=0 (charging); = 15.5 A × 51.8 V; live +381 W while `33135`=1 (discharging) — see the 2026-09-14 note |
| Battery current | 33134 (÷10 A) | Register is a **magnitude**; published **+ = charge, − = discharge** from `33135` | live +15.5 A while charging, +7.6 A while discharging — see the 2026-09-14 note |
| Grid power | 33130·33131 (S32, W) | **+ = export, − = import** | live −132 W (importing); cross-checks `33263` = −129 W |
| Battery direction flag | 33135 (U16) | **0 = charge, 1 = discharge** | live 0 while charging |

**2026-09-14 — battery sign correction (live).** Every Phase 0 fixture was
captured while charging (`33135`=0), so the "+ = charge, − = discharge" register
convention was never observed for a discharge. A live reading taken while the
house was running off the battery (house load 448 W, PV 189 W, grid 1 W,
`33135`=1) reported `33149·33150` = **+381 W** and `33134` = **+7.6 A** — both
positive. On this firmware the power and current registers are therefore
**magnitudes**, and only `33135` carries the direction (the legacy Python app
derived to/from-battery the same way). **Decision:** the decode reads both
registers as magnitudes and applies the sign from `33135`, so the published
`battery_power` / `battery_current` keep the documented + = charge, − = discharge
convention in both directions.

The old app's `grid_import=reg33130 / grid_export=reg33131` was a **bug**: those
two registers are the two halves of a single S32, not independent values. Its
`≤24000` clamp was a workaround for reading the S32 high word (0 or 0xFFFF) as a
standalone watt value.

## Confirmed input registers (fc04, read-only)

Live values from `fixtures/live-snapshot-comprehensive.json` (2026-09-08 ~15:40).

| Addr | Meaning | Type | Scale | Live | Decoded |
|---|---|---|---|---|---|
| 33022–33027 | RTC y/mo/d/h/mi/s | U16×6 | y+2000 | 26,9,8,15,40,31 | 2026-09-08 15:40:31 |
| 33035 | Generation today | U16 | ÷10 kWh | 101 | 10.1 kWh |
| 33036 | Generation yesterday | U16 | ÷10 kWh | 63 | 6.3 kWh |
| 33049 / 33050 | PV1 voltage / current | U16 | ÷10 V / ÷10 A | 2058 / 29 | 205.8 V / 2.9 A |
| 33051 / 33052 | PV2 voltage / current | U16 | ÷10 V / ÷10 A | 2010 / 30 | 201.0 V / 3.0 A |
| 33057·33058 | Total PV (DC) power | U32 | ×1 W | 0/1199 | 1199 W |
| 33079·33080 | Inverter AC active power | S32 | ×1 W | 0/260 | 260 W |
| 33093 | Inverter temperature | S16 | ÷10 °C | 289 | 28.9 °C |
| 33094 | Grid frequency | U16 | ÷100 Hz | 5014 | 50.14 Hz |
| 33095 | Inverter status | U16 | enum | 3 | (running) |
| 33121 | Operating status | U16 | bitfield | 1793 | — |
| 33130·33131 | Grid power (meter) | **S32** | ×1 W | 65535/65404 | −132 W (import) |
| 33132 | Work-mode read-back | U16 | bitfield | 35 | mirrors 43110 |
| 33133 | **Battery voltage** | U16 | ÷10 V | 518 | 51.8 V |
| 33134 | Battery current | **S16** | ÷10 A | 155 | +15.5 A (charge) |
| 33135 | Battery direction flag | U16 | 0=chg,1=dis | 0 | charging |
| 33139 | **Battery SOC** | U16 | ×1 % | 99 | 99 % |
| 33140 | **Battery SOH** | U16 | ×1 % | 97 | 97 % |
| 33141 | **BMS battery voltage** | U16 | ÷100 V | 5105 | 51.05 V |
| 33142 | BMS battery current | S16 | ÷10 A | 155 | 15.5 A (scale matches 33134) |
| 33143 | **BMS charge current limit** | U16 | ÷10 A | 150 | 15.0 A (0 at SOC 100 %; see §BMS current limits) |
| 33144 | **BMS discharge current limit** | U16 | ÷10 A | 1125 | 112.5 A |
| 33145 | BMS fault word 1 | U16 | bitfield | 0 | no fault (bit map vendor-documented, **unconfirmed live**) |
| 33146 | BMS fault word 2 | U16 | bitfield | 0 | no fault (bit map vendor-documented, **unconfirmed live**) |
| 33147 | House load power | U16 | ×1 W | 385 | 385 W |
| 33149·33150 | **Battery power** | **S32** | ×1 W | 0/802 | +802 W (charge) |
| 33161·33162 | Battery total charge energy | U32 | ×1 kWh | 0/10640 | 10640 kWh |
| 33163 | Battery charge today | U16 | ÷10 kWh | 89 | 8.9 kWh |
| 33165·33166 | Battery total discharge energy | U32 | ×1 kWh | 0/10439 | 10439 kWh |
| 33167 | Battery discharge today | U16 | ÷10 kWh | 25 | 2.5 kWh |
| 33169·33170 | Grid total import energy | U32 | ×1 kWh | 0/17482 | 17482 kWh |
| 33171 | Grid import today | U16 | ÷10 kWh | 208 | 20.8 kWh |
| 33173·33174 | Grid total export energy | U32 | ×1 kWh | 0/9377 | 9377 kWh |
| 33175 | Grid export today | U16 | ÷10 kWh | 33 | 3.3 kWh |
| 33213 | **Over-discharge SOC** | U16 | ×1 % | 20 | 20 % — mirrors holding `43011`; value from the full sweep, re-read in the 2026-09-15 probe |
| 33214 | **Force-charge SOC** | U16 | ×1 % | 19 | 19 % — mirrors holding `43018`; value from the full sweep, re-read in the 2026-09-15 probe |

Cross-checks that pin the map: battery power 802 W = 15.5 A × 51.8 V ✓; grid
S32 −132 W ≈ meter `33263` −129 W ✓; RTC = wall clock (± drift, below) ✓.

## Confirmed holding registers (fc03 read / fc06 write)

| Addr | Meaning | Type | Scale / values | Live | Notes |
|---|---|---|---|---|---|
| 43000–43005 | **RTC set** | U16×6 | y+2000 … | 26,9,8,15,40,31 | mirrors input 33022–33027; writing sets the clock |
| 43011 | Min SOC (overdischarge) | U16 | ×1 % | 20 | 20 % |
| 43018 | Force-charge SOC | U16 | ×1 % | 19 | 19 % — raw 19 in `live-snapshot-full-sweep.json`; confirmed by agreement with its input mirror `33214` = 19 (19 in `bms-limit-probe.json` too), the same way `43011` = 20 agrees with `33213` |
| 43110 | **Work mode (energy-storage switch)** | U16 | bitfield | 35 | fc06 write; reads back here AND at 33132 |
| 43114 | Charge/discharge enable | U16 | 0/1 | 1 | enabled |
| 43115 | Charge/discharge direction | U16 | 0=chg,1=dis | 0 | — |
| 43116 | Instant charge/discharge current | U16 | ÷10 A | 450 | 45.0 A |
| 43117 / 43118 | Max charge / discharge current | U16 | ÷10 A | 1000 / 1000 | 100.0 A (unit limit) |
| 43141 | **Timed charge current** | U16 | ÷10 A | 350 | 35.0 A |
| 43142 | **Timed discharge current** | U16 | ÷10 A | 600 | 60.0 A |
| 43143 / 43144 | Timed charge start H / M | U16 | 0–23 / 0–59 | 23 / 31 | 23:31 |
| 43145 / 43146 | Timed charge end H / M | U16 | 0–23 / 0–59 | 0 / 0 | — |
| 43147 / 43148 | Timed discharge start H / M | U16 | 0–23 / 0–59 | 0 / 0 | — |
| 43149 / 43150 | Timed discharge end H / M | U16 | 0–23 / 0–59 | 0 / 0 | — |
| 43151 / 43152 | Slot 2 leading pair — **unconfirmed** (not per-slot current: the app exposes one global charge/discharge current, 43141/43142) | U16 | — | 0 / 0 | slot 2 = 43151–43160: H/M fields at slot-1 offsets +2..+9, stride 10 (Stage A) |
| 43153 / 43154 | Slot 2 charge start H / M | U16 | 0–23 / 0–59 | 0 / 0 | — |
| 43155 / 43156 | Slot 2 charge end H / M | U16 | 0–23 / 0–59 | 5 / 29 | 05:29, owner-set in the Solis app |
| 43157 / 43158 | Slot 2 discharge start H / M | U16 | 0–23 / 0–59 | 0 / 0 | — |
| 43159 / 43160 | Slot 2 discharge end H / M | U16 | 0–23 / 0–59 | 0 / 0 | — |
| 43161 / 43162 | Slot 3 leading pair — **unconfirmed** (as 43151/43152) | U16 | — | 0 / 0 | slot 3 = 43161–43170, H/M at +2..+9 (Stage A) |
| 43163 / 43164 | Slot 3 charge start H / M | U16 | 0–23 / 0–59 | 14 / 2 | 14:02, owner-set in the Solis app |
| 43165 / 43166 | Slot 3 charge end H / M | U16 | 0–23 / 0–59 | 14 / 56 | 14:56 |
| 43167 / 43168 | Slot 3 discharge start H / M | U16 | 0–23 / 0–59 | 0 / 0 | — |
| 43169 / 43170 | Slot 3 discharge end H / M | U16 | 0–23 / 0–59 | 0 / 0 | — |

## The 43110 work-mode bitfield (confirmed)

Reads back at **both** `43110` (holding) and `33132` (input); live value **35**.

| Bit | Value | Meaning |
|---|---|---|
| 0 | 1 | Self-use |
| 1 | 2 | Timed charge/discharge ("optimised revenue" — the old app's "optimal income") |
| 5 | 32 | Allow grid charge (**bit5=1 ⇒ allow**; the 2020 PDF's "1=not allow" note is stale) |

Named values used by the app:
- **35** = bit0+bit1+bit5 = self-use + grid-charge + **timed on** ("optimal income ON")
- **33** = bit0+bit5 = self-use + grid-charge + **timed off** ("optimal income OFF")

Toggling the "optimal income" switch flips **bit 1** only (35 ↔ 33).

## Write-path findings (confirmed live)

From `fixtures/write-path-probe.json` (register 43141):
- fc06 single write is **accepted** (no-op write 350→350 acknowledged).
- Write 350→340 took effect **immediately** on read-back.
- **No revert after 130 s** — the value persisted (340) with no further action.
  → The hypothesised ~120 s commit/revert trigger **does not apply** to `43141`.
  Setpoints persist without a commit. (Undocumented in all sources; this is the
  first empirical answer. Re-verify per-register if new setpoints misbehave.)
- Original value **restored** (340→350, confirmed).

Amps write encoding: `int(round(amps, 1) * 10)`, fc06. The registers accept up to
100.0 A (`43117/43118` = 1000); the HA control range is clamped to **0–62.5 A**, the
datasheet battery charge/discharge rating for this model.

### Datasheet battery ratings

Solis datasheet *RHI-(3-6)K-48ES-5G* (AUS V1.8, 2021-10,
<https://solisinverters.au/uploads/files/202406/Solis_datasheet_RHI-(3-6)K-48ES-5G_AUS_V1.8_2021_10.pdf>):

| Model | Max battery charge/discharge current | Max battery charge/discharge power |
| --- | --- | --- |
| RHI-3K-48ES-5G | 62.5 A | 3 kW |
| **RHI-3.6K-48ES-5G** (this unit) | **62.5 A** | **3 kW** |
| RHI-4.6K-48ES-5G | 100 A | 5 kW |
| RHI-5K-48ES-5G | 100 A | 5 kW |
| RHI-6K-48ES-5G | 100 A | 5 kW |

The 100 A the registers accept is the 4.6K+ figure — the register map is shared
across the family — so the manager clamps to this model's 62.5 A. Note the 3 kW
power limit usually binds first: at the ~51.8 V pack voltage seen live that is
≈ 58 A, so 62.5 A is only reachable near the 48 V end of the range.

## RTC drift (confirmed)

- Inverter RTC ran **+204.9 s (~3.4 min) ahead** of real time on 2026-09-08.
- **Settable:** holding `43000–43005` mirrors input `33022–33027` exactly, so
  writing that block corrects the clock.
- **No timezone register.** The RTC is a *naive local datetime* (Y/M/D/H/M/S, no
  offset/zone field); no authoritative map documents a TZ register. The RTC is a
  bare crystal that normally only gets corrected by the Solarman cloud/app —
  talking directly to the datalogger, it never self-corrects, which explains the
  minutes-scale drift.
- → Spec an **RTC-drift sensor** and an optional **periodic RTC auto-sync**
  (re-write `43000–43005` from a reliable clock when drift exceeds a threshold),
  exposed as an HA control, in the writable-controls phase.

## Ambiguities — resolution status

| # | Question | Resolution |
|---|---|---|
| 1 | 32-bit word order | MSW-first (documentary; not empirically forced — all totals < 65536) |
| 2 | Battery-power sign | Register is a magnitude; sign derived from flag 33135 (2026-09-14: live +381 W with 33135=1 while discharging, after +802 W with 33135=0 while charging) |
| 3 | Grid power: two U16s vs one S32 | **One S32** (33130·33131 = −132 W; old app bug) |
| 4 | BMS current scale (33142) | ÷10 A (matches 33134 live) |
| 5 | 43110 bit5 grid-charge polarity | bit5=1 = allow (empirical 33/35) |
| 6 | Work-mode read-back location | Both 43110 and 33132 (both = 35 live) |
| 7 | ~120 s setpoint commit/revert | **No revert** for 43141 (persisted 130 s) |
| 8 | SOH validity (33140) | Reports a real value (97 %) on this unit |
| 9 | Amps write limit | Registers allow 100 A (the 4.6K+ rating); HA control clamped 0–62.5 A — the RHI-3.6K datasheet rating (62.5 A / 3 kW) |
| 10 | Max single-read width | Datalogger NAKs `illegal_address` above ~100 regs (probed: `ReadInput(33022,125)` NAK; `33022+100` OK, `33022+110` NAK). Manager reads the telemetry bank as two blocks of ≤100 (split `33121\|33122`); stricter than the sidecar's 125 wire cap. Both production blocks re-read live 2026-09-15 (`33022+100` and `33122+93`, two identical passes, `fixtures/live-poll-blocks-2026-09-15.json`). The blocks must be issued **sequentially**: run concurrently on the one datalogger socket, `33022+100` timed out with no reply while `33122+93` succeeded; every sequential retry succeeded. The manager already serialises polls, so this constrains ad-hoc probing only |
| 11 | Timed H/M register writes (43143–43150, slots 2/3) | fc06 accepted, persist ≥130 s, restore confirmed (Stage A probe on 43144, 43164) |
| 12 | 43024 writability | fc06 **acked but ignored** — read-back unchanged at 0/60/130 s; treat as read-only |
| 13 | Clearing a timed slot by zeroing its H/M pair | **Confirmed** — slot-3 charge `43163`–`43166` written 0/0/0/0, read back all-zero at 0 s and 60 s, restored to 14/2/14/56 with read-back (Stage B2 pre-design probe, `fixtures/write-probe-clear-slot3.json`) |
| 14 | BMS limit pair: `33143`/`33144` vs `33206`/`33207` | **`33143`/`33144`** — confirmed against the Solis app (see §BMS current limits), which is what rules `33206`/`33207` out — not their stability. They were unchanged (0/650) across the two captures that settled the ruling, but `33206` has since been seen at 450 (see the open observation closing that section) |
| 15 | BMS fault-word bit assignments (`33145`/`33146`) | **Unconfirmed live** — both words read 0 in every capture, so only the addressing and the "no fault" decode are proven. The bit map (fault-1 bits 1–7 = OV / UV / OT / UT / charge-OT / charge-UT / discharge-OC; fault-2 bits 0 / 3 / 4 = charge-OC / BMS-internal / module-unbalanced) comes from the vendor hybrid protocol document. The raw words are published alongside the per-bit sensors so a real fault can be reconciled against that table |

## Stage A (#27) — timed-slot layout & SOC probe

Live fc03 sweep of holding 43000–43195 on 2026-09-13 via the shipped sidecar in
`MODE=live` (`fixtures/live-snapshot-holding-stage-a.json`), cross-checked against
the owner's Solis-app Time-of-Use screen.

- **Timed slots 2 and 3 exist at stride 10, H/M windows at slot-1 offsets:** slot 2 =
  43151–43160, slot 3 = 43161–43170. The leading pair of each slot (43151/52,
  43161/62; 0 live) is **not** confirmed as per-slot current — the owner reports the
  Solis app exposes a single global charge/discharge current (43141/43142) for all
  slots. The only non-zero values in 43151–43195 fall
  exactly on the predicted H/M positions and are the windows the owner set in the
  app — slot 2 charge end 05:29 (`43155/56`), slot 3 charge 14:02–14:56
  (`43163–66`); slot 3 was zero in the Phase 0 sweep. 43171–43195 read all-zero.
- **43090–43122 is *not* a schedule table** (the Stage B premise is ruled out).
  The values are protection/config thresholds: `43112`=2300 → 230.0 V,
  `43113`=5000 → 50.00 Hz, `43098/43100/43102/43104` = 52.0/52.5/47.5/47.0 V and
  `43119–43122` = 42.0/53.5/55.0/60.0 V (48 V battery limits). Exact meanings are
  *not* decoded — only the "not a schedule" conclusion is confirmed.
- **43024 = 45, 43025 = 50** — shaped like a force-charge / backup SOC pair.
  Meaning and scale are **unconfirmed** pending an LCD/app cross-check.
- **Timed H/M registers are fc06-writable and persist**
  (`fixtures/write-probe-stage-a.json`): `43144` 31→32 and slot-3 `43164` 2→3
  read back changed at 0/60/130 s (no ~120 s revert, as for 43141), then were
  restored and re-read. Slot 2/3 registers behave exactly like slot 1.
- **43024 is not writable via fc06:** the write 45→46 was acked (`ok:true`) but
  every read-back (0/60/130 s) still returned 45 — the inverter silently ignores
  it. Treat it as read-only; its meaning stays unconfirmed. Lesson for Stage B:
  an fc06 ack proves nothing — only read-back confirmation does.
- Drift vs Phase 0: RTC in sync (`43000–43005` = 2026-09-13 14:59:03), energy
  counters advanced, nothing else changed except the slot-3 window.

## Additional registers captured (full sweep)

The entire range **input 33000–33304** and **holding 43000–43195** is *addressable*
and reads cleanly when swept in narrow reads — no address is invalid. This is about
address validity, not read *width*: a single read wider than ~100 registers NAKs
`illegal_address` regardless (see ambiguity #10). Full raw dump:
`fixtures/live-snapshot-full-sweep.json`.
Notable extras beyond the confirmed map above (decode as future features permit):

- **33000–33003** — product/model/firmware codes (`245,176,65,1`).
- **33004–33011** — **inverter serial** as ASCII (16 chars). Useful for the HA
  device `identifiers`/model block. **Redacted to 0 in the committed fixture.**
- **33161–33176** — battery & grid lifetime + today energy counters (confirmed
  above).
- **holding 43034–43067** — mirror copies of several input statistics (e.g.
  `43057`=21717 total generation mirrors input `33030`).
- **holding 43090–43122** — protection/config thresholds (voltage- and
  frequency-shaped; **not** a schedule table, see §Stage A) + instant-current
  config (`43114`–`43118`) and min/backup SOC (`43011`, `43024`).
- Various limit/config registers in `33181–33217` and `43012–43049` (except the
  confirmed force-charge SOC setting `43018`, tabled above) (charge
  voltage/current limits, SOC targets) — captured for later.

## Fixtures

- `fixtures/live-snapshot-known-blocks.json` — clean consistent snapshot of the
  old-app blocks (system/DC/meter/holding).
- `fixtures/live-snapshot-comprehensive.json` — broad capture incl. AC power,
  temp, freq, status, loads, energy totals, control block.
- `fixtures/live-snapshot-full-sweep.json` — full 33000–33304 / 43000–43195 dump
  (inverter serial redacted).
- `fixtures/write-path-probe.json` — the restored write test on 43141.
- `fixtures/live-snapshot-holding-stage-a.json` — Stage A (#27) holding
  43000–43195 sweep confirming timed slots 2/3.
- `fixtures/bms-limit-probe.json` — 2026-09-15 read-only probe confirming the BMS
  current limits (input 33139+8, 33194+24; holding 43110+61). The 33194+24 block
  also carries the SOC-threshold mirrors 33213/33214, and the 33139+8 block the
  two BMS fault words 33145/33146 (both 0).
- `fixtures/write-probe-stage-a.json` — Stage A write→read-back→restore probe on
  43144 and 43164 (held) and 43024 (acked but ignored).
- `fixtures/write-probe-clear-slot3.json` — Stage B2 pre-design probe: zeroing
  slot-3 charge H/M (43163–43166) clears the window; held at 0/60 s; restored.
- `fixtures/live-poll-blocks-2026-09-15.json` — the two **production poll blocks**
  read exactly as the manager reads them (input `33022+100` and `33122+93`), from
  the second of two sequential passes 3 s apart that returned byte-identical
  words. Live proof that the widened 93-register block is accepted on the wire.

These are the ground truth for the Go decode unit tests (Phase 3).

## Stage B2 pre-design probe — clearing a slot (confirmed)

Approach A (`docs/specs/09-schedule-controls.md`) ends a boost by writing the
slot back to "empty". Stage A had only confirmed non-zero H/M writes, so before
the design was finalised the owner ran a read-back-verified probe on the app-set
slot-3 charge window (`43163`–`43166` = 14/2/14/56, `43110` = 35):

- each of the four registers written to `0` via fc06, one at a time
  (start hour, start minute, end hour, end minute);
- the whole `43161`–`43170` block read back **all-zero at 0 s and at 60 s**;
- the four originals restored and confirmed by read-back (`14/2/14/56`);
- `43161`/`43162` (unconfirmed leading pair) untouched throughout.

Conclusion: an all-zero H/M window is a valid, persistent "unset" state — the
same encoding the B1 decoders (`inverter.TimedWindow.IsZero`) already treat as
empty — so B2 clears a boost by zeroing offsets `+2..+5` (or `+6..+9`) of slot 3.
The first read of the session returned one sidecar `503` after ~18 min idle
(datalogger dropped the session; the sidecar reset it and the retry succeeded) —
the same laggy-link behaviour the manager's read-with-retry covers.

## BMS current limits — `33143`/`33144` (confirmed)

The BMS advertises how much current the pack will currently accept and deliver.
These are ceilings, not readings: they are what limits a charge, so they explain
a boost that under-delivers.

Two candidate pairs appeared in the Phase 0 sweep. A read-only probe on
2026-09-15 (`fixtures/bms-limit-probe.json`) settled it:

| Addr | 2026-09-08 (SOC 99 %, charging 15.5 A) | 2026-09-15 am (SOC 100 %, idle) | 2026-09-15 15:51 (SOC 95 %, discharging) |
|---|---|---|---|
| `33143` | 150 (15.0 A) | **0** (0.0 A) | 450 (45.0 A) |
| `33144` | 1125 (112.5 A) | 1125 (112.5 A) | 1125 (112.5 A) |
| `33206` | 0 | 0 | 450 |
| `33207` | 650 | 650 | 650 |

**Ground truth:** with the 2026-09-15 capture open, the Solis app reported
**0 A charge / 112.5 A discharge** for the BMS — matching `33143`=0 and
`33144`=1125 exactly, which confirms both the addressing and the ÷10 A scale
against the vendor's own UI.

Supporting evidence:

- `33143` **varies with pack state** (15.0 A at SOC 99 %, 0.0 A at SOC 100 %) —
  the charge taper a BMS applies as the pack fills. It is not a constant.
- It is **not** a mirror of the timed charge setpoint: `43141` read 350 (35.0 A)
  in the 2026-09-08 fixtures while `33143` read 150.
- `33144` reads 112.5 A while the battery is **idle**, so it cannot be an
  instantaneous current — only a limit.
- `33206`/`33207` were byte-identical across the two captures a week and one SOC
  point apart that settled the ruling, while `33143` moved with the pack — so
  neither is the BMS charge limit the app reports (but see the observation below).

**Open observation (`fixtures/live-poll-blocks-2026-09-15.json`, 15:51 UTC).**
`33206` read **450** — equal to `33143` in the same capture — having read 0 in
both earlier captures while `33143` read 150 and 0. The ruling above stands, since
the app cross-check pins the BMS charge limit to `33143`; but `33206` is evidently
not a constant either. It may track the *configured* max charge current rather than
the BMS-advertised one. **Unresolved** — do not decode it until a capture separates
the two.

The pair sits inside the telemetry block the manager already reads
(`33122`–`33214`), so decoding it costs no extra Modbus traffic.

Also re-confirmed in the same probe: holding `43117`/`43118` = 1000/1000
(100.0 A), the inverter's own configured charge/discharge ceiling — distinct
from both the BMS limits above and the timed-slot setpoints `43141`/`43142`
(150/250 = 15.0/25.0 A at probe time).

## SOC threshold mirrors — `33213`/`33214` (confirmed)

The inverter exposes two of its own SOC settings read-only on the input bank:
the SOC it stops discharging at (over-discharge) and the SOC at which it
force-charges from the grid.

| Addr | Full sweep (2026-09-08) | Probe (2026-09-15) | Holding mirror |
|---|---|---|---|
| `33213` | 20 | 20 | `43011` = 20 |
| `33214` | 19 | 19 | `43018` = 19 |

**Ground truth:** agreement with the holding registers they mirror, captured in
the same sweep — `33213` = `43011` and `33214` = `43018`, both re-read unchanged
in a probe taken a week later. They are plain percentages (×1), not scaled.

Reading them costs no extra Modbus traffic: the poller's second telemetry block
widens from `33122+54` to `33122+93` (33122–33214), still one read and still
inside the datalogger's ~100-register single-read cap (ambiguity #10).

The widened block is **live-confirmed**, no longer inferred from the cap: on
2026-09-15 both production blocks (`33022+100` and `33122+93`) were read
back-to-back in two sequential passes 3 s apart, each returning byte-identical
words, with `33213`/`33214` again 20/19
(`fixtures/live-poll-blocks-2026-09-15.json`).

## Inverter status enum — `33095` label table

`33095` is an enum, published as a raw number since Phase 3. Its display text
comes from Appendix 2 of the vendor hybrid protocol document, "all 4G" column;
the manager carries that table in `internal/inverter/status.go` and publishes
`status_text` alongside the raw `status`.

This unit reads **3 → "Generating"** in normal operation. The codes that matter
operationally are the `0x20xx` communications faults, where a battery problem
surfaces: `0x2012` CAN_Comm_FAIL (battery COM fail), `0x2015` Alarm-BMS and
`0x2017` Alarm2-BMS. An unlisted code renders as its hex form (`0xBEEF`) rather
than as a guess.

## BMS fault words — `33145`/`33146` (addressing confirmed, bits not)

These two bitfields are the only register evidence that distinguishes a finished
charge — where the BMS simply drops its charge current limit to 0 A — from a
pack protection event.

Both words read **0 in every capture taken so far** (comprehensive 2026-09-08,
probe 2026-09-15), on a healthy pack. So the addressing and the "no fault"
decode are confirmed, and **the bit assignments are not**: they come from the
vendor hybrid protocol document (3–5 kW LV column) and have never been observed
set on this unit. See ambiguity #15.

| Word | Bit | Meaning |
|---|---|---|
| `33145` | 1 | Over voltage |
| `33145` | 2 | Under voltage |
| `33145` | 3 | Over temperature |
| `33145` | 4 | Under temperature |
| `33145` | 5 | Charge over temperature |
| `33145` | 6 | Charge under temperature |
| `33145` | 7 | Discharge over current |
| `33146` | 0 | Charge over current |
| `33146` | 3 | BMS internal |
| `33146` | 4 | Module unbalanced |

Bit 0 of `33145` and bits 1, 2, 5–7 of `33146` are reserved. The raw words are
published as sensors beside the per-bit binary sensors, so a real fault can be
reconciled against the vendor table even if a bit is mislabelled here.
