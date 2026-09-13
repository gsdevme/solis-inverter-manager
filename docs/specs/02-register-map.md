# 02 — Register map

> **Status: authored (Phase 3).** The Go-facing decode/encode contract for
> `internal/inverter`. Every rule here traces back to the confirmed source; this
> spec adds the Go modelling (types, field names, block grouping, encoding) on top
> of the raw facts rather than restating the live-value tables.

## Confirmed source

**`docs/phase0/findings.md`** is the confirmed, load-bearing source for this map:
the register set was **verified against the live inverter on 2026-09-08** and
cross-referenced with the authoritative online sources. Raw captures live in
`docs/phase0/fixtures/` and are the decode unit-test fixtures for this phase.

Every decode/encode rule in this spec MUST trace back to `docs/phase0/findings.md`.
Trust no register that findings.md does not confirm.

## Decode rules

- **Register width.** Each Modbus register is a **16-bit unsigned word** (`uint16`)
  unless a row below marks it signed (S16) or 32-bit (U32/S32).
- **32-bit values (U32/S32).** Two consecutive registers, **MSW at the lower
  address** (big-endian words): `raw = uint32(reg[addr])<<16 | uint32(reg[addr+1])`.
  For S32, reinterpret that `uint32` as `int32` (two's complement).
  - Not empirically forced on this unit (every total currently reads < 65536, so
    the high word is 0 today), but documentary consensus is unanimous. Decode
    MSW-first; revisit only if a total ever exceeds 65535 and reads absurd.
- **Signed values (S16/S32).** Two's complement. Battery power, grid power, battery
  current, BMS current, and AC active power are signed.
- **Scale.** Apply the per-register divisor/multiplier from the tables below
  **after** width/sign decode. `÷10` and `÷100` yield a fractional physical value;
  represent decoded physical values as `float64` at the package boundary.
- **Address convention.** `reg[addr]` is indexed by absolute Modbus address; the
  sidecar returns a raw `uint16` slice for `read_input(addr, count)` /
  `read_holding(addr, count)` starting at `addr` (see `01-sidecar-contract.md`).

### Sign conventions (confirmed)

| Quantity | Register(s) | Sign meaning |
|---|---|---|
| Battery power | 33149·33150 (S32, W) | **+ = charge, − = discharge** |
| Battery current | 33134 (S16, ÷10 A) | **+ = charge, − = discharge** |
| Grid power | 33130·33131 (S32, W) | **+ = export, − = import** |
| Battery direction flag | 33135 (U16) | **0 = charge, 1 = discharge** |

**Grid power is one S32, not two U16s.** 33130 and 33131 are the MSW/LSW of a
single signed 32-bit watt value. The legacy app treated them as independent
`grid_import`/`grid_export` and needed a `≤24000` clamp to hide the high word —
that was a bug. Decode as one S32.

## Input registers (fc04, read-only)

Confirmed decode set. `Type` drives width/sign; `Scale` is applied after decode.
Live values and cross-checks are in findings.md — not repeated here.

| Addr | Field | Type | Scale / unit |
|---|---|---|---|
| 33022–33027 | RTC (y/mo/d/h/mi/s) | U16×6 | y = reg+2000; naive local datetime, no TZ |
| 33035 | Generation today | U16 | ÷10 kWh |
| 33036 | Generation yesterday | U16 | ÷10 kWh |
| 33049 / 33050 | PV1 voltage / current | U16 | ÷10 V / ÷10 A |
| 33051 / 33052 | PV2 voltage / current | U16 | ÷10 V / ÷10 A |
| 33057·33058 | Total PV (DC) power | U32 | ×1 W |
| 33079·33080 | Inverter AC active power | S32 | ×1 W |
| 33093 | Inverter temperature | S16 | ÷10 °C |
| 33094 | Grid frequency | U16 | ÷100 Hz |
| 33095 | Inverter status | U16 | enum (3 = running) |
| 33121 | Operating status | U16 | bitfield (raw for now) |
| 33130·33131 | Grid power (meter) | S32 | ×1 W (+export/−import) |
| 33132 | Work-mode read-back | U16 | bitfield — mirrors 43110 (see below) |
| 33133 | Battery voltage | U16 | ÷10 V |
| 33134 | Battery current | S16 | ÷10 A (+charge/−discharge) |
| 33135 | Battery direction flag | U16 | 0 = charge, 1 = discharge |
| 33139 | Battery SOC | U16 | ×1 % |
| 33140 | Battery SOH | U16 | ×1 % |
| 33141 | BMS battery voltage | U16 | ÷100 V |
| 33142 | BMS battery current | S16 | ÷10 A |
| 33147 | House load power | U16 | ×1 W |
| 33149·33150 | Battery power | S32 | ×1 W (+charge/−discharge) |
| 33161·33162 | Battery total charge energy | U32 | ×1 kWh |
| 33163 | Battery charge today | U16 | ÷10 kWh |
| 33165·33166 | Battery total discharge energy | U32 | ×1 kWh |
| 33167 | Battery discharge today | U16 | ÷10 kWh |
| 33169·33170 | Grid total import energy | U32 | ×1 kWh |
| 33171 | Grid import today | U16 | ÷10 kWh |
| 33173·33174 | Grid total export energy | U32 | ×1 kWh |
| 33175 | Grid export today | U16 | ÷10 kWh |

**Derived/consistency checks** (used by tests, not registers): battery power ≈
battery current × battery voltage; grid S32 ≈ external meter reading. See
findings.md for the pinned live cross-checks.

**Block reads.** The decoder consumes raw slices from the sidecar. Group reads
into contiguous blocks rather than one register at a time; decoders MUST accept a
base address + slice so a block read can be sliced.

The live Solarman datalogger NAKs any single read wider than ~100 registers with
`illegal_address` (probed: `33022+100` OK, `33022+110` NAK) — a **stricter** bound
than the sidecar's 125-register wire cap (`REQ-SD-07`). The manager therefore reads
the telemetry bank (33022–33175) as **two blocks of ≤ 100**, split at `33121|33122`
so no multi-word value straddles the boundary:

- block 1 = `{33022, 100}` → covers `33022..33121`
- block 2 = `{33122, 54}` → covers `33122..33175`

The `33121|33122` split avoids the 6-word RTC block (33022–33027) and every U32/S32
pair (33057·58, 33079·80, 33130·31, 33149·50, 33161·62, 33165·66, 33169·70,
33173·74). `Snapshot` resolves each register by absolute address across the blocks,
so the two reads need no merging.

## Holding registers (fc03 read / fc06 write)

| Addr | Field | Type | Scale / values | R/W |
|---|---|---|---|---|
| 43000–43005 | RTC set (y/mo/d/h/mi/s) | U16×6 | y = reg+2000; mirrors input 33022–33027 | R/W |
| 43011 | Min SOC (overdischarge) | U16 | ×1 % | R/W |
| 43110 | Work mode (energy-storage switch) | U16 | bitfield (see below) | R/W |
| 43114 | Charge/discharge enable | U16 | 0 / 1 | R/W |
| 43115 | Charge/discharge direction | U16 | 0 = charge, 1 = discharge | R/W |
| 43116 | Instant charge/discharge current | U16 | ÷10 A | R/W |
| 43117 / 43118 | Max charge / discharge current | U16 | ÷10 A (1000 = 100.0 A unit limit) | R/W |
| 43141 | Timed charge current | U16 | ÷10 A | R/W |
| 43142 | Timed discharge current | U16 | ÷10 A | R/W |
| 43143 / 43144 | Timed charge start H / M | U16 | 0–23 / 0–59 | R/W |
| 43145 / 43146 | Timed charge end H / M | U16 | 0–23 / 0–59 | R/W |
| 43147 / 43148 | Timed discharge start H / M | U16 | 0–23 / 0–59 | R/W |
| 43149 / 43150 | Timed discharge end H / M | U16 | 0–23 / 0–59 | R/W |

## Work-mode bitfield (43110)

Reads back at **both** `43110` (holding) and `33132` (input). A `uint16` bitfield.

| Bit | Value | Meaning |
|---|---|---|
| 0 | 1 | Self-use |
| 1 | 2 | Timed charge/discharge ("optimised revenue" / legacy "optimal income") |
| 5 | 32 | Allow grid charge (**bit5 = 1 ⇒ allow**) |

Named values used by the controls layer:
- **35** = bit0+bit1+bit5 = self-use + grid-charge + **timed ON** ("optimal income ON").
- **33** = bit0+bit5 = self-use + grid-charge + **timed OFF** ("optimal income OFF").

Toggling "optimal income" flips **bit 1 only** (35 ↔ 33); the switch decode/encode
MUST preserve the other bits (read-modify-write), never assume the whole field.

## Write-path & encoding

- **fc06 single-register writes** are accepted and take effect immediately on
  read-back. Setpoints **persist** — no ~120 s commit/revert on 43141 (confirmed
  over 130 s). Re-verify per-register if a new setpoint misbehaves.
- **Amps encoding:** `raw = uint16(round(amps * 10))`; decode is `raw / 10.0`.
  The inverter accepts up to 100.0 A (`43117/43118` = 1000); the HA control range
  is deliberately clamped to **0–60 A** per the plan.
- **RTC set:** write the 6-register block `43000–43005` as `[y-2000, mo, d, h, mi, s]`.
  Naive local datetime, **no timezone register**. Drift is real (≈ +205 s observed)
  and only corrects via a write; an RTC-drift sensor + optional periodic auto-sync
  is a writable-controls / scheduler concern.
- **READ-BEFORE-WRITE guard.** Every write MUST first read the register and skip
  the write when the value already matches (holding registers are flash-backed —
  needless writes wear flash). The guard itself is specified in
  `03-mqtt-ha-discovery.md` / `05-config.md`; `internal/inverter` provides the
  decode/encode primitives it builds on.

## Not yet modelled (captured, deferred)

The full sweep (`input 33000–33304`, `holding 43000–43195`) reads cleanly. Beyond
the set above (decode as future features permit, per findings.md §"Additional
registers"): product/model/firmware (33000–33003), inverter serial as ASCII
(33004–33011, for the HA device block), holding mirrors of input stats
(43034–43067), the multi-slot schedule table (43090–43122), and various
limit/config registers (33181–33217, 43012–43049).
