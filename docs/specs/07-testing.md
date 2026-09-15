# 07 — Testing

Three suites guard this repo, all driven from the `Makefile` so CI and a laptop run
identical commands against identically pinned tools (`REQ-TS-06`, `REQ-DP-10`):

| Suite | Command | What it proves |
|---|---|---|
| Go unit/integration | `make test` | Units behave, decoders match the live fixtures |
| Go acceptance (godog) | `make test-e2e` | Observable behaviour, wired end to end in-process |
| Python sidecar | `make sidecar-test` + `make sidecar-lint` | The transport contract holds |

Nothing in any suite needs an inverter, a datalogger, an MQTT broker or a network.

## Unit tests (`REQ-TS-01`)

29 `*_test.go` files, excluded from `./features/...` by `make test` so the fast loop
stays fast:

| Package | Files | Coverage |
|---|---|---|
| `internal/inverter` | 8 | Register decode/encode: block addressing, RTC, work-mode bitfield, amps, timed slots, live fixtures, the write probe |
| `internal/controls` | 6 | The read-before-write guard, command routing/validation, locking, the schedule reconcile, setpoint reads, the fake holding bank |
| `internal/homeassistant` | 3 | Discovery payload shape, the entity catalogue, the state document |
| `internal/cmd` | 3 | Setpoint freshness gating, the built state document, the serve seams: the reconnect republish order and re-subscribe (`REQ-HA-05`/`REQ-HA-11`), the single teardown path and its two exit branches (`REQ-LC-10`), the sidecar startup wait (`REQ-LC-11`), the setpoints-reuse state reader (`REQ-SC-03`) |
| `internal/publisher` | 2 | Collect (block split + decode), discovery/availability/state publishes |
| `internal/schedule` | 2 | ToU window parsing/joining, boost planning and option mapping |
| `internal/config` | 1 | Defaults, `errors.Join` validation, secret redaction |
| `internal/scheduler` | 1 | The timed loop, backoff, cache, readiness — under `testing/synctest` — plus command/poll mutual exclusion on `apiMu` (`REQ-SC-05`), which uses real time because a goroutine blocked on a mutex is not durably blocked in a synctest bubble |
| `internal/server` | 1 | Probes, the readiness counter, the status page |
| `internal/mqtt` | 1 | The reconnect-hook fire/drain path (`REQ-LC-14`) |
| `internal/sidecarclient` | 1 | The typed transport client and its error mapping |

## Fixture-driven decode (`REQ-TS-02`)

Decoder tests assert against the **live Phase 0 captures** in
`docs/phase0/fixtures/`, not hand-written expectations — the ground truth is the
inverter's own words, cross-checked for physical consistency (battery power =
current × voltage, the grid S32 against meter `33263`). The same captures are what
every `REQ-RM-*` traces to, so a decode change that contradicts the hardware fails
here rather than in production. `writeprobe_test.go` pins the confirmed write-path
behaviour (fc06 accepted, no ~120 s revert, `43024` acked-but-ignored).

## Acceptance suite (`features/`) (`REQ-TS-03`)

godog, run by `go test ./features/...` / `make test-e2e`. **30 scenarios** across six
feature files sharing one `features/steps_test.go`:

| Feature file | Scenarios | Subject |
|---|---|---|
| `health.feature` | 2 | `/healthz`, `/readyz` transitions |
| `mqtt_discovery.feature` | 4 | Retained discovery configs, availability `online`/`offline` |
| `mqtt_controls.feature` | 6 | Command topics, validation, the guarded write path |
| `polling.feature` | 3 | Retry/backoff, the retained last-good cache, readiness |
| `schedule.feature` | 2 | Derived `tou_window` / `boost` / `boost_ends_at` |
| `schedule_controls.feature` | 13 | Boost programming, ToU assertion, the reconcile |

**How the world is faked.** The suite runs the *real* units in-process against Go
fakes — there is no Python sidecar process, no broker and no inverter:

- `stubReader` implements `publisher.RegisterReader`, returning zero-filled or seeded
  register blocks and programmable transient failures.
- `fakeHRW` is a programmable, recording `controls.HoldingReadWriter` standing in for
  the holding-register bank, counting reads and writes so a scenario can assert that
  the guard issued **no** fc06 when the value already matched.
- `publisher.RecordingPublisher` captures MQTT output as last-message-per-topic
  (`REQ-HA-20`).
- `httptest` serves only the manager's **own** health/status HTTP surface.

> There is deliberately **no in-process mock of the sidecar's HTTP contract**. The
> sidecar's own pytest suite covers that contract from the Python side, and the Go
> side stubs at the `RegisterReader` / `HoldingReadWriter` interfaces instead, which
> keeps the acceptance suite testing manager behaviour rather than re-testing HTTP.

**Known gap:** no scenario yet covers graceful shutdown publishing retained `offline`
through the full `REQ-LC-10` teardown. `mqtt_discovery.feature` asserts the retained
`offline` payload directly, and `internal/mqtt` unit-tests the hook drain, but the
two are not joined end to end.

## Determinism (`REQ-TS-04`)

- `testing/synctest` drives the scheduler's timed loop, its ticker and its backoff on
  a fake clock — no real sleeps, no flaky timing.
- The `Now` and `After` seams (`REQ-SC-07`) are injected; the scheduler, controls
  handler and publisher share one `Now`, so the drift a test asserts is the drift the
  RTC-sync decision used.
- The acceptance suite drives `Scheduler.PollNow` (`REQ-SC-09`) one cycle at a time
  with an immediate fake backoff clock, rather than waiting on a ticker.
- `Server.SetReady` (`REQ-LC-13`) lets a scenario assert readiness transitions
  without driving whole poll cycles.

## Lint & vet (`REQ-TS-05`)

`golangci-lint` is pinned to **`v2.13.2`** and installed into `./bin` on first use, so
CI and local runs agree. `.golangci.yml` sets `version: "2"`, `run.go: "1.27"`, the
`standard` linter set and the `gofmt` formatter. `go vet ./...` is a separate target.

```sh
make lint    # golangci-lint run (installs the pinned binary on first use)
make vet     # go vet ./...
gofmt -l .   # must be empty
```

## Sidecar suite (`REQ-TS-07`)

The Python sidecar carries its own tests and linting, pinned in
`sidecar/requirements-dev.txt` (`pytest==8.3.4`, `ruff==0.9.2`):

```sh
make sidecar-install   # pip install -r sidecar/requirements-dev.txt
make sidecar-test      # pytest sidecar
make sidecar-lint      # ruff check + ruff format --check (sidecar/ruff.toml)
make sidecar-run       # MODE=mock python -m sidecar — fixture-backed, no hardware
```

- `sidecar/tests/test_api.py` — the HTTP surface: route dispatch, input validation
  (`count` 1..125, `addr`/`value` 0..65535), the `{"error":{code,message}}` envelope
  and every status code of `REQ-SD-07`, and `/health`.
- `sidecar/tests/test_transport.py` — the transport: the single lock, reconnect on
  error, per-call timeout, and the `MODE=mock` fixture-backed register map.
- `sidecar/tests/test_config.py` — env parsing and fail-fast: the Go-duration grammar
  (`REQ-SD-11`), listen-address splitting (`REQ-SD-08`), fixture resolution
  (`REQ-SD-09`), serial redaction (`REQ-SD-12`) and the aggregated `ConfigError`
  (`REQ-SD-13`).

## CI

`.github/workflows/checks.yml` is the single reusable gate (`REQ-DP-10`), called by
both the PR pipeline and the release pipeline. Five jobs, keeping the same
lint-vs-test split in each language so a formatting failure is distinguishable from a
behavioural one at a glance in the checks list:

| Job | Runs |
|---|---|
| `lint` | `make vet` then `make lint` |
| `test` | `make test` |
| `e2e` | `make test-e2e` |
| `sidecar-lint` | `make sidecar-install` then `make sidecar-lint` |
| `sidecar-test` | `make sidecar-install` then `make sidecar-test` |

Every job invokes a Makefile target, so there is no second definition of "green" to
drift. The Python jobs pin `3.12` to match the sidecar's runtime image
(`python:3.12-slim`) and ruff's `target-version`, and cache pip on **both** sidecar
requirements files — the runtime pin (`pysolarmanv5`) lives in `requirements.txt`,
which `requirements-dev.txt` includes, and the transport tests import it.
