# 02 — Register map

> **Status: skeleton.** Full content authored in Phase 3 (decode). This spec is a
> thin index over the confirmed source below; do not restate register facts here —
> derive them from the source.

## Confirmed source

**`docs/phase0/findings.md`** is the confirmed, load-bearing source for this map:
the register set was **verified against the live inverter on 2026-09-08** and
cross-referenced with the authoritative online sources. Raw captures live in
`docs/phase0/fixtures/` and become the decode unit-test fixtures in Phase 3.

Every decode/encode rule in this spec MUST trace back to `docs/phase0/findings.md`.

## Decode rules (TODO — from findings.md)

TODO: register width (16-bit words); 32-bit U32/S32 word order (**MSW at the lower
address**); signed two's-complement values and the confirmed sign conventions
(battery/grid power, battery current, direction flag).

## Input registers, fc04, read-only (TODO — from findings.md)

TODO: system/DC/AC/meter/battery/energy blocks (RTC, generation, PV, AC power,
temperature, frequency, status, grid S32, battery voltage/current/SOC/SOH/power,
energy totals).

## Holding registers, fc03 read / fc06 write (TODO — from findings.md)

TODO: RTC set (43000–43005), min SOC (43011), work-mode (43110), charge/discharge
enable/direction/current, timed charge/discharge current (43141/43142) and schedule.

## Work-mode bitfield 43110 (TODO — from findings.md)

TODO: bit0 self-use, bit1 timed charge/discharge, bit5 allow grid charge; named
values 35 ("optimal income ON") / 33 ("optimal income OFF").

## Write-path notes (TODO — from findings.md)

TODO: fc06 single-register writes; setpoints persist (no ~120 s revert on 43141);
amps encoding `round(amps,1)*10`; HA control range clamped 0–60 A. The
READ-BEFORE-WRITE write-guard that governs all writes is specified in
`03-mqtt-ha-discovery.md` and `05-config.md`.
