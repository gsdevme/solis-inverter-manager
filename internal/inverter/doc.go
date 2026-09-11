// Package inverter decodes and encodes the Solis inverter's Modbus registers into
// domain values.
//
// It turns raw register words fetched via the sidecar into a typed Telemetry
// reading (battery SOC/power, PV, grid, energy totals, RTC, work-mode) and encodes
// setpoint writes (charge/discharge current, work-mode, RTC), applying the
// width/scale/sign rules confirmed in docs/phase0/findings.md and specified in
// docs/specs/02-register-map.md.
//
// The package is pure: it performs no I/O and reads no configuration. A Block is a
// raw register slice keyed by its base address; a Snapshot aggregates the blocks
// from one poll so decoders resolve registers by absolute address. Decoded
// physical values are float64 with scale already applied. The work-mode helpers and
// EncodeAmps/EncodeRTC produce the register words the write-guard compares against a
// prior read (read-before-write) before issuing an fc06 write.
package inverter
