# 11 — Register probe (`probe` subcommand)

A **read-only register workbench** built into the manager binary, for answering
register questions against the live inverter without MQTT, Home Assistant or the
scheduler. It exists because every probe so far was throwaway (`findings.md`:
"The probe harness itself was throwaway"), and confirming an UNVERIFIED register
means correlating an LCD change with a before/after read, which needs a repeatable
tool rather than another one-off script. `REQ-PR-01` to `REQ-PR-03` in
`REQUIREMENTS.md` carry the stable IDs; the companion skill is
`.claude/skills/inverter-probe/SKILL.md`.

## Shape

- Lives in `internal/cmd/probe.go` as `solis-inverter-manager probe <verb>`,
  beside `serve`, so it reuses `internal/sidecarclient` and every decoder in
  `internal/inverter`, `internal/publisher` and `internal/controls`. There is no
  second register model and no Python: the sidecar stays a dumb transport.
- Talks to the **sidecar only**. `--sidecar` (then `SIDECAR_URL`, then
  `http://127.0.0.1:8081`) names it; `--timeout` (default `15s`) bounds each
  request. Start a live sidecar with `set -a; . ./.env; set +a; MODE=live python -m
  sidecar` — the sidecar reads the process environment only, never `.env`; only the
  Go binary loads `.env` — or a canned one with `make sidecar-run`.
- **Never writes.** There is deliberately no write verb: writes stay on the
  guarded command path (`REQ-HA-10`), and the probe's job is to decide what a
  register means *before* anything is written to it.
- Reads wider than **100 registers are split into sequential chunks of ≤ 100**,
  because the live datalogger NAKs wider reads and times out on concurrent ones
  (`findings.md` ambiguity #10).

## Verbs

| Verb | Does | Output |
|---|---|---|
| `read-input <addr> [count]` / `read-holding <addr> [count]` | Dumps raw words (fc04 / fc03) | one row per register: address, `u16`, hex, `s16` |
| `decode` | Runs one **real poll** — `publisher.Collect` (two input blocks) and `controls.ReadSetpoints` (one holding block) — through `homeassistant.BuildState` | the exact retained state document Home Assistant would receive, pretty-printed; the "is the inverter reachable and does it decode sanely" check |
| `capture --out <file> --input addr:count... --holding addr:count...` | Reads each span (chunked on the wire, one block in the file) and writes it in the **`docs/phase0/fixtures` shape** (two-space indented, like the committed fixtures) to a **new** file — an existing `--out` is refused, never overwritten, so a before/after pair cannot destroy its "before" half — `{connection, captured_at, via, blocks[{kind, addr, count, regs, by_addr}]}` | a file the decode tests (`inverter/fixture_test.go`) load and the sidecar mock (`MOCK_FIXTURE`) seeds from, unchanged |

Spans are `addr:count`; a bare `addr` is one register. Addresses and counts are
bounded to the Modbus range at parse time.

## Workflow this supports

1. **Reachability:** `probe decode`. A failure here is a network/sidecar problem,
   not a register one.
2. **Look:** `probe read-holding <addr> [count]` / `probe read-input ...` for raw
   words around a question, noted down with a timestamp.
3. **Correlate with the LCD/app:** change one setting, then repeat the same
   `read-*` and compare the before/after pair: the register (and, for a bitfield,
   the bit) that followed the setting is the answer.
4. **Pin it:** `probe capture --out docs/phase0/fixtures/<topic>-<date>.json ...`
   so the words become a test fixture, then record the result in
   `docs/phase0/findings.md` and promote the register row in
   `02-register-map.md` from UNVERIFIED to confirmed.

An unconfirmed register goes into the specs **marked UNVERIFIED and behind a
feature flag defaulting to off**, or is not modelled at all; the probe is how it
stops being unconfirmed.

## Testing

`internal/cmd/probe_test.go` drives the prober against the in-process `fakeBus`
with the clock injected: span parsing bounds; a 250-register span becomes three
sequential reads of 100/100/50 reassembled in order; `dump` prints one row per
register; `decode` issues exactly the manager's three frames (`33022+100`,
`33122+93`, `43110+61`) and prints valid JSON carrying `battery_soc`; `capture`
writes a fixture that unmarshals into the fixture shape; the command tree carries
all four verbs under `probe`.
