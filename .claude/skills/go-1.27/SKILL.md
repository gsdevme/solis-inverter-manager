---
name: go-1.27
description: Use when writing Go for this repo to apply current-toolchain (go1.27) idioms — iterators/range-over-func, slices/maps, log/slog, testing/synctest, t.Context, generics. Validated against the installed go1.27.0.
---

# Go 1.27 toolchain idioms

Validated against `go1.27.0` (installed; `go.mod` says `go 1.27.0`). Prefer these over
older patterns.

## Iterators (range-over-func)

- Functions returning `iter.Seq[T]` / `iter.Seq2[K,V]` are `range`-able:
  `for v := range seq { … }`. Use for streaming without materialising slices.
- Bridge to slices with `slices.Collect(seq)`, `slices.Sorted(seq)`,
  `maps.Keys(m)`/`maps.Values(m)` (both return iterators — wrap with
  `slices.Sorted`/`slices.Collect` to get a slice).

## `slices` and `maps`

- `slices.Contains`, `ContainsFunc`, `IndexFunc`, `SortFunc`, `SortedFunc`,
  `Concat`, `Equal`, `Clone`, `Compact`, `BinarySearch`.
- `maps.Clone`, `maps.Copy`, `maps.Equal`, `maps.Keys`/`maps.Values` (iterators).
- Builtins `min`, `max`, `clear` are available — e.g. clamp the HA amps control with
  `min(max(amps, 0), 60)` rather than hand-rolled branches.

## `log/slog` (structured logging)

- Default to `slog`. Build a handler from config:
  `slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})` (or `TextHandler`).
- `slog.SetDefault(slog.New(handler))`; then `slog.InfoContext(ctx, "polled",
  "soc", pct, "battery_w", w)`.
- Prefer typed attrs for hot paths: `slog.Int`, `slog.String`, `slog.Duration`.
- Never log secrets — pass the redacted config value (`INVERTER_SERIAL`,
  `MQTT_PASSWORD`); `internal/config` already redacts.

## Context in tests

- `t.Context()` returns a context cancelled just before cleanup — use it instead of
  `context.Background()` in tests so goroutines stop deterministically.

## `testing/synctest` (deterministic time)

- `synctest.Test(t, func(t *testing.T){ … })` runs the body in a **bubble** with a
  **fake clock** starting 2000-01-01 UTC. `time.Sleep`, `time.After`, `time.Ticker`,
  and `context` timers all use the fake clock.
- `synctest.Wait()` blocks until every goroutine in the bubble is durably blocked —
  use it to advance past scheduled work without real sleeps.
- Ideal for the ~60s serialized poll scheduler: assert the poll ticks on interval and
  backoff grows on repeated failure, with zero wall-clock delay. Inject the clock.
- Keep bubbles self-contained: no real network/processes; use the `MODE=mock` fake
  sidecar via an in-bubble `httptest`-style fake or inject the `sidecarclient`.

## Generics

- Use type params for small utilities (`Optional[T]`, `ptr[T any](v T) *T`), not for
  everything. A concrete type is clearer when there's one caller.
- Constraints from `cmp.Ordered` for comparisons; `any` for pass-through.

## JSON

- Stick with stdlib `encoding/json` (struct tags, `json.RawMessage` for deferred
  parsing, `json.Number` for numeric precision). Only reach for `encoding/json/v2` if the
  repo explicitly enables it (`GOEXPERIMENT`); do not import it speculatively.
- The sidecar wire types are small stable envelopes (`{addr,count,regs}`,
  `{error:{code,message}}`) — model them as typed structs, not `map[string]any`.

## Register decode (repo-specific)

- The sidecar returns raw `[]uint16` for a block read starting at `addr`. Slice by
  absolute address: a small helper `at(regs []uint16, base, addr int) uint16` and
  `u32(regs, base, addr) uint32` (MSW at the lower address) keeps decoders declarative.
- Reinterpret two's-complement with `int16(u)` / `int32(u32)` for S16/S32 registers;
  apply scale as a final `float64` step. See `docs/specs/02-register-map.md`.

## Misc

- `errors.Join` to combine multiple failures (config validation does this); `errors.Is`/
  `As` for inspection.
- `context.WithTimeoutCause`/`WithCancelCause` when a specific cancellation reason helps
  debugging.
- Run `go vet ./...` — it catches slog key/value mismatches and loop/context misuse.
