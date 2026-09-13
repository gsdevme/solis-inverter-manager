# Solis Inverter Manager

A Go orchestrator + thin Python sidecar that reads a **Solis RHI-3.6K-48ES-5G**
hybrid inverter over a **Solarman V5** datalogger (TCP `:8899`) and republishes
telemetry to **MQTT** with **Home Assistant autodiscovery**, plus native HA controls
(charge/discharge amps, work mode, RTC sync) written back to the inverter behind a
read-before-write flash-wear guard.

## Architecture

Two containers, one pod:

- **manager** (Go) — owns all register semantics. Polls on an interval, decodes,
  publishes HA discovery + a single retained state document + availability, and
  handles inbound HA command topics. Exposes `/healthz` and `/readyz` on `:8080`.
- **sidecar** (Python) — a dumb Solarman V5 transport. Holds one persistent socket
  with a single lock, exposes generic register read/write RPCs over localhost
  `:8081`. It has no inverter knowledge and never talks to MQTT.

The manager reaches the sidecar over `SIDECAR_URL`; only the sidecar opens the
datalogger socket. See [`docs/specs/`](docs/specs/) for the full specification
(source of truth) and [`docs/plans/rebuild.md`](docs/plans/rebuild.md) for the
architecture and phase roadmap.

## Run it locally

The full stack runs with no inverter hardware — `MODE=mock` serves canned Phase 0
fixtures end to end.

```sh
cp .env.dist .env                 # ships MODE=mock; edit for a live run
docker compose up                 # mqtt + sidecar(mock) + manager

# watch discovery + retained state land on the broker
docker compose exec mqtt mosquitto_sub -t '#' -v

# health / readiness
curl -s localhost:8080/healthz    # 200 ok
curl -s localhost:8080/readyz     # 200 ready after the first successful poll
```

For a **live** run, set `MODE=live` plus `INVERTER_IP` / `INVERTER_SERIAL` (the
**datalogger** serial — a ~10-digit decimal) and your MQTT values in `.env`. The
host must have a LAN route to the datalogger (`:8899`) and broker (`:1883`).

### Without containers

```sh
make build        # -> ./bin/solis-inverter-manager
make test         # unit/integration
make test-e2e     # godog acceptance suite
make lint         # golangci-lint (pinned)

MODE=mock HEALTH_ADDR=:8080 go run ./cmd serve
```

Configuration is a single env catalogue (`MODE`, inverter/sidecar, polling,
MQTT/HA, RTC-sync, health/logging) documented in
[`docs/specs/05-config.md`](docs/specs/05-config.md) and [`.env.dist`](.env.dist).
`INVERTER_SERIAL` and `MQTT_PASSWORD` are secrets and are redacted from logs.

## Images

Both build from the repo root:

```sh
docker build -t solis-manager .                          # Go manager (distroless nonroot)
docker build -f sidecar/Dockerfile -t solis-sidecar .    # Python sidecar
```

Per the project guardrail, **no image is published until the owner approves** — CI
builds both images build-only.

## Deployment

Kubernetes manifests are **not** carried in this repo; cluster deployment is managed
via GitOps (Helm/Flux) in a separate infrastructure repo. The intended manifest
shape — two-container pod, ConfigMap/Secret split, probe wiring, security context,
termination grace — is documented as reference examples in
[`docs/specs/08-deployment.md`](docs/specs/08-deployment.md).
