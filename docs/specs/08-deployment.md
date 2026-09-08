# 08 — Deployment

> **Status: skeleton.** Full content authored in a later phase.

## Two-container pod (TODO)

TODO: deploy the Go **manager** and the thin Python **sidecar** as a
**two-container pod**. The manager reaches the sidecar over localhost
(`SIDECAR_URL`); only the sidecar opens the TCP socket to the datalogger (port
8899). Images, resource limits, non-root, and the readiness/liveness probe wiring
for both containers are specified here later.

## Manager image (TODO)

TODO: multi-stage Dockerfile, static `CGO_ENABLED=0`, distroless nonroot,
`EXPOSE 8080`, default `CMD serve`, default env `MODE=live`. (No Dockerfiles are
created in the scaffold phase.)

## Sidecar image (TODO)

TODO: minimal Python image with `pysolarmanv5`, the localhost HTTP server, no MQTT.

## CI/CD (TODO)

TODO: PR gate running `make vet`/`lint`/`test`/`test-e2e`; release-please + a
multi-arch image push on merge.
