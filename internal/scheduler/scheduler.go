// Package scheduler owns the resilient poll loop.
//
// Each tick reads the inverter (via the sidecar) through an injected StateReader,
// decodes telemetry + writable-control setpoints, caches the last-good result,
// publishes state to MQTT and reports the outcome to the health server (readiness
// flips ready on the first success and not-ready after FAILURE_THRESHOLD
// consecutive failures). Ticks are serialised on apiMu at cadence POLL_INTERVAL
// with an immediate first poll; transient read errors are retried with exponential
// backoff up to POLL_MAX_RETRIES.
//
// Command dispatch is serialised on the SAME apiMu via ApplyCommand (mutex-on-
// demand): a guarded write + re-read + refresh is therefore atomic against a poll,
// because the sidecar's single socket can only service one Modbus frame at a time.
//
// Opt-in, threshold-gated RTC auto-sync is folded into the poll (no second
// goroutine): when enabled and the decoded clock drifts by more than the
// threshold, the guarded 43000–43005 write runs under apiMu. It is self-limiting
// (post-sync drift ≈ 0) and respects the flash-wear guardrail via the read-before-
// write guard. See docs/specs/04-polling-scheduling.md.
//
// The declarative schedule reconcile is folded in the same way: after a successful
// publish, the timed slots the poll just read are asserted against the desired
// schedule, so the steady state costs no extra Modbus frames and only differing
// registers are written.
//
// Now and After are injectable so testing/synctest can drive the loop on a fake
// clock.
package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/gsdevme/solis-inverter-manager/internal/homeassistant"
	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
)

// StateReader reads one poll's telemetry and writable-control setpoints through
// the serialised sidecar. It returns an error only when telemetry itself cannot be
// read (so the loop retries/backs off); a setpoints sub-read failure is handled by
// the adapter (reuse last-known) and never surfaces as an error here.
type StateReader interface {
	Read(ctx context.Context) (inverter.Telemetry, homeassistant.Setpoints, error)
}

// StatePublisher publishes the shared state document. When no broker is configured
// (mock runs) the adapter logs decoded telemetry and returns nil.
type StatePublisher interface {
	PublishState(ctx context.Context, tel inverter.Telemetry, sp homeassistant.Setpoints) error
}

// HealthReporter records poll outcomes for readiness (implemented by *server.Server).
type HealthReporter interface {
	MarkSuccess()
	MarkFailure()
}

// RTCSyncer performs the guarded RTC block write for opt-in auto-sync. It reports
// whether any register was written and the error that stopped the sequence, if any. May be nil when
// controls are disabled (auto-sync is then a no-op).
type RTCSyncer interface {
	SyncRTC(ctx context.Context) (bool, error)
}

// Reconciler asserts the desired timed schedule against the slots a poll just
// read, guard-writing only registers that differ. It reports whether any register
// was written and the error that stopped the sequence, if any. May be nil when controls are disabled
// (the reconcile is then a no-op).
type Reconciler interface {
	Reconcile(ctx context.Context, slots inverter.TimedSlots) (bool, error)
}

// Commander routes one inbound MQTT command (route → guarded write → refresh).
// Implemented by *controls.Handler.Apply. May be nil when controls are disabled.
type Commander interface {
	Apply(ctx context.Context, topic string, payload []byte)
}

// Config configures the scheduler.
type Config struct {
	PollInterval time.Duration
	MaxRetries   int

	// RTCSyncEnabled and RTCDriftThreshold gate opt-in RTC auto-sync folded into
	// the poll. RTCSyncEnabled defaults false; the sync fires only when enabled and
	// |drift| exceeds RTCDriftThreshold.
	RTCSyncEnabled    bool
	RTCDriftThreshold time.Duration

	Logger *slog.Logger
	// Now and After are injectable for deterministic tests (testing/synctest).
	Now   func() time.Time
	After func(time.Duration) <-chan time.Time
}

// Scheduler owns the poll loop, the last-good state cache and command serialisation.
type Scheduler struct {
	reader     StateReader
	publisher  StatePublisher
	health     HealthReporter
	rtc        RTCSyncer
	commander  Commander
	reconciler Reconciler

	cfg    Config
	logger *slog.Logger
	now    func() time.Time
	after  func(time.Duration) <-chan time.Time

	// apiMu serialises every sidecar interaction: polls never overlap, and a
	// command (ApplyCommand) is atomic against a poll.
	apiMu sync.Mutex

	// cacheMu guards the last-good cache independently of apiMu, so LastState (the
	// reconnect republish hook, on a transport goroutine) never blocks behind an
	// in-flight poll that may hold apiMu across several Modbus frames.
	cacheMu sync.Mutex
	tel     inverter.Telemetry
	sp      homeassistant.Setpoints
	have    bool
}

// New builds a Scheduler, defaulting the clock seams and clamping MaxRetries. rtc,
// commander and reconciler may be nil (controls disabled): auto-sync,
// ApplyCommand and the per-poll schedule reconcile then become no-ops.
func New(reader StateReader, pub StatePublisher, health HealthReporter, rtc RTCSyncer, commander Commander, reconciler Reconciler, cfg Config) *Scheduler {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.After == nil {
		cfg.After = time.After
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	return &Scheduler{
		reader: reader, publisher: pub, health: health,
		rtc: rtc, commander: commander, reconciler: reconciler,
		cfg: cfg, logger: cfg.Logger, now: cfg.Now, after: cfg.After,
	}
}

// Run polls immediately, then every PollInterval, blocking until ctx is cancelled.
// It is a single goroutine; RTC auto-sync is folded into poll, so there is no
// second goroutine.
func (s *Scheduler) Run(ctx context.Context) {
	s.poll(ctx) // immediate first poll
	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.poll(ctx)
		}
	}
}

// PollNow runs a single poll cycle. Exposed for the acceptance suite; the timed
// loop uses the same underlying logic.
func (s *Scheduler) PollNow(ctx context.Context) { s.poll(ctx) }

// poll performs one read (with backoff) + cache + publish, updates health, then
// runs opt-in RTC auto-sync and the declarative schedule reconcile. The whole
// cycle holds apiMu so it never overlaps a command or another poll.
func (s *Scheduler) poll(ctx context.Context) {
	s.apiMu.Lock()
	defer s.apiMu.Unlock()

	tel, sp, err := s.readWithRetry(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		s.logger.WarnContext(ctx, "poll failed", "err", err)
		s.health.MarkFailure()
		return
	}
	// Cache the last-good result before publishing so a subsequent publish failure
	// (broker down) still leaves the reconnect hook a fresh state to republish.
	s.RememberState(tel, sp)

	if err := s.publisher.PublishState(ctx, tel, sp); err != nil {
		s.logger.WarnContext(ctx, "publish state failed", "err", err)
		s.health.MarkFailure()
		return
	}
	s.health.MarkSuccess()

	s.maybeSyncRTC(ctx, tel)
	s.maybeReconcile(ctx, sp.Slots)
}

// maybeSyncRTC runs the guarded RTC write when auto-sync is enabled and the decoded
// inverter clock drifts by more than the threshold. It runs under apiMu (poll holds
// it) so the RTC write never overlaps another sidecar frame.
func (s *Scheduler) maybeSyncRTC(ctx context.Context, tel inverter.Telemetry) {
	if !s.cfg.RTCSyncEnabled || s.rtc == nil {
		return
	}
	drift := inverter.Drift(tel.Time, s.now())
	if abs(drift) <= s.cfg.RTCDriftThreshold {
		return
	}
	wrote, err := s.rtc.SyncRTC(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "rtc auto-sync failed", "drift", drift, "err", err)
		return
	}
	if wrote {
		s.logger.InfoContext(ctx, "rtc auto-synced", "drift", drift, "threshold", s.cfg.RTCDriftThreshold)
	}
}

// maybeReconcile asserts the desired timed schedule against the slots this poll
// read, so the steady-state reconcile costs no extra Modbus frames. It runs under
// apiMu (poll holds it) so its guarded writes never overlap another sidecar frame.
// A reconcile failure is non-fatal: telemetry is already published and healthy.
func (s *Scheduler) maybeReconcile(ctx context.Context, slots inverter.TimedSlots) {
	if s.reconciler == nil {
		return
	}
	wrote, err := s.reconciler.Reconcile(ctx, slots)
	if err != nil {
		s.logger.WarnContext(ctx, "schedule reconcile failed", "err", err)
		return
	}
	if wrote {
		s.logger.InfoContext(ctx, "schedule reconciled")
	}
}

// readWithRetry reads a poll, retrying transient errors with exponential backoff
// (1s, 2s, 4s, …) up to MaxRetries, honouring ctx cancellation via After.
func (s *Scheduler) readWithRetry(ctx context.Context) (inverter.Telemetry, homeassistant.Setpoints, error) {
	var lastErr error
	backoff := time.Second
	for attempt := 0; attempt <= s.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return inverter.Telemetry{}, homeassistant.Setpoints{}, ctx.Err()
			case <-s.after(backoff):
			}
			backoff *= 2
		}
		tel, sp, err := s.reader.Read(ctx)
		if err == nil {
			return tel, sp, nil
		}
		if ctx.Err() != nil {
			return inverter.Telemetry{}, homeassistant.Setpoints{}, ctx.Err()
		}
		lastErr = err
		s.logger.DebugContext(ctx, "poll attempt failed", "attempt", attempt, "err", err)
	}
	return inverter.Telemetry{}, homeassistant.Setpoints{}, lastErr
}

// ApplyCommand routes one inbound MQTT command under apiMu, giving mutex-on-demand
// serialisation: the guarded write + re-read + refresh is atomic against a poll.
// serve wires this into mc.SetOnMessage. It is a no-op when controls are disabled
// (nil commander).
func (s *Scheduler) ApplyCommand(ctx context.Context, topic string, payload []byte) {
	if s.commander == nil {
		return
	}
	s.apiMu.Lock()
	defer s.apiMu.Unlock()
	s.commander.Apply(ctx, topic, payload)
}

// LastState returns the most recent successfully-read telemetry and setpoints, and
// whether any poll has succeeded yet. The reconnect republish hook uses it.
func (s *Scheduler) LastState() (inverter.Telemetry, homeassistant.Setpoints, bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	return s.tel, s.sp, s.have
}

// RememberState updates the last-good cache. poll calls it on every successful
// read; serve's command-refresh calls it after a command republishes state, so the
// reconnect hook does not republish pre-command values in the window before the
// next poll.
func (s *Scheduler) RememberState(tel inverter.Telemetry, sp homeassistant.Setpoints) {
	s.cacheMu.Lock()
	s.tel, s.sp, s.have = tel, sp, true
	s.cacheMu.Unlock()
}

// abs returns the absolute value of a duration.
func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
