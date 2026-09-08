// Package scheduler owns the poll loop.
//
// Each tick it reads the inverter (via the sidecar), decodes the registers, applies
// any pending setpoint writes under the READ-BEFORE-WRITE write-guard, publishes
// state to MQTT, and reports success/failure to the health server (flipping
// readiness via server.SetReady). Ticks are serialised and the cadence is
// POLL_INTERVAL. See docs/specs/04-polling-scheduling.md.
//
// TODO(phase-5): implement the loop with injectable Now/After for deterministic
// testing/synctest tests, retry with backoff, and the readiness reporting.
package scheduler
