# 08 — Deployment

This service is packaged as two container images and runs as a **two-container
pod**: the Go **manager** and the thin Python **sidecar**. The manager reaches the
sidecar over `localhost` (`SIDECAR_URL`); only the sidecar opens the TCP socket to
the Solarman datalogger (port `8899`). A single replica is correct — the inverter
has one datalogger and the sidecar holds a single persistent socket, so there is no
horizontal scaling story.

> **Where the manifests live.** This repository does **not** carry Kubernetes
> manifests. Cluster deployment is managed elsewhere via GitOps (Helm / Flux) in a
> dedicated infrastructure repo. The manifests below are **reference examples** that
> document the intended shape — probe wiring, the ConfigMap/Secret split, the
> security context, and the termination grace period — so the GitOps chart can be
> authored to match. Treat them as the contract, not as files to `kubectl apply`
> from here.

## Images

Two images, both built from the repo root:

- **Manager** — [`Dockerfile`](../../Dockerfile). Two-stage: `golang:1.27`
  (matches `go.mod` `go 1.27.0`) builds a static `CGO_ENABLED=0 GOOS=linux` binary
  (`-trimpath -ldflags "-s -w"`), shipped on `gcr.io/distroless/static:nonroot`.
  `ENV MODE=live`, `EXPOSE 8080`, `USER nonroot:nonroot`,
  `ENTRYPOINT ["/solis-inverter-manager"]`, `CMD ["serve"]`.

  ```sh
  docker build -t solis-manager .
  ```

- **Sidecar** — [`sidecar/Dockerfile`](../../sidecar/Dockerfile). `python:3.12-slim`
  with `pysolarmanv5`, the localhost HTTP transport (no MQTT), listening on `8081`.
  Built from the repo root so the Phase 0 fixtures are copied alongside for
  `MODE=mock`.

  ```sh
  docker build -f sidecar/Dockerfile -t solis-sidecar .
  ```

**Publishing is gated.** Per the project guardrail, **no image is pushed until the
owner approves.** CI builds both images build-only (see below); flipping to publish
is a one-line change in `release.yml` plus a ghcr login (documented in that file's
header).

## Probes

Each container is probed on its own health surface:

| Container | Liveness | Readiness | Notes |
|---|---|---|---|
| manager | `GET /healthz` :8080 | `GET /readyz` :8080 | Readiness is scheduler-driven (`REQ-LC-09`): ready after the first fully-successful poll, not-ready after `FAILURE_THRESHOLD` consecutive failures. An unreachable sidecar fails the poll, so `/readyz` reflects sidecar reachability transitively. |
| sidecar | `GET /health` :8081 | — | `REQ-SD-04`: `{ok, inverter_reachable, mode}`, never raises. Used for liveness; the manager's `/readyz` is the pod's aggregate readiness signal. |

`/readyz` — not the MQTT availability topic — is the authoritative readiness signal
for orchestration: the retained-`offline` Last Will has an intentional ~40s delay
(a flap debounce; see [`03-mqtt-ha-discovery.md`](03-mqtt-ha-discovery.md)), so it
lags a dead pod and must not drive scheduling decisions.

## Config split: ConfigMap + Secret

Non-secret env goes in a **ConfigMap**; the two secrets go in a **Secret**. Both are
mounted with `envFrom` so the manager sees the full env catalogue from
[`05-config.md`](05-config.md).

- **ConfigMap** — `MODE`, `INVERTER_IP`, `INVERTER_PORT`, `POLL_INTERVAL`,
  `POLL_MAX_RETRIES`, `FAILURE_THRESHOLD`, `CONTROLS_ENABLED`, `TOU_WINDOW`,
  `RTC_SYNC_ENABLED`, `RTC_DRIFT_THRESHOLD`, `MQTT_BROKER_URL`, `MQTT_USERNAME`,
  `MQTT_CLIENT_ID`, `MQTT_TOPIC_PREFIX`, `HA_DISCOVERY_PREFIX`, `HEALTH_ADDR`,
  `LOG_LEVEL`, `LOG_FORMAT`.
- **Secret** — `INVERTER_SERIAL` (datalogger serial) and `MQTT_PASSWORD`. These are
  the two values `config` redacts in logs; keep them out of the ConfigMap.

In the pod the manager talks to the sidecar over loopback, so
`SIDECAR_URL=http://127.0.0.1:8081` is set on the manager container directly (it is
topology, not tunable config).

## Termination

`terminationGracePeriodSeconds: 30` gives the manager's graceful shutdown room to
run: it drains the scheduler, publishes retained `offline`, and disconnects the
broker cleanly (which drains the in-flight reconnect-republish goroutine) before the
health server stops — all bounded by a 5s shutdown context (`REQ-LC-10`). 30s
comfortably covers that plus SIGTERM propagation to both containers.

## Security context

- **Both containers:** `runAsNonRoot: true`, `allowPrivilegeEscalation: false`,
  `capabilities.drop: [ALL]`, `seccompProfile.type: RuntimeDefault`.
- **Manager:** `readOnlyRootFilesystem: true` — the distroless static binary needs
  no writable filesystem.
- **Sidecar:** if it needs scratch space, mount an `emptyDir` at `/tmp` rather than
  relaxing the root filesystem.

## CI / release

- **PR gate** ([`ci.yml`](../../.github/workflows/ci.yml)): the `checks` reusable
  workflow (`make lint`/`test`/`test-e2e`) plus a `docker-build` job that builds
  **both** images build-only (`push: false`), native `linux/amd64`, with scoped gha
  caches. Catches Dockerfile breakage before merge.
- **Release** ([`release.yml`](../../.github/workflows/release.yml)): on merge to
  `master`, the same gate → `release-please` (`release-type: go`, driven by
  [`release-please-config.json`](../../release-please-config.json) +
  [`.release-please-manifest.json`](../../.release-please-manifest.json)) maintains
  the Release PR and cuts tags → an `image` job (gated on `release_created`) builds
  both images multi-arch (`linux/amd64,linux/arm64`). **Build-only (`push: false`)
  pending owner approval** — the header comment documents exactly what to flip to
  publish to ghcr.

## Reference manifests

Example only — the canonical source is the GitOps/Helm chart in the infrastructure
repo. Image refs are placeholders (nothing is published yet).

```yaml
# configmap.yaml — non-secret env (see 05-config.md for meanings)
apiVersion: v1
kind: ConfigMap
metadata:
  name: solis-inverter-manager
data:
  MODE: "live"
  INVERTER_IP: "192.168.1.50"
  INVERTER_PORT: "8899"
  POLL_INTERVAL: "60s"
  POLL_MAX_RETRIES: "3"
  FAILURE_THRESHOLD: "3"
  CONTROLS_ENABLED: "true"
  TOU_WINDOW: "23:30-05:30"
  RTC_SYNC_ENABLED: "false"
  RTC_DRIFT_THRESHOLD: "60s"
  MQTT_BROKER_URL: "mqtt://mqtt.example:1883"
  MQTT_USERNAME: "solis"
  MQTT_CLIENT_ID: "solis-inverter-manager"
  MQTT_TOPIC_PREFIX: "solis"
  HA_DISCOVERY_PREFIX: "homeassistant"
  HEALTH_ADDR: ":8080"
  LOG_LEVEL: "info"
  LOG_FORMAT: "json"
```

```yaml
# secret.yaml — TEMPLATE ONLY, never commit real secrets. In GitOps these come
# from a SealedSecret / External Secret, not plaintext.
apiVersion: v1
kind: Secret
metadata:
  name: solis-inverter-manager
type: Opaque
stringData:
  INVERTER_SERIAL: "REPLACE_WITH_DATALOGGER_SERIAL"
  MQTT_PASSWORD: "REPLACE_WITH_MQTT_PASSWORD"
```

```yaml
# deployment.yaml — two-container pod, single replica
apiVersion: apps/v1
kind: Deployment
metadata:
  name: solis-inverter-manager
  labels: { app: solis-inverter-manager }
spec:
  replicas: 1
  selector:
    matchLabels: { app: solis-inverter-manager }
  template:
    metadata:
      labels: { app: solis-inverter-manager }
    spec:
      terminationGracePeriodSeconds: 30
      securityContext:
        runAsNonRoot: true
        seccompProfile: { type: RuntimeDefault }
      containers:
        - name: manager
          image: ghcr.io/gsdevme/solis-inverter-manager:latest # placeholder — not yet published
          args: ["serve"]
          ports:
            - { name: health, containerPort: 8080 }
          env:
            - name: SIDECAR_URL
              value: http://127.0.0.1:8081 # loopback to the sidecar container
          envFrom:
            - configMapRef: { name: solis-inverter-manager }
            - secretRef: { name: solis-inverter-manager }
          livenessProbe:
            httpGet: { path: /healthz, port: health }
            periodSeconds: 10
          readinessProbe:
            httpGet: { path: /readyz, port: health }
            periodSeconds: 10
          resources:
            requests: { cpu: "10m", memory: "32Mi" }
            limits: { cpu: "250m", memory: "128Mi" }
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities: { drop: [ALL] }
        - name: sidecar
          image: ghcr.io/gsdevme/solis-sidecar:latest # placeholder — not yet published
          ports:
            - { name: sidecar, containerPort: 8081 }
          envFrom:
            - configMapRef: { name: solis-inverter-manager }
            - secretRef: { name: solis-inverter-manager }
          livenessProbe:
            httpGet: { path: /health, port: sidecar }
            periodSeconds: 10
          resources:
            requests: { cpu: "10m", memory: "32Mi" }
            limits: { cpu: "250m", memory: "128Mi" }
          securityContext:
            allowPrivilegeEscalation: false
            capabilities: { drop: [ALL] }
          volumeMounts:
            - { name: tmp, mountPath: /tmp }
      volumes:
        - name: tmp
          emptyDir: {}
```

```yaml
# service.yaml — ClusterIP so probes/scrapers can reach the health port
apiVersion: v1
kind: Service
metadata:
  name: solis-inverter-manager
spec:
  selector: { app: solis-inverter-manager }
  ports:
    - { name: health, port: 8080, targetPort: health }
```
