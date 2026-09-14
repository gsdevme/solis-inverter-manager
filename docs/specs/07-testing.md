# 07 — Testing

> **Status: skeleton.** Full content authored in a later phase.

## Unit tests (TODO)

TODO: `config` (defaults, validation, redaction), `server` (probes, readiness),
`inverter` (register decode/encode against `docs/phase0/fixtures/`), `homeassistant`
(discovery/state payloads), `publisher` (recording fake), `scheduler`
(`testing/synctest`), `sidecarclient`.

## Mock sidecar (`internal/mock`) (TODO)

TODO: an in-process stand-in for the Python sidecar serving canned register
snapshots (seeded from `docs/phase0/fixtures/`) over the real sidecar contract,
accepting guarded writes. Shared by the godog suite and a standalone mock command.

## Acceptance suite (`features/`) (TODO)

The godog harness runs under `go test ./features/...` (and `make test-e2e`). The
suite is `health`, `mqtt_discovery`, `mqtt_controls`, `polling`, `schedule` and
`schedule_controls` (one `.feature` file each, sharing `features/steps_test.go`).

TODO: a scenario for graceful shutdown publishing `offline`.

## Lint (TODO)

`golangci-lint` (default linters + gofmt) via `make lint`; `go vet` via `make vet`.
