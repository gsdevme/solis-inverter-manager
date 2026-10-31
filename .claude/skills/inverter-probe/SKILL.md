---
name: inverter-probe
description: Use when a register question has to be answered against the live Solis inverter — confirming an UNVERIFIED register, checking the inverter decodes sanely, or capturing a fixture — using the read-only `probe` subcommand over the sidecar, with no MQTT or Home Assistant. Source of truth is docs/specs/11-probe.md.
---

# Probing the live inverter

The manager binary has a read-only register workbench: `solis-inverter-manager
probe <verb>`. It talks to the sidecar only and reuses the manager's decoders, so
what it prints is what the manager would publish. **It never writes** — writes
stay on the guarded command path (`REQ-HA-10`). Spec: `docs/specs/11-probe.md`.

## Preconditions

- The host needs a **network route to the inverter's LAN** (`:8899`). A sandbox
  or cloud session times out; say so and hand the commands to the owner instead
  of guessing results.
- `INVERTER_SERIAL` is the **datalogger (Solarman stick) serial**, a ~10-digit
  decimal, not the alphanumeric inverter serial. It is a secret: never paste it
  into logs, docs or commits.
- Reads wider than ~100 registers NAK and concurrent reads time out
  (`findings.md` ambiguity #10). The probe chunks and serialises for you; do not
  run two probes at once against one datalogger.

## Run it

```sh
# 1. a live sidecar. The Python sidecar reads the process environment only, NOT
#    .env (only the Go binary loads .env), so export it first.
set -a; . ./.env; set +a
MODE=live sidecar/.venv/bin/python -m sidecar   # or: make sidecar-run (canned fixtures)

# 2. in another shell (SIDECAR_URL or --sidecar picks the sidecar; default :8081)
make build
./bin/solis-inverter-manager probe decode                 # reachability + full decode
./bin/solis-inverter-manager probe read-holding 43110 1   # raw words
./bin/solis-inverter-manager probe read-input 33139
./bin/solis-inverter-manager probe capture \
  --out docs/phase0/fixtures/<topic>-$(date +%F).json \
  --holding 43110:1
```

## Confirming a register (the only way one stops being UNVERIFIED)

1. `probe decode` first. If it fails, fix reachability; nothing below is a
   register problem.
2. Read the candidates raw (`read-holding` / `read-input`) and write the values
   down with a timestamp.
3. Change **one** setting on the inverter LCD or in the Solis app, then repeat
   the same reads. The register that followed the setting in the before/after
   pair is the one; for a bitfield, compare the binary words to find the bit.
4. `probe capture` the registers into `docs/phase0/fixtures/` — that directory
   holds **live captures only**, never synthetic data.
5. Record the raw words and the conclusion in `docs/phase0/findings.md`, promote
   the row in `docs/specs/02-register-map.md` from UNVERIFIED to confirmed, and
   add a fixture-driven decode test. Only then may a write behind that register
   be documented as safe to enable.

A register the probe could not confirm **stays** UNVERIFIED, read-only or behind
a flag defaulting to off. Do not promote on documentary evidence alone.
