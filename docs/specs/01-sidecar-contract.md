# 01 — Sidecar contract

> **Status: skeleton.** Full content authored in a later phase.

The Go manager (`internal/sidecarclient`) talks to the thin Python sidecar over
**localhost HTTP** (`SIDECAR_URL`). The sidecar owns the Solarman-V5/Modbus
transport to the datalogger (port 8899) and exposes only raw register access — no
decode, no MQTT, no HA.

## Endpoints (TODO)

- TODO: `GET /health` — sidecar liveness/inverter-reachability (consumed by `/readyz`).
- TODO: read input registers (fc04) — request a block, return raw words.
- TODO: read holding registers (fc03) — request a block, return raw words.
- TODO: write holding register (fc06) — single-register write, used only by the
  guarded write path (see `03-mqtt-ha-discovery.md`).

## Wire format (TODO)

TODO: JSON request/response shapes, register addressing, error envelope, timeouts,
retry/reconnect semantics (sidecar reconnects on error between calls).

## Error mapping (TODO)

TODO: transport errors vs illegal-address vs timeout, and how the manager maps them
to poll failures / retries.
