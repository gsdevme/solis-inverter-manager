// Package server exposes the daemon's HTTP surface: a human-friendly status page
// on / plus liveness (/healthz) and readiness (/readyz) probes. Liveness is always
// OK while the process runs; readiness starts false, flips ready on the first
// successful poll and flips back not-ready after FAILURE_THRESHOLD consecutive
// poll failures. The scheduler drives readiness via MarkSuccess/MarkFailure.
// See docs/specs/06-lifecycle-health.md.
package server

import (
	"net/http"
	"sync"
	"time"
)

// Config configures the status server. It is deliberately decoupled from
// internal/config so serve.go maps domain values in and there is no import
// coupling here.
type Config struct {
	// FailureThreshold is the number of consecutive poll failures after which
	// readiness flips back to not-ready. A value below 1 is treated as 1 (see New);
	// the scheduler reports outcomes via MarkSuccess/MarkFailure and this type owns
	// the counting.
	FailureThreshold int
	PollInterval     time.Duration
}

// Server tracks process readiness and serves the health/status endpoints.
//
// Readiness is mutex-guarded because a flip depends on the consecutive-failure
// counter, not a single flag. It starts false: /readyz returns 503 until the first
// MarkSuccess, and returns to 503 once MarkFailure has been called `threshold`
// times in a row.
type Server struct {
	mu                  sync.Mutex
	ready               bool
	consecutiveFailures int
	threshold           int

	cfg       Config
	startedAt time.Time
}

// New returns a Server with readiness initially false that flips not-ready after
// cfg.FailureThreshold consecutive failures. A threshold below 1 is treated as 1.
func New(cfg Config) *Server {
	threshold := cfg.FailureThreshold
	if threshold < 1 {
		threshold = 1
	}
	return &Server{threshold: threshold, cfg: cfg, startedAt: time.Now()}
}

// MarkSuccess records a successful poll: readiness becomes true and the
// consecutive-failure counter resets.
func (s *Server) MarkSuccess() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready = true
	s.consecutiveFailures = 0
}

// MarkFailure records a poll failure; readiness flips not-ready once the number of
// consecutive failures reaches the configured threshold. Failures below the
// threshold hold the current (last-good) readiness so a single transient miss
// never blanks HA.
func (s *Server) MarkFailure() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consecutiveFailures++
	if s.consecutiveFailures >= s.threshold {
		s.ready = false
	}
}

// SetReady flips the readiness flag directly, bypassing the failure counter. It
// remains a thin setter for tests and the godog harness that assert readiness
// transitions without driving whole poll cycles; the scheduler uses MarkSuccess/
// MarkFailure in production.
func (s *Server) SetReady(ready bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready = ready
	if ready {
		s.consecutiveFailures = 0
	}
}

// Ready reports the current readiness state.
func (s *Server) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
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

// handleReadyz is readiness: 200 once a poll has succeeded, 503 before the first
// success and after FAILURE_THRESHOLD consecutive failures. Readiness is driven by
// the scheduler's MarkSuccess/MarkFailure calls, so it already reflects sidecar
// reachability transitively (a poll that cannot reach the sidecar fails).
func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	if s.Ready() {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("not ready"))
}
