// Package sidecarclient is the Go manager's typed HTTP client for the Python
// sidecar's register RPCs.
//
// The sidecar owns the Modbus/Solarman-V5 transport to the inverter's datalogger
// (port 8899) and exposes a small localhost HTTP API. This package provides a
// typed Client over SIDECAR_URL for reading register blocks (ReadInput/fc04,
// ReadHolding/fc03), issuing single-register writes (WriteHolding/fc06), and a
// health/reachability probe (Health) consumed by /readyz. The wire contract is
// docs/specs/01-sidecar-contract.md.
//
// The client is a dumb transport. It returns raw uint16 register words and ack
// values and performs no register decode, scaling, sign or endianness handling —
// that is the internal/inverter package's job. It also enforces no
// read-before-write guard: WriteHolding always issues the fc06, and the guard
// that avoids flash wear lives in the manager above this client. Non-2xx
// responses map to sentinel errors (ErrTimeout, ErrConnection, ErrFrame,
// ErrIllegalAddress, ErrBadRequest, ...) so callers can classify failures with
// errors.Is.
package sidecarclient
