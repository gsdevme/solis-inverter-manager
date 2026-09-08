// Package inverter decodes and encodes the Solis inverter's Modbus registers into
// domain values.
//
// It turns raw register words fetched via the sidecar into a typed reading (battery
// SOC/power, PV, grid, energy totals, RTC, work-mode) and encodes setpoint writes
// (charge/discharge current, work-mode, RTC), applying the width/scale/sign rules
// confirmed in docs/phase0/findings.md and specified in docs/specs/02-register-map.md.
//
// TODO(phase-3): implement register decode/encode, the Reading type, the work-mode
// bitfield, and the READ-BEFORE-WRITE comparison helpers used by the write-guard.
package inverter
