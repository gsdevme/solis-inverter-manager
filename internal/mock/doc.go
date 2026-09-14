// Package mock provides an in-process stand-in for the Python sidecar so the full
// pipeline can run without a real inverter (MODE=mock).
//
// It will serve canned register snapshots (seeded from docs/phase0/fixtures) over
// the same HTTP contract as the real sidecar, and accept guarded writes so the
// write-path can be exercised end to end. It is intended to be shared by the godog
// acceptance suite and a standalone mock command. See docs/specs/07-testing.md.
//
// TODO(phase-6): implement the mock sidecar server, its routes and fixture loading.
package mock
