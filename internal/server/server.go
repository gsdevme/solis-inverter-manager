// Package server exposes the daemon's HTTP surface: a human-friendly status page
// on / plus liveness (/healthz) and readiness (/readyz) probes. Liveness is always
// OK while the process runs; readiness starts false, flips ready on the first
// successful poll and flips back not-ready after FAILURE_THRESHOLD consecutive
// poll failures. The scheduler drives readiness via MarkSuccess/MarkFailure.
// See docs/specs/06-lifecycle-health.md.
package server

import (
	"fmt"
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

// Reading is one recorded state document for the status page.
type Reading struct {
	// Doc is the flat JSON state document as published to Home Assistant.
	Doc []byte
	// SetpointsStale is true when the writable-control setpoints in Doc were
	// reused from an earlier read because the holding-register read failed, so
	// the page can say so instead of stamping them with the telemetry's age.
	SetpointsStale bool
}

// reading is an immutable snapshot of the last recorded Reading, decoded into
// rendered rows once at record time. at is when it was recorded; setpointsAt is
// when the setpoints it carries were last read straight from the inverter, which
// is zero while none ever have been.
type reading struct {
	rows        []valueRow
	at          time.Time
	setpointsAt time.Time
}

// Server tracks process readiness and serves the health/status endpoints.
//
// Readiness is mutex-guarded because a flip depends on the consecutive-failure
// counter, not a single flag. It starts false: /readyz returns 503 until the first
// MarkSuccess, and returns to 503 once MarkFailure has been called `threshold`
// times in a row.
//
// The last reading lives behind the same mutex. It is an immutable snapshot shared
// by pointer: RecordReading decodes outside the lock and swaps a fresh one in, so
// handleRoot renders from the pointer with the lock released and never races a
// later reading. The state document is opaque here — this package takes bytes in
// and renders HTML out, and knows nothing of the inverter or Home Assistant.
type Server struct {
	mu                  sync.Mutex
	ready               bool
	consecutiveFailures int
	threshold           int
	last                *reading

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

// RecordReading decodes and stores a reading. It returns an error and keeps the
// previous reading when Doc is not a single JSON object (empty, null, an array, a
// scalar, malformed, or followed by trailing data). The caller may reuse or mutate
// Doc afterwards: nothing of it is retained past the decode.
//
// A reading whose setpoints were reused from an earlier read carries that earlier
// read's timestamp forward, so the page can date the telemetry and the setpoints
// separately.
func (s *Server) RecordReading(r Reading) error {
	rows, err := decodeValues(r.Doc)
	if err != nil {
		return fmt.Errorf("record reading: %w", err)
	}
	at := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	setpointsAt := at
	if r.SetpointsStale {
		setpointsAt = time.Time{}
		if s.last != nil {
			setpointsAt = s.last.setpointsAt
		}
	}
	s.last = &reading{rows: rows, at: at, setpointsAt: setpointsAt}
	return nil
}

// snapshot returns readiness and the last recorded reading (nil before the first)
// under a single critical section, so the page renders one consistent view of the
// Server. The reading is immutable, so the caller keeps using it after the lock is
// released.
func (s *Server) snapshot() (bool, *reading) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready, s.last
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
