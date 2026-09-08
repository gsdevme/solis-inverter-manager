// Package sidecarclient is the Go manager's HTTP client for the Python sidecar.
//
// The sidecar owns the Modbus/Solarman-V5 transport to the inverter's datalogger
// (port 8899) and exposes a small localhost HTTP API. This package will provide a
// typed client over SIDECAR_URL for reading register blocks and issuing guarded
// writes, plus a health/reachability probe consumed by /readyz.
//
// TODO(phase-2+): define the request/response types (mirroring the sidecar
// contract in docs/specs/01-sidecar-contract.md), the client constructor, read and
// write methods, timeouts and error mapping.
package sidecarclient
