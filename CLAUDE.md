# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A spec-driven rebuild of `solis-inverter-manager`: a Go orchestrator + thin Python
sidecar that reads a **Solis RHI-3.6K-48ES-5G** hybrid inverter over a **Solarman
V5** datalogger (TCP `:8899`, `mb_slave_id=1`) and republishes telemetry to **MQTT**
with **Home Assistant autodiscovery**, plus native HA controls (charge/discharge
amps, work mode) written back to the inverter. Modelled on the sibling services
`gsdevme/hyundai-bluelink-mqtt` and `gsdevme/unifi-ha-presence-mqtt`.

The rebuild is **in progress**. See `docs/plans/rebuild.md` for the roadmap and
current status; live progress is tracked in GitHub **epic #25** (phase issues
#17–#24).

## Start here (read first)

1. `docs/plans/rebuild.md` — architecture, decisions, phase roadmap + status.
2. `docs/specs/*.md` — the **source of truth** for behaviour (authored per phase).
   `docs/specs/REQUIREMENTS.md` holds stable `REQ-*` IDs.
3. `docs/phase0/findings.md` — the **probe-confirmed register map** (scale, sign,
   endianness, the 43110 work-mode bitfield, write-path behaviour, RTC drift),
   with raw live captures in `docs/phase0/fixtures/` that drive the decode tests.

## Current status

- **Phase 0** (#17) ✅ — register map confirmed against the live inverter.
- **Phase 1** (#18) ✅ — Go scaffold: module `github.com/gsdevme/solis-inverter-manager`
  (go 1.27), cobra `serve`, `internal/config` (env catalog, `errors.Join`
  fail-fast, `MODE=mock|live`, slog with secret redaction), `internal/server`
  (`/healthz`, `/readyz`), Makefile, `.golangci.yml`, godog harness, `docs/specs`
  skeletons, `.env.dist`. The `internal/{mqtt,homeassistant,publisher,scheduler,
  mock,inverter,sidecarclient}` packages are doc-only stubs filled in by later
  phases.
- **Phase 2** (#19) ✅ — thin Python sidecar (`sidecar/`): `pysolarmanv5` transport
  (persistent socket, single lock, reconnect-on-error), REST-ish generic register
  RPCs, `MODE=mock` fixture server, pytest + Dockerfile; contract in
  `docs/specs/01-sidecar-contract.md`.
- **Phase 3** (#20) is next: the Go register map + decode, `internal/sidecarclient`,
  then delete the legacy Python monolith.

## Commands

```sh
make build        # -> ./bin/solis-inverter-manager
make vet          # go vet ./...
make test         # unit/integration (excludes the godog features suite)
make test-e2e     # godog acceptance suite (./features/...)
make lint         # golangci-lint (installs pinned binary into ./bin on first use)

gofmt -l .        # must be empty
go vet ./... && go build ./...
MODE=mock HEALTH_ADDR=:18080 go run ./cmd serve   # /healthz=200, /readyz=503 (not ready yet)
```

## Local run modes (MODE=live|mock)

`internal/config` resolves a single `MODE` switch. `MODE=mock` drops the
inverter/MQTT requirements so the pipeline runs against canned data with no
hardware; `MODE=live` requires the real inverter/sidecar and MQTT values.
`.env.dist` ships `MODE=mock` — `cp .env.dist .env` to start. The `MODE=mock`
fake sidecar itself lands in **Phase 2**.

## Conventions

- **stdlib-first**, mirroring the siblings. External deps kept minimal: cobra,
  autopaho (`paho.golang`, added in Phase 4), godotenv, godog (test-only).
- Structured logging via `log/slog`; `config` redacts secrets (`INVERTER_SERIAL`,
  `MQTT_PASSWORD`) — never log credentials.
- Spec-driven: update the relevant `docs/specs/*.md` and `REQ-*` entry in the same
  change as the behaviour.
- The Go manager owns **all** register semantics; the sidecar is a dumb transport.

## Guardrails (important)

- **Read-before-write:** never issue a Modbus write (fc06) unless a read shows the
  value differs — holding registers are flash-backed and needless writes wear
  flash. Applies to setpoints (43141/43142 amps, 43110 work mode) and RTC sync
  (43000–43005, only when drift exceeds a threshold).
- **Container image publishing is approved** as of `v2.0.0`: `release.yml` pushes the
  manager and sidecar images to ghcr on every release-please release. Branch pushes
  never publish.
- Trust no register unconfirmed — the Phase 0 fixtures are the ground truth.

## Live inverter access (Phase 0 lessons)

- The host needs a network route to the LAN; a sandbox times out on `:8899` /
  `:1883`. Verify reachability before live work.
- `INVERTER_SERIAL` must be the **datalogger (Solarman WiFi-stick) serial** — a
  ~10-digit decimal number — not the inverter serial (alphanumeric; also readable
  as ASCII at input registers 33004–33011).
- `.env` is gitignored and holds real secrets locally only.
