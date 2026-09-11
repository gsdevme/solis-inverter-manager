package sidecarclient

import (
	"errors"
	"fmt"
)

// Sentinel errors mirror the sidecar's wire error codes (see the "Error model"
// section of docs/specs/01-sidecar-contract.md and sidecar/errors.py). A
// SidecarError returned by any client method unwraps to exactly one of these, so
// callers branch on the failure class with errors.Is rather than string-matching
// the code.
//
// The manager treats ErrTimeout, ErrConnection and ErrFrame as retryable poll
// failures; ErrBadRequest and ErrIllegalAddress signal a programming or
// addressing bug and should not be retried blindly.
var (
	// ErrBadRequest is the "bad_request" code (HTTP 400): a malformed body or an
	// out-of-range argument.
	ErrBadRequest = errors.New("sidecar: bad request")
	// ErrTimeout is the "timeout" code (HTTP 504): the datalogger socket timed
	// out (INVERTER_SOCKET_TIMEOUT).
	ErrTimeout = errors.New("sidecar: timeout")
	// ErrIllegalAddress is the "illegal_address" code (HTTP 502): the inverter
	// returned a Modbus exception such as an illegal data address.
	ErrIllegalAddress = errors.New("sidecar: illegal address")
	// ErrFrame is the "frame_error" code (HTTP 502): a Solarman V5 frame or CRC
	// error while decoding the response.
	ErrFrame = errors.New("sidecar: frame error")
	// ErrConnection is the "connection_error" code (HTTP 503): the socket is dead
	// or a fresh connection to the datalogger failed.
	ErrConnection = errors.New("sidecar: connection error")
	// ErrNotFound is the "not_found" code (HTTP 404): no route matched.
	ErrNotFound = errors.New("sidecar: not found")
	// ErrInternal is the fallback "internal_error" code (HTTP 500) and the class
	// for any unrecognised wire code.
	ErrInternal = errors.New("sidecar: internal error")
)

// codeSentinels maps each documented wire code to its sentinel. Codes absent
// from the map fall back to ErrInternal via sentinelForCode.
var codeSentinels = map[string]error{
	"bad_request":      ErrBadRequest,
	"timeout":          ErrTimeout,
	"illegal_address":  ErrIllegalAddress,
	"frame_error":      ErrFrame,
	"connection_error": ErrConnection,
	"not_found":        ErrNotFound,
	"internal_error":   ErrInternal,
}

// sentinelForCode returns the sentinel for a wire code, defaulting to ErrInternal
// for any code the contract does not define.
func sentinelForCode(code string) error {
	if s, ok := codeSentinels[code]; ok {
		return s
	}
	return ErrInternal
}

// SidecarError is a non-2xx response carrying the sidecar's error envelope. It
// records the wire Code and Message and the HTTP Status, and unwraps to the
// sentinel for Code so errors.Is(err, ErrTimeout) and friends match.
type SidecarError struct {
	// Code is the wire code string, e.g. "timeout" (see sidecar/errors.py).
	Code string
	// Message is the human-readable detail from the envelope.
	Message string
	// Status is the HTTP status the sidecar returned.
	Status int
}

// Error implements error.
func (e *SidecarError) Error() string {
	return fmt.Sprintf("sidecar: %s (code=%s, http=%d)", e.Message, e.Code, e.Status)
}

// Unwrap returns the sentinel for the error's code so callers can classify the
// failure with errors.Is.
func (e *SidecarError) Unwrap() error {
	return sentinelForCode(e.Code)
}
