# Requirements

Stable, traceable requirement IDs. Prefix meanings: `SD` sidecar contract/client,
`RM` register map/decode, `HA` MQTT/Home Assistant, `SC` scheduling, `CF` config,
`LC` lifecycle/health, `TS` testing, `DP` deployment.

IDs are **stable and never renumbered** — they are cited from the specs and from
code comments (`internal/controls/*.go`, `internal/homeassistant/discovery.go`).
A behaviour change updates the relevant `REQ-*` entry and its spec in the same
commit; new behaviour takes the next free number in its prefix.

## Sidecar (`sidecar/`, `01-sidecar-contract.md`)

The thin Python transport sidecar (Phase 2, #19). See
[`01-sidecar-contract.md`](01-sidecar-contract.md) for the full wire contract.

- **REQ-SD-01** HTTP with JSON request/response bodies on `:8081` by default
  (`SIDECAR_LISTEN_ADDR`, REQ-SD-08), matching the Go `SIDECAR_URL` default. In the
  deployed two-container pod the manager reaches it over **loopback** — that is a
  pod-topology property (REQ-DP-01, `08-deployment.md`), **not** a bind guarantee:
  the default empty host binds all interfaces and `docker-compose.yml` publishes
  `8081:8081` to the host. → `sidecar/http_api.py`, `sidecar/__main__.py`
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
  `illegal_address`/`frame_error`/`connection_error`/`not_found`, plus
  `internal_error` (500) — the base `SidecarError` code, used as the catch-all when
  an unhandled exception escapes `do_GET`/`do_POST`, and the Go client's fallback
  for any unrecognised code. → `sidecar/http_api.py`, `sidecar/errors.py`

- **REQ-SD-08** `SIDECAR_LISTEN_ADDR` (default `:8081`) is split into host/port on the
  **last** `:`; an address with no colon fails validation. An empty host binds all
  interfaces (Go's `:8081` convention) and the startup line renders it `0.0.0.0:<port>`.
  → `sidecar/config.py`, `sidecar/__main__.py`
- **REQ-SD-09** `MOCK_FIXTURE` selects the `MODE=mock` seed snapshot(s): one path, or
  several comma-separated, applied in order so a later fixture wins where two define the
  same register. Unset or empty, it resolves to `live-snapshot-full-sweep.json` **then**
  `live-snapshot-comprehensive.json` under `docs/phase0/fixtures/` **relative to the
  package's parent directory**, so it works both from a checkout and from the image
  (which copies the fixtures alongside the package). Two are needed because the full
  sweep skips the ranges the other snapshots already cover, leaving the whole battery
  block (33121–33180) zero — a mock run seeded from it alone looks like a dead inverter.
  The two captures come from one session and agree on all 22 registers they share, so the
  merged device is internally consistent. In `MODE=mock` **every** listed path must be a
  file, or config validation fails. → `sidecar/config.py`, `sidecar/mock.py`
- **REQ-SD-10** The sidecar reads `INVERTER_IP`, `INVERTER_SERIAL` and `INVERTER_PORT`
  (default `8899`) under the **same names** as the Go manager, so both processes read one
  `.env` (REQ-CF-01). `INVERTER_IP` and `INVERTER_SERIAL` are required only when
  `MODE=live`; a non-integer `INVERTER_PORT` is a validation error. → `sidecar/config.py`
- **REQ-SD-11** `parse_duration_or_seconds` mirrors the Go duration grammar for
  `INVERTER_SOCKET_TIMEOUT`: a bare int/float is seconds, otherwise a Go duration string
  (`10s`, `1m30s`, `500ms`; units `ns|us|µs|ms|s|m|h`, optional leading `-`) is parsed to
  seconds. Empty takes the default (`10s`); an unparseable string, or a result `<= 0`, is
  a validation error. → `sidecar/config.py`
- **REQ-SD-12** Secret redaction mirrors the Go `LogValue()` contract (REQ-CF-03):
  `INVERTER_SERIAL` is never logged verbatim. `Config.redacted_serial()` renders it as
  first-two + `****` + last-two (`****` when 4 chars or fewer, empty for empty) and the
  `MODE=live` startup line logs only that form. → `sidecar/config.py`, `sidecar/__main__.py`
- **REQ-SD-13** Fail-fast config: every problem is collected and raised **once** as a
  single `ConfigError` (mirroring the Go `errors.Join` style of REQ-CF-02), which
  `__main__` logs before exiting `1`. `MODE` is required and must be exactly `mock` or
  `live`. → `sidecar/config.py`, `sidecar/__main__.py`
- **REQ-SD-14** `SidecarHTTPServer` extends `ThreadingHTTPServer` with
  `daemon_threads = True` and carries the transport and mode for its handlers, so
  `/health` and register calls are accepted concurrently and no worker thread can block
  process shutdown. The transport's own global lock still guarantees exactly one Modbus
  frame in flight (REQ-SD-05). → `sidecar/http_api.py`

> Note: the Go **client** (`internal/sidecarclient`) that consumes this contract,
> and wiring `/health` into `/readyz`, are covered by `REQ-LC-09`/`REQ-LC-11`.

## Register map / decode (`internal/inverter`, `02-register-map.md`)

`internal/inverter` owns **all** register semantics; the sidecar is a dumb transport
(REQ-SD-02). Every rule below traces to a probe-confirmed row in
[`docs/phase0/findings.md`](../phase0/findings.md) and is modelled for Go in
[`02-register-map.md`](02-register-map.md). Trust no register findings.md does not
confirm.

- **REQ-RM-01** Register/address model: every Modbus register is a `uint16` unless a
  spec row marks it S16/U32/S32. Decoders take a base address plus a raw slice and
  resolve registers by **absolute** Modbus address, so a multi-block `Snapshot`
  needs no merging or reordering; a register the snapshot does not cover is
  `ErrRegisterOutOfRange`. → `inverter/block.go`, `inverter/registers.go`
- **REQ-RM-02** 32-bit word order is **MSW at the lower address** (big-endian words):
  `raw = uint32(reg[addr])<<16 | uint32(reg[addr+1])`; S32 reinterprets that `uint32`
  as `int32` (two's complement). Documentary consensus, not empirically forced — every
  32-bit total currently reads below 65536, so the high word is always 0 today
  (findings.md ambiguity #1). → `inverter/block.go` (`u32`, `s32`)
- **REQ-RM-03** Scale is applied **after** width/sign decode: `÷10` (`div10`), `÷100`
  (`div100`) or `×1`. Decoded physical values cross the package boundary as `float64`.
  → `inverter/telemetry.go`
- **REQ-RM-04** Input-register address constants (fc04) — `RegRTCRead` (33022) through
  `RegGridExportToday` (33175) — are named once and are exactly the rows the confirmed
  input table holds. `RegTotalPVPower`, `RegACActivePower`, `RegGridPower`,
  `RegBatteryPower` and the four lifetime-energy constants name the **MSW** of their
  32-bit pair. → `inverter/registers.go`, `02-register-map.md` §"Input registers",
  `docs/phase0/findings.md` §"Confirmed input registers"
- **REQ-RM-05** Holding-register address constants (fc03/fc06) — `RegRTCSet` (43000),
  `RegMinSOC` (43011), `RegWorkMode` (43110), `RegChargeDischargeEnable` through
  `RegMaxDischargeCurrent` (43114–43118), `RegTimedChargeCurrent`/
  `RegTimedDischargeCurrent` (43141/43142) and slot 1's eight H/M registers
  (43143–43150) — same provenance. → `inverter/registers.go`,
  `02-register-map.md` §"Holding registers", `docs/phase0/findings.md`
  §"Confirmed holding registers"
- **REQ-RM-06** Grid power is **one S32** at 33130·33131 (`+ = export, − = import`),
  never two independent U16s. The legacy app's `grid_import`/`grid_export` split and
  its `≤24000` clamp were a bug: the clamp existed only to hide the S32 high word read
  as a standalone watt value. → `inverter/telemetry.go` (`Grid.PowerW`),
  `docs/phase0/findings.md` ambiguity #3
- **REQ-RM-07** Battery power (33149·33150) and battery current (33134) are read as
  **magnitudes**; the sign comes from the 33135 direction flag (**0 = charge**,
  1 = discharge) and is published `+ = charge, − = discharge`. A zero magnitude is
  returned unsigned so a resting battery never publishes `-0`. Confirmed live in both
  directions (2026-09-14: +381 W with `33135`=1 while discharging).
  → `inverter/telemetry.go` (`signedByDirection`),
  `docs/phase0/findings.md` §"2026-09-14 — battery sign correction"
- **REQ-RM-08** RTC: the six-register block decodes and encodes as
  `[y-2000, mo, d, h, mi, s]` at either bank — `RegRTCRead` (33022, input) or
  `RegRTCSet` (43000, holding) — into a **naive local** datetime, because the inverter
  carries no timezone register. `Drift(rtc, now)` is positive when the inverter clock
  runs fast, and `RTCWriteRegisters(t)` expands the block into six address/value pairs
  so the guarded write loop (REQ-HA-13) compares and writes each register
  independently. → `inverter/rtc.go`
- **REQ-RM-09** The 43110 work-mode bitfield (mirrored at input 33132): bit 0 self-use,
  bit 1 timed ("optimal income"), bit 5 allow-grid-charge (**1 = allow**). Named values
  `WorkModeTimedOn` = 35 and `WorkModeTimedOff` = 33. `WorkMode.Raw` preserves the
  original word so `Encode()` overlays only the three known flags and **bits this map
  does not model survive a round trip**. `WithTimed(on)` returns a copy with **only
  bit 1** changed — the single mutator the catalogue needs, and how REQ-HA-09's
  read-modify-write flips 35 ↔ 33 without assuming the whole field. Bits 0 and 5 have
  no mutator: nothing writes them today, and adding one needs a confirmed use case
  (the guardrail is that an unprobed work mode is never written).
  → `inverter/workmode.go`
- **REQ-RM-10** Current registers encode as `raw = uint16(round(amps * 10))` and decode
  as `raw / 10`. `EncodeAmps` deliberately does **not** clamp: the 0–60 A Home
  Assistant range is the separate policy helper `ClampHAChargeAmps`, kept out of the
  primitive so the encoder stays a pure unit conversion. The inverter itself accepts up
  to 100.0 A (`43117`/`43118` = 1000); the narrower range is an HA-control decision
  (findings.md ambiguity #9). → `inverter/controls.go`
- **REQ-RM-11** Timed-slot layout: three slots at `RegTimedSlotStride` = 10 from slot 1's
  base, with each slot's H/M windows at the **slot-1 offsets `+2..+9`** — the offsets are
  derived from the confirmed slot-1 addresses rather than hand-typed, so slots 2 and 3
  follow the register map. The **leading pair of each slot** (`43151`/`43152`,
  `43161`/`43162`) is documented **UNCONFIRMED** and is never decoded and never written:
  charge and discharge currents are global (43141/43142), not per slot. An all-zero H/M
  window decodes as *unset* (`TimedWindow.IsZero`); an hour above 23 or a minute above 59
  is a decode error. `TimedSlotWriteRegisters` emits only offsets `+2..+9`, lists a
  direction whose target window is unset **before** one that is set (so a slot swapping
  direction clears the old window first and never transits the unprobed both-set state),
  and within a direction always orders start hour, start minute, end hour, end minute.
  → `inverter/schedule.go`, `inverter/registers.go`,
  `docs/phase0/findings.md` §"Stage A (#27)", §"Stage B2 pre-design probe"
- **REQ-RM-12** Telemetry-bank read split: the live datalogger NAKs any single read wider
  than ~100 registers with `illegal_address` — a **stricter** bound than the sidecar's
  125-register wire cap (REQ-SD-07) — so 33022–33214 is read as two blocks of ≤ 100,
  `{33022, 100}` and `{33122, 93}`, split at `33121|33122`. That boundary straddles
  neither the 6-word RTC block nor any U32/S32 pair, and `Snapshot` resolves across both
  blocks (REQ-RM-01), so the two reads need no merging. Both widths are
  **live-confirmed** 2026-09-15 — read back-to-back in two sequential passes that
  returned identical words (`docs/phase0/fixtures/live-poll-blocks-2026-09-15.json`) —
  and the blocks must be issued **sequentially**: concurrent reads on the single
  datalogger socket time out, which the manager's serialised poll already guarantees.
  → `publisher/collect.go`, `docs/phase0/findings.md` ambiguity #10
- **REQ-RM-13** *(reserved)* Five probe-confirmed holding constants are **named but not
  yet wired** into decode, publish or any write path: `RegMinSOC` (43011),
  `RegForceChargeSOCSetting` (43018), `RegChargeDischargeEnable` (43114),
  `RegChargeDischargeDirection` (43115) and `RegInstantCurrent` (43116). The two SOC
  settings are reachable through their input mirrors (REQ-RM-17), which is what
  telemetry reads. Naming them is deliberate — they are confirmed
  ground truth — but giving any of them behaviour needs its own requirement.
  `RegMaxChargeCurrent` (43117) and `RegMaxDischargeCurrent` (43118) left this
  reserved set in REQ-RM-15 — they are now read and published, still never written.
  → `inverter/registers.go`
- **REQ-RM-14** *(deferred scope)* Registers findings.md captures that `internal/inverter`
  deliberately does **not** model yet: product/model/firmware `33000–33003`, inverter
  serial as ASCII `33004–33011`, limit/config `33181–33217` **except the SOC mirrors
  `33213`/`33214`** (modelled in REQ-RM-17), the meter cross-check
  `33263`, holding `43012–43049` **except the force-charge SOC setting `43018`**
  (named under REQ-RM-13 and mirrored at `33214`); the deferred remainder includes the
  `43024`/`43025` SOC-shaped pair, where `43024` acks fc06 but silently ignores it, so
  treat it read-only. Also deferred: the input-statistic
  mirrors `43034–43067`, and the protection-threshold table `43090–43122` (ruled out as
  a schedule in Stage A). This is an explicit **deferred scope**, not an omission: the
  full sweep reads cleanly and expanding to complete Modbus coverage is intended, but
  each addition lands with its own `REQ-RM-*` and its own confirmation.
  → `inverter/registers.go` (scope note),
  `docs/phase0/findings.md` §"Additional registers captured"

- **REQ-RM-15** Current ceilings are decoded as three distinct pairs and never conflated:
  the **BMS-advertised** limits `RegBMSChargeCurrentLimit` (33143) /
  `RegBMSDischargeCurrentLimit` (33144), U16 `÷10` A, unsigned (the 33135 direction flag
  does not apply to a limit) — the charge limit tapers towards 0 as the pack fills; the
  **inverter-configured** ceiling `RegMaxChargeCurrent` (43117) /
  `RegMaxDischargeCurrent` (43118), read via the setpoint block and published read-only,
  **never written**; and the **timed-slot** setpoints 43141/43142 (REQ-HA-08), the only
  writable pair. Both new pairs fall inside blocks already read, so they add no Modbus
  traffic. Confirmed against the Solis app on 2026-09-15 (0 A charge / 112.5 A discharge).
  → `inverter/registers.go`, `inverter/telemetry.go`, `controls/setpoints.go`,
  `docs/phase0/findings.md` §"BMS current limits", ambiguity #14
- **REQ-RM-16** The BMS fault words `RegBMSFault1` (33145) and `RegBMSFault2` (33146)
  are decoded as bitfields into `BMSFault1`/`BMSFault2`, each preserving the original
  word in `Raw` and exposing `Any()` (raw ≠ 0) so an unmodelled bit still reads as a
  fault. They are **decode-only** — nothing writes them. The bit assignments come from
  the vendor hybrid protocol document and are **unconfirmed live**: both words read 0 in
  every capture, so only the addressing and the "no fault" decode are proven, and `Raw`
  is published alongside the per-bit sensors so a real fault can be reconciled against
  the vendor table (REQ-HA-22).
  → `inverter/bmsfault.go`, `inverter/telemetry.go`,
  `docs/phase0/findings.md` §"BMS fault words", ambiguity #15
- **REQ-RM-17** The SOC-threshold mirrors `RegOverdischargeSOC` (33213) and
  `RegForceChargeSOC` (33214) are decoded as plain percentages (×1) and published
  read-only; they are the input-bank mirrors of the holding SOC settings `RegMinSOC`
  (43011) and `RegForceChargeSOCSetting` (43018), which is their confirmation:
  `33213` = `43011` = 20 and `33214` = `43018` = 19 in the same full sweep, both
  mirrors re-read unchanged a week later. They
  leave the deferred `33181–33217` range of REQ-RM-14. Reaching them widens the second
  telemetry block from `{33122, 54}` to `{33122, 93}` (REQ-RM-12) — still one read and
  still inside the ~100-register datalogger cap, so no extra Modbus traffic. The
  93-register read is live-confirmed 2026-09-15
  (`docs/phase0/fixtures/live-poll-blocks-2026-09-15.json`, two identical passes).
  → `inverter/registers.go`, `inverter/telemetry.go`, `publisher/collect.go`,
  `docs/phase0/findings.md` §"SOC threshold mirrors"
- **REQ-RM-18** `StatusLabel` maps the `33095` status enum to the vendor display text
  (protocol Appendix 2, "all 4G" column), falling back to the hex form `0x%04X` for an
  unlisted code so an unknown status is legible and never mislabelled. The table lives in
  `internal/inverter` as domain knowledge, like `WorkMode`; `homeassistant/state.go` only
  calls it. The raw enum keeps its own sensor (REQ-HA-22).
  → `inverter/status.go`, `homeassistant/state.go`,
  `docs/phase0/findings.md` §"Inverter status enum"

## MQTT & Home Assistant (`internal/mqtt`, `internal/homeassistant`, `internal/publisher`)

Phase 4 (#20/#21) is **read-only** discovery + state + availability. See
[`03-mqtt-ha-discovery.md`](03-mqtt-ha-discovery.md) for the full topic scheme,
payload shapes and the 57-entity table.

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
  the 57 read-only entity keys plus the four control-readback fields
  (`set_charge_current`, `set_discharge_current`, `optimal_income`,
  `boost_select`) — 61 tags.
  `rtc` is RFC3339; `rtc_drift` is seconds.
  → `homeassistant/state.go`, `homeassistant/entities.go`
- **REQ-HA-03** Entity classes per the `03` table; **daily** energy counters use
  `state_class: total`, **lifetime** counters `total_increasing`; signed
  battery/grid power is one signed entity (not split), with a derived
  `battery_charging` binary_sensor. `battery_power` and `battery_current` take
  their sign from the 33135 direction flag during decode (the registers are
  magnitudes). `battery_charge_power` and `battery_discharge_power` are **derived
  convenience sensors** for Home Assistant (`max(battery_power, 0)` /
  `max(-battery_power, 0)`, computed in `BuildState` from that one signed value —
  not an independent decode of the register halves). Fractional-scale entities
  publish `suggested_display_precision` matching the register scale (÷10 → 1,
  ÷100 → 2) so HA renders `49.0 V`, not `49 V`.
  → `homeassistant/entities.go`, `homeassistant/state.go`
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
  `set_discharge_current` (`43142`), HA `number`, 0–60 A step 0.1, U16 `÷10` A
  (REQ-RM-10), written via fc06 behind the READ-BEFORE-WRITE guard. Both discovery
  payloads carry `mode: "box"` (from `Entity.Mode`), so Home Assistant renders a
  numeric input box rather than a slider — a 0.1 A step across a 0–60 range is not
  usefully draggable. → `internal/homeassistant`, `internal/controls`
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
- **REQ-HA-19** `controls.Handler.OnMessage(ctx, topic, payload)` is the **public command
  entry point** — what the MQTT router, the unit tests and the godog suite all call. It
  takes the shared API lock so a command never interleaves with a poll on the single
  sidecar socket (REQ-SC-05), then delegates to `Apply`, which routes on the topic's
  `<key>` segment and performs the parse, server-side validation (REQ-HA-12) and guarded
  write (REQ-HA-10). `Apply` remains callable directly for a caller that already holds
  the lock. → `controls/handler.go`, `internal/mqtt`, `cmd/serve.go`
- **REQ-HA-20** Publisher seams, so the publish path is one code path in every mode:
  `publisher.Discard` is a no-op `Publisher` used by the no-broker (`mock`) path, so the
  pipeline runs the **identical** build-and-publish step and the status page still
  receives the built document (REQ-LC-12) instead of the code branching around the
  publisher; `publisher.WithNow` injects the clock the Service reads, so `serve.go` shares
  one clock across the scheduler, the RTC sync and `rtc_drift` (REQ-SC-07);
  `Service.AvailabilityTopic()` exposes the LWT topic so `cmd` sets the Will without
  rebuilding topic strings (REQ-HA-04); and `publisher.RecordingPublisher` is the
  concurrency-safe last-message-per-topic fake the unit tests and the acceptance suite
  assert MQTT output against without a broker (REQ-TS-03).
  → `publisher/publisher.go`, `publisher/recording.go`, `cmd/serve.go`
- **REQ-HA-21** Four read-only current-ceiling sensors publish the pairs of REQ-RM-15,
  all `device_class: current`, `entity_category: diagnostic`, unit `A`, precision 1:
  `bms_charge_current_limit` / `bms_discharge_current_limit` (from telemetry) carry
  `state_class: measurement` because they move with pack state;
  `inverter_max_charge_current` / `inverter_max_discharge_current` (from the setpoint
  block) carry **no** `state_class`, being near-static configuration for which long-term
  statistics would be noise. They are read-only and are published whether or not
  `CONTROLS_ENABLED` is set. The pre-existing `set_charge_current` /
  `set_discharge_current` are relabelled "Timed charge/discharge current" so the three
  tiers are distinguishable in the UI; their **keys are unchanged**, so `unique_id`,
  `object_id` and every entity id are untouched.
  → `homeassistant/entities.go`, `homeassistant/state.go`, `controls/setpoints.go`,
  `cmd/serve.go`
- **REQ-HA-22** Fifteen read-only diagnostic entities publish the BMS fault, SOC-threshold
  and status-label additions of REQ-RM-16/17/18, all `entity_category: diagnostic` and all
  published whether or not `CONTROLS_ENABLED` is set: `bms_fault_1` / `bms_fault_2`
  (sensors, the raw `33145`/`33146` words, no device or state class); ten
  `device_class: problem` binary_sensors, one per decoded bit — `bms_over_voltage`,
  `bms_under_voltage`, `bms_over_temp`, `bms_under_temp`, `bms_charge_over_temp`,
  `bms_charge_under_temp`, `bms_discharge_over_current`, `bms_charge_over_current`,
  `bms_internal_protection`, `bms_module_unbalanced`; `overdischarge_soc` /
  `force_charge_soc` (unit `%`, **no** `device_class` — `battery` would read as the
  device's own remaining charge — and **no** `state_class`, near-static configuration
  for which long-term statistics would be noise); and `status_text`, the decoded
  `33095` label published beside the unchanged numeric `status`. Entity catalogue: 47 → 62
  (57 read-only, 61 state tags).
  → `homeassistant/entities.go`, `homeassistant/state.go`, `inverter/status.go`,
  `inverter/bmsfault.go`

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
- **REQ-SC-07** Two injectable clock seams, each with a distinct job. `Now` supplies the
  current time for **RTC drift** (`Drift(tel.Time, now())`) and boost planning, and is
  shared by the scheduler, the controls handler and the publisher so one fake clock covers
  all three. `After` supplies **retry backoff waits only** (REQ-SC-02). The **poll cadence
  is a `time.NewTicker`**, not a seam — so a poll that runs long does not push the schedule
  out, the next tick arrives on the original cadence. Both seams default to `time.Now` /
  `time.After` and make the loop and the backoff deterministic under `testing/synctest`.
  → `scheduler/scheduler.go`, `cmd/serve.go`, `controls/handler.go`
- **REQ-SC-08** Setpoint-freshness gating of the schedule reconcile. `setpointFreshness`
  records — atomically, zero value meaning *fresh*, so the first poll is never treated as
  stale — whether the last read decoded setpoints from the inverter or reused the
  scheduler's last-known cache (REQ-SC-03). `gate` wraps the `scheduler.Reconciler` so a
  **stale cycle is skipped in full** (logged at debug, reported as "wrote nothing"),
  exactly like a cycle whose publish failed: slots reused from cache can be a whole poll
  interval old, and diffing against them would let the manager clear a window it never
  saw — a boost the owner set from the Solis app between two polls. The next poll that
  reads setpoints successfully re-asserts the schedule. The same flag dates the setpoints
  the status page shows (`Reading.SetpointsStale`, REQ-LC-12).
  → `cmd/freshness.go`, `cmd/serve.go`, `controls/reconcile.go`
- **REQ-SC-09** `Scheduler.PollNow(ctx)` runs exactly one poll cycle through the **same**
  `poll` the timed loop uses — same `apiMu`, same retry/cache/publish/health/RTC-sync/
  reconcile path — so the acceptance suite drives cycles explicitly instead of waiting on
  a ticker and asserts the real sequence rather than a parallel test-only one.
  → `scheduler/scheduler.go`, `features/polling.feature`

## Config (`internal/config`, `05-config.md`)

- **REQ-CF-01** All env vars in `05-config.md` bound with defaults. → `config.go`
- **REQ-CF-02** Fail-fast validation via `errors.Join`: every problem is collected and
  reported at once rather than on first failure. Malformed values fail startup rather
  than silently falling back to the default — a non-integer `INVERTER_PORT`,
  `POLL_MAX_RETRIES` or `FAILURE_THRESHOLD` is an aggregated error in its own right,
  distinct from a parsed-but-out-of-range value. → `config.go`
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
- **REQ-CF-09** `SIDECAR_STARTUP_TIMEOUT` (Go duration, default `30s`, must be `>= 0`;
  `0` disables the startup wait) bounds the wait for the sidecar's HTTP listener
  described in `REQ-LC-11`. Validated in the same `errors.Join` fail-fast pass and
  logged like the rest of the config. → `config.go`, `.env.dist`, `cmd/serve.go`
- **REQ-CF-10** `CONTROLS_ENABLED` (bool, default `true`) is parsed and validated in the
  same `errors.Join` fail-fast pass, so an unparseable value fails startup rather than
  silently defaulting. It is the **Ruling R1 kill switch** consumed by REQ-HA-13 (command
  entities omitted from discovery, no command subscription), REQ-HA-17 (no schedule
  reconcile) and REQ-SC-06 (no RTC auto-sync) — with it false the manager is fully
  read-only. → `config.go`, `.env.dist`, `05-config.md`

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
- **REQ-LC-11** Startup **waits for the sidecar to serve** before announcing and
  polling: after the sidecar client is built and **before** the MQTT connect,
  discovery and `online` publish and before the scheduler starts, `/health` is probed
  every `500ms` for up to `SIDECAR_STARTUP_TIMEOUT` (`REQ-CF-09`). Any `200` ends the
  wait (`inverter_reachable` is ignored — reachability is `REQ-LC-09`'s job) and logs
  `sidecar serving` with the elapsed time; exhausting the timeout logs
  `sidecar not serving after startup timeout, continuing` at WARN and startup
  continues, since the scheduler's retries and the `/readyz` gate cover a sidecar that
  is still down. A SIGTERM during the wait falls through to the `REQ-LC-10` teardown
  without the warning. This removes the spurious first-poll
  `connection refused` / `poll failed` on every pod start. →
  `cmd/serve.go`, `sidecarclient.WaitUntilServing`
- **REQ-LC-12** The status page (`GET /{$}`) shows the **last inverter values**: every
  key of the flat state document (`REQ-HA-*`, `03-mqtt-ha-discovery.md`) as built on
  the most recent poll or command refresh, sorted by key, with the reading's age;
  "no readings yet" before the first successful read. Values are recorded in-process
  (`RecordReading`) from the *same built document* the publisher sends — `cmd`'s
  `statePublisher` builds it once and fans it out to Home Assistant and the page, so
  there is no second build to drift and no MQTT subscription, and the page works
  without a broker (`publisher.Discard`). When the setpoints in a reading were reused
  from cache after a failed holding-register read, the page says so and dates them
  from their last successful read. The page auto-refreshes at the poll interval,
  whose 5 s floor `POLL_INTERVAL` validation (`REQ-SC-01`) already enforces; a zero
  interval emits no refresh tag. → `internal/server`, `cmd/serve.go`, `cmd/state.go`
- **REQ-LC-13** `Server.SetReady(bool)` is a deliberate **test/godog seam**: it flips the
  readiness flag directly, bypassing the consecutive-failure counter (setting ready also
  resets that counter), so a scenario can assert a `/readyz` transition without driving
  whole poll cycles. Production readiness always flows through `MarkSuccess`/`MarkFailure`
  (REQ-LC-09) — nothing in `cmd` calls `SetReady`.
  → `internal/server/server.go`, `features/health.feature`
- **REQ-LC-14** `mqtt.Client.SetOnConnectionUp(fn)` registers or replaces the reconnect
  hook after construction (production supplies it once via `Options.OnConnectionUp`). It
  is the seam the client's own tests use to exercise the `fireOnUp`/`drainHook` path —
  that a hook runs in a tracked goroutine, that `Disconnect` cancels and **waits** for an
  in-flight hook before the clean disconnect, that a cancelled shutdown context still
  returns promptly, and that no new hook launches once teardown has begun — without
  standing up a broker. It is the unit-test half of REQ-LC-10's drain guarantee.
  → `internal/mqtt/client.go`, `internal/mqtt/client_test.go`
- **REQ-LC-08** `cmd/main.go` reports errors to stderr and exits non-zero. →
  `cmd/main.go`

## Testing (`*_test.go`, `features`, `sidecar/tests`, `07-testing.md`)

See [`07-testing.md`](07-testing.md) for the authored detail.

- **REQ-TS-01** Go unit/integration suite: 31 `*_test.go` files covering `config`
  (defaults, validation, redaction), `server` (probes, readiness, status page),
  `inverter` (decode/encode), `homeassistant` (discovery/state payloads), `publisher`,
  `controls`, `schedule`, `scheduler`, `mqtt`, `cmd` and `sidecarclient`. `make test`
  runs them and **excludes** `./features/...`, so the fast loop stays fast.
  → `*_test.go`, `Makefile`
- **REQ-TS-02** Decode tests are **fixture-driven** from the Phase 0 live captures in
  `docs/phase0/fixtures/`, so the ground truth in the tests is the inverter's own words
  rather than hand-written expectations — the same captures REQ-RM-* traces to.
  → `inverter/fixture_test.go`, `inverter/telemetry_test.go`, `inverter/writeprobe_test.go`,
  `docs/phase0/fixtures/`
- **REQ-TS-03** godog acceptance suite: six feature files / **31 scenarios** —
  `health` (2), `mqtt_discovery` (5), `mqtt_controls` (6), `polling` (3), `schedule` (2),
  `schedule_controls` (13) — sharing one `features/steps_test.go`, run by `make test-e2e`.
  The suite wires the **real** units in-process against Go fakes rather than a live
  sidecar or broker: `stubReader` implements `publisher.RegisterReader` (zero-filled or
  seeded register blocks, programmable failures), `fakeHRW` is a programmable, recording
  `controls.HoldingReadWriter` standing in for the holding-register bank, and
  `publisher.RecordingPublisher` captures MQTT output (REQ-HA-20). `httptest` serves only
  the manager's **own** health/status HTTP surface. There is no inverter, no Python
  sidecar process and no broker in the suite.
  → `features/*.feature`, `features/steps_test.go`, `publisher/recording.go`
- **REQ-TS-04** Time is deterministic: the timed loop and the retry backoff run under
  `testing/synctest` with the injected `Now`/`After` seams (REQ-SC-07), and the acceptance
  suite drives `PollNow` (REQ-SC-09) with an immediate fake backoff clock to assert
  retries, the retained cache and readiness transitions without real sleeps.
  → `scheduler/scheduler_test.go`, `features/steps_test.go`
- **REQ-TS-05** Go static analysis: `golangci-lint` pinned to `v2.13.2`, installed into
  `./bin` on first use, configured `version: "2"`, `run.go: "1.27"`, the `standard` linter
  set and the `gofmt` formatter; `go vet ./...` as its own target. → `.golangci.yml`,
  `Makefile`
- **REQ-TS-06** The Makefile is the single source of truth for the Go dev loop —
  `build`, `vet`, `lint`, `test`, `test-e2e`, `run` — so CI and a developer's laptop run
  identical commands against identically pinned tools. → `Makefile`
- **REQ-TS-07** The Python sidecar carries its own suite: `pytest` over
  `sidecar/tests/test_api.py`, `test_transport.py` and `test_config.py` (env parsing,
  duration grammar, redaction, aggregated `ConfigError` — REQ-SD-08…13), plus
  `ruff check` and `ruff format --check` against `sidecar/ruff.toml`. Both tools are pinned in
  `sidecar/requirements-dev.txt` (`pytest==8.3.4`, `ruff==0.9.2`) and driven by the
  `sidecar-install` / `sidecar-run` / `sidecar-test` / `sidecar-lint` targets, mirroring
  REQ-TS-06 for the sidecar. → `sidecar/tests/`, `sidecar/ruff.toml`,
  `sidecar/requirements-dev.txt`, `Makefile`

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
- **REQ-DP-06** Image build & publish. The **PR gate** (`ci.yml`, job `docker-build`)
  builds **both** images build-only — `push: false`, native `linux/amd64`, scoped gha
  caches — so a broken Dockerfile fails before merge without paying for emulated arm64.
  A **release-please release** (`release.yml`, job `image`, gated on
  `release_created == 'true'`) logs in to ghcr with the workflow `GITHUB_TOKEN`
  (`packages: write`) and **pushes** both images multi-arch
  (`linux/amd64,linux/arm64`) as `ghcr.io/gsdevme/solis-inverter-manager` and
  `ghcr.io/gsdevme/solis-inverter-manager-sidecar`, each tagged with the release tag
  (`vX.Y.Z`) **and** `latest`. Container publishing was **approved by the owner as of
  `v2.0.0`**; branch pushes and pull requests never publish.
  → `.github/workflows/{ci,release}.yml`, `release-please-config.json`,
  `.release-please-manifest.json`, `08-deployment.md`
- **REQ-DP-07** Kubernetes manifests are **not** carried in this repo; cluster
  deployment is managed via GitOps (Helm/Flux) in a separate infrastructure repo.
  `08-deployment.md` documents the intended manifest shape as reference examples.
- **REQ-DP-08** Module path `github.com/gsdevme/solis-inverter-manager`, `go 1.27`
  (toolchain `go 1.27.0`). → `go.mod`
- **REQ-DP-09** Local three-service dev stack: `docker-compose.yml` runs
  `eclipse-mosquitto:2` (config `deploy/mosquitto/mosquitto.conf` — anonymous, no
  persistence, **dev only, never a production broker**), the sidecar image and the manager
  image on one compose network, both application services reading the same `.env`.
  Compose overrides `SIDECAR_URL=http://sidecar:8081` and `MQTT_BROKER_URL=mqtt://mqtt:1883`
  over the loopback defaults that suit a bare `go run`, publishes `1883`/`8081`/`8080` to
  the host, and `depends_on`-orders the manager behind the broker and sidecar. With
  `.env.dist`'s `MODE=mock` the whole stack runs end to end with no hardware
  (`cp .env.dist .env && docker compose up`); a live run only needs `MODE=live` plus
  `INVERTER_IP`/`INVERTER_SERIAL`, since only the sidecar opens the `:8899` socket
  (REQ-DP-01). → `docker-compose.yml`, `deploy/mosquitto/mosquitto.conf`, `08-deployment.md`
- **REQ-DP-10** `checks.yml` is the **single reusable quality gate** (`workflow_call`,
  workflow-level `permissions: {}` with `contents: read` granted per job), called by both
  the PR pipeline (`ci.yml`) and the release pipeline (`release.yml`), so the same gate
  that guards a merge also guards a release. Five jobs, mirroring the same lint-vs-test
  split in each language so a formatting failure is distinguishable from a behavioural
  one at a glance: `lint` (`make vet` then `make lint`), `test`, `e2e`
  (REQ-TS-05/01/03), `sidecar-lint` and `sidecar-test` (REQ-TS-07). Every job invokes a
  Makefile target, so CI and a laptop use identical pinned tool versions; the Python
  jobs pin `3.12` to match the sidecar's runtime image and ruff's `target-version`, and
  cache pip on **both** sidecar requirements files (the runtime pin lives in
  `requirements.txt`, which `requirements-dev.txt` includes).
  → `.github/workflows/checks.yml`, `Makefile`
