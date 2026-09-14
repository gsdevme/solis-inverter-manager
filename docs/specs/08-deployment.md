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

  Runs as the numeric non-root user `65532:65532` (no passwd entry is needed) so
  the pod's `runAsNonRoot` check passes.

  ```sh
  docker build -f sidecar/Dockerfile -t solis-sidecar .
  ```

**Published images.** Publishing was approved by the owner as of `v2.0.0`. Each
release pushes both images to ghcr, tagged with the release tag (`vX.Y.Z`) and
`latest`:

| Image | Built from |
|---|---|
| `ghcr.io/gsdevme/solis-inverter-manager` | `Dockerfile` |
| `ghcr.io/gsdevme/solis-inverter-manager-sidecar` | `sidecar/Dockerfile` |

Both packages are **public** on ghcr so the cluster pulls them without an
`imagePullSecret` (set once on the package page after the first push). Pin the
release tag in the GitOps manifest, never `latest`.

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
  `MQTT_CLIENT_ID`, `MQTT_TOPIC_PREFIX`, `HA_DISCOVERY_PREFIX`,
  `HA_OBJECT_ID_PREFIX`, `HEALTH_ADDR`, `LOG_LEVEL`, `LOG_FORMAT`.
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
  the Release PR and cuts tags → an `image` job (gated on `release_created`) logs
  in to ghcr with the workflow `GITHUB_TOKEN` (`packages: write`) and builds and
  **pushes** both images multi-arch (`linux/amd64,linux/arm64`) under the tags
  listed in [Images](#images). Only a release-please release publishes; branch
  pushes and PRs never do.

## Migrating from the legacy Python publisher

The legacy `cli/publish.py` monolith (removed in Phase 3) published a different,
smaller entity set under a different device. Nothing is shared with the Go
manager — different discovery topics, different device identifier, different
state topics — so the two never collide, but the legacy entities do **not** clean
themselves up: their discovery configs are retained, so Home Assistant keeps
re-creating them from the broker forever after the old publisher is gone.

### 1. Entity id mapping

With the default `HA_OBJECT_ID_PREFIX=solis_inverter`:

| Legacy entity | New entity |
| --- | --- |
| `sensor.solis_inverter_battery` | `sensor.solis_inverter_battery_soc` |
| `sensor.solis_inverter_pv` | `sensor.solis_inverter_pv_total_power` |
| `sensor.solis_inverter_power_to_battery` | `sensor.solis_inverter_battery_charge_power` |
| `sensor.solis_inverter_power_from_battery` | `sensor.solis_inverter_battery_discharge_power` |
| `sensor.solis_inverter_charge_amps` | `number.solis_inverter_set_charge_current` |
| `sensor.solis_inverter_discharge_amps` | `number.solis_inverter_set_discharge_current` |
| `binary_sensor.solis_inverter_optimal_income` + the `switch.grid_charge_switch` YAML template on top of it | `select.solis_inverter_optimal_income` (`Run` / `Stop`) |
| `binary_sensor.solis_inverter_poller` | no entity — the availability topic `solis/<serial>/availability` (`online`/`offline`) drives every entity's availability instead |

Note the **domain changes** for the two amp setpoints (`sensor` → `number`) and
for optimal income (`binary_sensor`/`switch` → `select`): anything referencing
them by entity id — automations, scripts, template sensors, dashboards — must be
updated, and a `switch.turn_on` service call becomes
`select.select_option` with `Run` or `Stop`.

Command topics move too:

| Legacy command topic | New command topic |
| --- | --- |
| `solar_inverter_manager/set_charge` | `solis/<serial>/set_charge_current/set` |
| `solar_inverter_manager/set_discharge` | `solis/<serial>/set_discharge_current/set` |
| `solar_inverter_manager/set_optimal_income` (`1`/`true`) | `solis/<serial>/optimal_income/set` (`Run`/`Stop`) |

`<serial>` is `INVERTER_SERIAL`, the datalogger serial, and `solis` is
`MQTT_TOPIC_PREFIX`. The new controls are native discovery entities, so nothing
should need to publish to these topics by hand.

### 2. Clear the legacy retained topics — BEFORE starting the new manager

Do this while **both** publishers are stopped. Publishing an empty retained
payload (`-r -n`) to a discovery topic is how Home Assistant is told to delete
the entity; the same empty-retained trick clears the orphaned state/attribute
topics so they stop showing up in MQTT explorers.

The legacy publisher used `ha_mqtt_discoverable`, which slugifies the device name
to `Solis-Inverter` and each entity name to `Solis-Inverter-<Name>`, putting
discovery under `homeassistant/` and state under `hmd/`. Eight entities, six
`sensor` and two `binary_sensor`:

```sh
BROKER=mqtt.example        # -h; add -u/-P if the broker needs auth

for name in Power-From-Battery Power-To-Battery Battery Charge-Amps Discharge-Amps PV; do
  mosquitto_pub -h "$BROKER" -r -n -t "homeassistant/sensor/Solis-Inverter/Solis-Inverter-$name/config"
  mosquitto_pub -h "$BROKER" -r -n -t "hmd/sensor/Solis-Inverter/Solis-Inverter-$name/state"
  mosquitto_pub -h "$BROKER" -r -n -t "hmd/sensor/Solis-Inverter/Solis-Inverter-$name/attributes"
done

for name in Poller optimal_income; do
  mosquitto_pub -h "$BROKER" -r -n -t "homeassistant/binary_sensor/Solis-Inverter/Solis-Inverter-$name/config"
  mosquitto_pub -h "$BROKER" -r -n -t "hmd/binary_sensor/Solis-Inverter/Solis-Inverter-$name/state"
  mosquitto_pub -h "$BROKER" -r -n -t "hmd/binary_sensor/Solis-Inverter/Solis-Inverter-$name/attributes"
done
```

Also retire the three legacy command topics if anything ever published to them
retained:

```sh
for t in set_charge set_discharge set_optimal_income; do
  mosquitto_pub -h "$BROKER" -r -n -t "solar_inverter_manager/$t"
done
```

**The old device disappears on its own.** The legacy device identifier was
`solis_inverter_<serial>`; the new one is `<serial>` (see *Device block* in
[`03-mqtt-ha-discovery.md`](03-mqtt-ha-discovery.md)), so they are two distinct
devices in the registry. Home Assistant removes a device once its last entity is
gone, so clearing the eight discovery configs above is enough — do not delete the
new device by hand while tidying up. Any legacy entity that survives (one Home
Assistant had customised, or a stale registry row) can be deleted from
**Settings → Devices & services → MQTT** once its retained config is cleared.

Only then start the new manager; it publishes its own discovery, availability
`online` and first state, and the new entities appear under the `Solis Inverter`
device.

### 3. Post-cutover UI checks

- **Energy dashboard** (*Settings → Dashboards → Energy*): re-point the battery
  in/out sources at the Riemann-sum integration sensors built on
  `sensor.solis_inverter_battery_charge_power` /
  `…_battery_discharge_power`, and the solar production source at the one built
  on `sensor.solis_inverter_pv_total_power`. The dashboard silently keeps a
  reference to a now-missing entity, so check it explicitly rather than assuming.
- **Utility meters** (*Settings → Devices & services → Helpers*): each utility
  meter and Riemann-sum helper carries its source entity id — update every one
  that named a legacy id from the table above. A helper pointing at a deleted
  entity reports `unavailable` rather than erroring, so it is easy to miss.
- **`input_boolean.is_cheap_rate`**: confirm the automation windows that flip it
  still match `TOU_WINDOW` (default `23:30-05:30`). The manager now asserts that
  window into timed slots 1–2 on every poll (REQ-HA-17), so an automation on a
  different schedule will disagree with the inverter — `sensor.solis_inverter_tou_window`
  reports the window the inverter is actually running.
- **Boost**: the legacy setup had no equivalent; `select.solis_inverter_boost_select`
  plus `sensor.solis_inverter_boost` / `…_boost_ends_at` replace any manual
  slot-3 fiddling.

## Reference manifests

Example only — the canonical source is the GitOps/Helm chart in the infrastructure
repo. Pin the image refs to a release tag; `v2.0.0` below is the first published
release.

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
  HA_OBJECT_ID_PREFIX: "solis_inverter"
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
          image: ghcr.io/gsdevme/solis-inverter-manager:v2.0.0
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
          image: ghcr.io/gsdevme/solis-inverter-manager-sidecar:v2.0.0
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
