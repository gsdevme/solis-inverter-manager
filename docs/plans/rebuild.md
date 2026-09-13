# Rebuild plan — Go orchestrator + Python sidecar (spec-driven)

Load-bearing roadmap for rebuilding `solis-inverter-manager` from the legacy 2023
Python app into a Go manager + thin Python sidecar. This document is the durable
plan of record; live progress is tracked on GitHub (**epic #25**, phase issues
#17–#24). The detailed source of truth for behaviour is `docs/specs/`; the
probe-confirmed register map is `docs/phase0/findings.md`.

## Why

The legacy app works but is dated: a hardcoded poll loop, no proper HA
availability/LWT, setpoints via raw MQTT topics + HA automations (no native
controls), a committed `.env` with real secrets, and stale docs. The Modbus link
(Solis RHI-3.6K-48ES-5G via a Solarman V5 datalogger, TCP port 8899,
`mb_slave_id=1`) is genuinely laggy and must not be hammered — the polling model
is conservative and resilient rather than fast.

## Architecture

Single k8s pod (single replica), two containers talking over localhost:

```
Pod
├── manager (Go, distroless)  ── owns everything
│     • autopaho MQTT: LWT + retained state + HA autodiscovery
│     • register map + decode/scale/sign  (from docs/phase0/findings.md)
│     • serialized slow poll, backoff, retained last-good cache, readiness
│     • /healthz + /readyz (also probes the sidecar)
│     • HTTP client → sidecar on 127.0.0.1
└── sidecar (Python, thin)   ── dumb Solarman V5 transport
      • pysolarmanv5 (sync), single persistent socket, one lock (serialized)
      • REST-ish HTTP/JSON: POST read_input / read_holding / write_holding
        (raw uint16) + GET /health — see docs/specs/01-sidecar-contract.md
      • reconnect-on-error between calls
      • MODE=mock → in-repo fake register server (seeded from Phase 0 fixtures)
```

Data flow per tick: Go scheduler → sidecar RPC (raw registers) → Go decode →
publish retained state JSON + refresh availability → HA reads via value_templates.
Writes: HA command topic → Go validates → **read-before-write guard** → sidecar
`write_holding` → re-read to confirm.

## Decisions locked (with the owner)

- Go orchestrator + thin Python sidecar; two containers, one pod, localhost.
- Sidecar is dumb transport only: generic register RPCs. **Go owns the register
  map, decoding, scaling, sign conventions, and all HA semantics.**
- Polling: single persistent connection, one in-flight request (mutex), ~60s
  tick, exponential backoff, retained last-good state so HA never blanks;
  readiness flips after N consecutive failures.
- HA controls: charge amps & discharge amps as `number` (0–60 A), optimal income
  as `switch`/`select`, via command topics, validated server-side.
- **Read-before-write guard:** never issue a Modbus write unless a read shows the
  value differs — holding registers are flash-backed and needless writes wear
  flash. Applies to all setpoints (43141/43142 amps, 43110 work mode) and any RTC
  auto-sync (only write 43000–43005 when drift exceeds a threshold).
- Rebuild in place, preserve git history. No `git push` beyond the feature branch
  and **no container image publish** until the owner says so.
- Modelled on the sibling services `gsdevme/hyundai-bluelink-mqtt` and
  `gsdevme/unifi-ha-presence-mqtt` (go 1.27, stdlib-first, autopaho, numbered
  `docs/specs`, godog BDD, distroless, `/healthz`+`/readyz`, HA autodiscovery with
  retained state + availability/LWT).

## The register map

The legacy map was a **hypothesis**; every public Solis map is reverse-engineered.
Phase 0 confirmed it against the live inverter and captured raw fixtures. The
confirmed map, decode/scale/sign rules, the 43110 work-mode bitfield (33/35), the
grid-power S32 fix, the write-path result (no ~120s revert), and RTC drift +
settability (no timezone register) are in **`docs/phase0/findings.md`**, with raw
captures in `docs/phase0/fixtures/`. Those fixtures drive the Go decode unit tests
(Phase 3). `docs/specs/02-register-map.md` is the Go-facing spec derived from them.

## Phases & status

TDD throughout; godog `.feature` coverage where behaviour is observable.

| Phase | Issue | Status | Summary |
|---|---|---|---|
| 0 — Investigation & register confirmation | #17 | ✅ done (`6eb7cf9`) | Probe-confirmed map + fixtures in `docs/phase0/` |
| 1 — Scaffold | #18 | ✅ done (`fb95c1e`) | Go module (go 1.27), cobra serve, config+slog, health, Makefile, golangci, godog harness, specs skeleton, `.env.dist`, secrets hygiene |
| 2 — Thin Python sidecar | #19 | ✅ done | `sidecar/` `pysolarmanv5` transport, persistent socket, single lock, reconnect-on-error, REST-ish generic RPCs, `MODE=mock` fixture server, own requirements + Dockerfile + pytest; contract in `docs/specs/01-sidecar-contract.md`; `MODE=live` fc04/fc06 smoke verified against the real inverter |
| 3 — Register map + decode (Go) | #20 | ✅ done | `internal/inverter` constants + decoders (fixture-tested against `docs/phase0/fixtures/`), `internal/sidecarclient` typed transport client; legacy Python monolith deleted |
| 4 — HA discovery + state (read-only) | #21 | ✅ done | autopaho, LWT+availability, device + read-only entities, retained state JSON, reconnect re-publish |
| 5 — Writable controls | #22 | ✅ done | `number` (amps 0–60), `switch`/`select` (work mode 33/35), RTC sync; read-before-write + re-read confirm |
| 6 — Scheduler | #23 | ✅ done | serialized ~60s poll, backoff, retained cache, failure-threshold readiness, injectable clock, opt-in threshold-gated RTC auto-sync |
| 7 — Deployment | #24 | ✅ done | manager `Dockerfile` (distroless nonroot) + sidecar image; full `docker-compose` stack (mqtt + sidecar + manager); CI `docker-build` (build-only) + release-please/release.yml (multi-arch, build-only pending approval); two-container pod, ConfigMap/Secret, probes documented as reference examples in `08-deployment.md` (manifests live in a separate GitOps/Helm/Flux repo) |

Exit criteria for each phase are on its GitHub issue.

## Config (env var catalog)

`MODE` (`mock|live`), `INVERTER_IP`, `INVERTER_SERIAL` (datalogger/WiFi-stick
serial — numeric), `INVERTER_PORT` (8899), `INVERTER_SOCKET_TIMEOUT`,
`SIDECAR_URL` (127.0.0.1), `MQTT_BROKER_URL`, `MQTT_USERNAME`, `MQTT_PASSWORD`,
`MQTT_CLIENT_ID`, `MQTT_TOPIC_PREFIX`, `HA_DISCOVERY_PREFIX` (default
`homeassistant`), `POLL_INTERVAL` (default 60s), `POLL_MAX_RETRIES`,
`FAILURE_THRESHOLD`, `CONTROLS_ENABLED` (default true), `RTC_SYNC_ENABLED`
(default false), `RTC_DRIFT_THRESHOLD` (default 60s), `HEALTH_ADDR` (`:8080`),
`LOG_LEVEL`, `LOG_FORMAT`. See `docs/specs/05-config.md` and `.env.dist`.

## Verification

- **Unit/decode:** `make test` — decoders verified against the real captured
  register fixtures.
- **BDD/e2e:** `make test-e2e` (godog) with `MODE=mock` sidecar — discovery
  payloads, availability transitions, command-topic writes, backoff/readiness.
- **Live smoke:** both containers locally (compose) against the real inverter + a
  test MQTT broker; HA autodiscovery, sensors populate, entities go `unavailable`
  on LWT, controls write back and reflect on the next poll.
- **Deploy:** manifests are not carried here — cluster deployment is GitOps
  (Helm/Flux) in a separate infra repo; `docs/specs/08-deployment.md` documents the
  intended manifest shape as reference examples. Local end-to-end runs via
  `docker compose up`.

## Guardrails

- No `git push` beyond the `rebuild-go-sidecar` branch; **no image publish** until
  approved.
- One atomic commit per change (Conventional Commits).
- Trust no register unconfirmed — cross-reference + probe + fixtures (done in
  Phase 0) before relying on it.

## Taking over (another agent / laptop)

1. Read, in order: this file → `docs/specs/` (00 overview first) →
   `docs/phase0/findings.md`.
2. Check GitHub **epic #25** for live status; the next open phase issue is the
   task at hand.
3. Dev loop is offline-first: `MODE=mock` needs no hardware. `make build`,
   `make test`, `make test-e2e`.
4. **Live work prerequisites** (Phase 0 lessons):
   - The host must have a network route to the LAN — a sandbox times out on the
     inverter (`:8899`) and MQTT (`:1883`). Verify reachability first.
   - `INVERTER_SERIAL` must be the **datalogger (Solarman WiFi-stick) serial** — a
     ~10-digit decimal number — **not** the inverter serial (which is alphanumeric
     and also readable as ASCII at input registers 33004–33011).
   - `.env` is gitignored and holds real secrets locally only; copy `.env.dist`.
