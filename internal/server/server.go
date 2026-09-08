// Package server exposes the daemon's HTTP surface: a human-friendly status page
// on / plus liveness (/healthz) and readiness (/readyz) probes. Liveness is always
// OK while the process runs; readiness starts false and is flipped ready by the
// scheduler after the first successful poll (see SetReady).
// See docs/specs/06-lifecycle-health.md.
package server

import (
	"net/http"
	"sync/atomic"
	"time"
)

// Config configures the status server. It is deliberately decoupled from
// internal/config so serve.go maps domain values in and there is no import
// coupling here.
type Config struct {
	// FailureThreshold is the number of consecutive poll failures after which
	// readiness should flip back to not-ready. It is carried here for later phases
	// (the scheduler owns the counting and calls SetReady); the scaffold only
	// exposes the flag itself.
	FailureThreshold int
	PollInterval     time.Duration
}

// Server tracks process readiness and serves the health/status endpoints.
//
// Readiness is a single atomic flag so probes and the scheduler can read/write it
// without locking. It starts false: /readyz returns 503 until SetReady(true) is
// called after the first successful poll.
type Server struct {
	ready atomic.Bool

	cfg       Config
	startedAt time.Time
}

// New returns a Server with readiness initially false.
func New(cfg Config) *Server {
	return &Server{cfg: cfg, startedAt: time.Now()}
}

// SetReady flips the readiness flag. Later phases call SetReady(true) after the
// first successful poll and SetReady(false) once consecutive failures exceed the
// configured threshold.
//
// TODO(phase-2+): the scheduler drives this — SetReady(true) after the first
// successful poll, SetReady(false) after FailureThreshold consecutive failures.
func (s *Server) SetReady(ready bool) {
	s.ready.Store(ready)
}

// Ready reports the current readiness state.
func (s *Server) Ready() bool {
	return s.ready.Load()
}

// Handler returns the mux serving /, /healthz and /readyz. See routes.go for the
// route table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	addRoutes(mux, s)
	return mux
}

// handleLivez is liveness: always 200 while the process is up.
func (s *Server) handleLivez(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleReadyz is readiness: 200 only once the ready flag is set, else 503.
//
// TODO(phase-2+): also probe the sidecar (internal/sidecarclient) here so /readyz
// reflects sidecar reachability, not just the first-poll flag.
func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	if s.Ready() {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("not ready"))
}
