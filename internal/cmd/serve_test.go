package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsdevme/solis-inverter-manager/internal/homeassistant"
	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
	"github.com/gsdevme/solis-inverter-manager/internal/sidecarclient"
)

// stepRecorder is an ordered, concurrency-safe log of the steps a teardown or a
// republish pass took. Order is the thing under test in both, so every fake in
// this file records into one of these rather than into per-step counters.
type stepRecorder struct {
	mu    sync.Mutex
	steps []string
}

// record appends one step.
func (r *stepRecorder) record(step string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = append(r.steps, step)
}

// taken returns a copy of the steps recorded so far.
func (r *stepRecorder) taken() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.steps...)
}

// equal reports whether the recorded steps are exactly want, in order.
func (r *stepRecorder) equal(want ...string) bool {
	got := r.taken()
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// capturingLogger returns a logger writing into a buffer and a reader for what
// it has written, so a test can assert on the WARN lines the production code
// treats as its only output (waitForSidecar's timeout warning, say).
func capturingLogger() (*slog.Logger, func() string) {
	var (
		mu  sync.Mutex
		buf strings.Builder
	)
	logger := slog.New(slog.NewTextHandler(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return buf.Write(p)
	}), nil))
	return logger, func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

// writerFunc adapts a closure to io.Writer for capturingLogger.
type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// recordingBroker is a reconnectPublisher that records every publish in call
// order and can be told to fail any single step, so a test can assert both the
// sequence a reconnect must follow and that a failure does not abort the rest.
type recordingBroker struct {
	*stepRecorder
	fail  map[string]error
	state []inverter.Telemetry
}

func newRecordingBroker() *recordingBroker {
	return &recordingBroker{stepRecorder: &stepRecorder{}, fail: map[string]error{}}
}

func (b *recordingBroker) step(name string) error {
	b.record(name)
	return b.fail[name]
}

func (b *recordingBroker) PublishDiscovery(context.Context) error { return b.step("discovery") }

func (b *recordingBroker) PublishDiscoveryRemovals(context.Context) error {
	return b.step("removals")
}

func (b *recordingBroker) PublishAvailability(_ context.Context, online bool) error {
	if online {
		return b.step("availability online")
	}
	return b.step("availability offline")
}

func (b *recordingBroker) PublishState(_ context.Context, tel inverter.Telemetry, _ homeassistant.Setpoints) (homeassistant.Message, error) {
	b.mu.Lock()
	b.state = append(b.state, tel)
	b.mu.Unlock()
	return homeassistant.Message{}, b.step("state")
}

// republishFixture wires a republisher over a recordingBroker the way serve.go
// wires one over the publisher Service: cached tells it whether a poll has
// already filled the scheduler's last-good cache, and controls whether the
// command subscription exists.
type republishFixture struct {
	broker *recordingBroker
	rp     republisher
}

// cachedTelemetry is the last-good reading the fixture's cache reports, distinct
// enough that a test can tell the republish read it rather than a zero value.
func cachedTelemetry() inverter.Telemetry {
	var tel inverter.Telemetry
	tel.Battery.SOCPercent = 71
	return tel
}

func newRepublishFixture(cached, controls bool) *republishFixture {
	broker := newRecordingBroker()
	f := &republishFixture{broker: broker}
	f.rp = republisher{
		svc: func() reconnectPublisher { return broker },
		lastState: func() (inverter.Telemetry, homeassistant.Setpoints, bool) {
			if !cached {
				return inverter.Telemetry{}, homeassistant.Setpoints{}, false
			}
			return cachedTelemetry(), homeassistant.Setpoints{SetChargeCurrent: 20}, true
		},
		logger: discardLogger(),
	}
	if controls {
		f.rp.commandTopic = "solis/+/set"
		f.rp.subscribe = func(_ context.Context, topic string) error {
			broker.record("subscribe " + topic)
			return nil
		}
	}
	return f
}

// TestRepublishOrder pins REQ-HA-05 and REQ-HA-11: a reconnected session gets
// discovery, the removals, availability "online", the last cached state and the
// command subscription, in that order. Home Assistant needs the discovery config
// before the state it describes, and the command topic must come back with the
// session or every control silently stops working.
func TestRepublishOrder(t *testing.T) {
	f := newRepublishFixture(true, true)

	f.rp.run(context.Background())

	want := []string{"discovery", "removals", "availability online", "state", "subscribe solis/+/set"}
	if !f.broker.equal(want...) {
		t.Fatalf("republish steps = %v, want %v", f.broker.taken(), want)
	}
	if len(f.broker.state) != 1 || f.broker.state[0] != cachedTelemetry() {
		t.Errorf("republished state = %v, want the scheduler's cached reading %v", f.broker.state, cachedTelemetry())
	}
}

// TestRepublishSkipsStateBeforeTheFirstPoll covers the connect that happens
// before any poll has succeeded (and before the scheduler exists): there is no
// last-good reading, so nothing is published to the state topic — publishing a
// zero document would tell Home Assistant the battery is at 0%.
func TestRepublishSkipsStateBeforeTheFirstPoll(t *testing.T) {
	f := newRepublishFixture(false, true)

	f.rp.run(context.Background())

	want := []string{"discovery", "removals", "availability online", "subscribe solis/+/set"}
	if !f.broker.equal(want...) {
		t.Fatalf("republish steps = %v, want %v", f.broker.taken(), want)
	}
	if len(f.broker.state) != 0 {
		t.Errorf("published %d state documents with an empty cache, want 0", len(f.broker.state))
	}
}

// TestRepublishContinuesAfterAFailedStep: a republish is best-effort, so one
// failing publish must not cost the session its subscription or its state.
func TestRepublishContinuesAfterAFailedStep(t *testing.T) {
	f := newRepublishFixture(true, true)
	f.broker.fail["discovery"] = errors.New("broker refused")
	f.broker.fail["availability online"] = errors.New("broker refused")

	f.rp.run(context.Background())

	want := []string{"discovery", "removals", "availability online", "state", "subscribe solis/+/set"}
	if !f.broker.equal(want...) {
		t.Fatalf("republish steps = %v, want every step attempted %v", f.broker.taken(), want)
	}
}

// TestRepublishWithoutControlsDoesNotSubscribe: with CONTROLS_ENABLED=false the
// command entities are absent from discovery, so there is no command topic to
// subscribe to (REQ-HA-11).
func TestRepublishWithoutControlsDoesNotSubscribe(t *testing.T) {
	f := newRepublishFixture(true, false)

	f.rp.run(context.Background())

	want := []string{"discovery", "removals", "availability online", "state"}
	if !f.broker.equal(want...) {
		t.Fatalf("republish steps = %v, want %v", f.broker.taken(), want)
	}
}

// TestRepublishWithoutAPublisherDoesNothing covers the startup window in which
// the transport reports the connection up before serve has stored the Service:
// the hook must return quietly rather than dereference a nil publisher.
func TestRepublishWithoutAPublisherDoesNothing(t *testing.T) {
	f := newRepublishFixture(true, true)
	f.rp.svc = func() reconnectPublisher { return nil }

	f.rp.run(context.Background())

	if steps := f.broker.taken(); len(steps) != 0 {
		t.Errorf("republish steps = %v with no publisher, want none", steps)
	}
}

// teardownFixture wires a teardown whose every step records itself, over a
// scheduler-drain channel the test controls.
type teardownFixture struct {
	*stepRecorder
	schedDone chan struct{}
	td        teardown
}

func newTeardownFixture(broker bool, offlineErr error) *teardownFixture {
	f := &teardownFixture{stepRecorder: &stepRecorder{}, schedDone: make(chan struct{})}
	f.td = teardown{
		schedDone: f.schedDone,
		stopHealth: func() error {
			f.record("stop health")
			return nil
		},
		logger: discardLogger(),
	}
	if broker {
		f.td.offline = func(context.Context) error {
			f.record("offline")
			return offlineErr
		}
		f.td.disconnect = func(context.Context) error {
			f.record("disconnect")
			return nil
		}
	}
	return f
}

// run executes the teardown in its own goroutine and returns a channel closed
// when it finishes, so a test can assert it is still blocked on the drain.
func (f *teardownFixture) run() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		f.td.run()
		close(done)
	}()
	return done
}

// TestTeardownDrainsTheSchedulerFirst pins the load-bearing half of REQ-LC-10
// and REQ-HA-04: nothing happens until the scheduler goroutine has returned, so
// no in-flight poll or command is talking to the sidecar while the session is
// being closed; then "offline", disconnect and the health server, in that order.
func TestTeardownDrainsTheSchedulerFirst(t *testing.T) {
	f := newTeardownFixture(true, nil)

	done := f.run()
	select {
	case <-done:
		t.Fatal("teardown finished before the scheduler drained")
	case <-time.After(50 * time.Millisecond):
	}
	if steps := f.taken(); len(steps) != 0 {
		t.Fatalf("teardown took steps %v before the scheduler drained, want none", steps)
	}

	close(f.schedDone)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("teardown did not finish after the scheduler drained")
	}

	want := []string{"offline", "disconnect", "stop health"}
	if !f.equal(want...) {
		t.Errorf("teardown steps = %v, want %v", f.taken(), want)
	}
}

// TestTeardownWithoutABrokerStopsTheHealthServer: a mock run has no MQTT session,
// and must still tear down through the same path.
func TestTeardownWithoutABrokerStopsTheHealthServer(t *testing.T) {
	f := newTeardownFixture(false, nil)
	close(f.schedDone)

	f.td.run()

	if !f.equal("stop health") {
		t.Errorf("teardown steps = %v, want just the health server", f.taken())
	}
}

// TestTeardownContinuesAfterAFailedStep: the retained "offline" is best-effort
// (the LWT covers an unreachable broker anyway), so its failure must not skip the
// clean disconnect or leave the health listener running.
func TestTeardownContinuesAfterAFailedStep(t *testing.T) {
	f := newTeardownFixture(true, errors.New("broker unreachable"))
	close(f.schedDone)

	f.td.run()

	want := []string{"offline", "disconnect", "stop health"}
	if !f.equal(want...) {
		t.Errorf("teardown steps = %v, want %v", f.taken(), want)
	}
}

// exitFixture counts shutdown runs, which is how "exactly once" is asserted.
type exitFixture struct {
	shutdowns atomic.Int64
	cancelled atomic.Bool
}

func (f *exitFixture) shutdown() { f.shutdowns.Add(1) }

// TestWaitForExitOnSignal covers the SIGTERM/SIGINT branch of REQ-LC-10: the run
// context is cancelled, teardown runs once and the process exits zero.
func TestWaitForExitOnSignal(t *testing.T) {
	var f exitFixture
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := waitForExit(ctx, make(chan error, 1), func() { f.cancelled.Store(true) }, f.shutdown)

	if err != nil {
		t.Errorf("waitForExit = %v, want nil on the signal branch", err)
	}
	if got := f.shutdowns.Load(); got != 1 {
		t.Errorf("shutdown ran %d times, want exactly 1", got)
	}
}

// TestWaitForExitOnHealthServerError covers the other branch: a listen/serve
// failure must cancel the run context (so the scheduler drains and teardown's
// wait can return), run the same single teardown, and surface the health error as
// the exit error (REQ-LC-08).
func TestWaitForExitOnHealthServerError(t *testing.T) {
	var f exitFixture
	boom := errors.New("listen tcp :8080: address already in use")
	healthErr := make(chan error, 1)
	healthErr <- boom

	err := waitForExit(context.Background(), healthErr, func() { f.cancelled.Store(true) }, f.shutdown)

	if !errors.Is(err, boom) {
		t.Errorf("waitForExit = %v, want it to wrap %v", err, boom)
	}
	if !f.cancelled.Load() {
		t.Error("the health-error branch did not cancel the run context; the scheduler would never drain")
	}
	if got := f.shutdowns.Load(); got != 1 {
		t.Errorf("shutdown ran %d times, want exactly 1", got)
	}
}

// TestWaitForExitOnCleanHealthServerClose: the health goroutine reports nil when
// the listener closed cleanly, which must exit zero, not a wrapped nil.
func TestWaitForExitOnCleanHealthServerClose(t *testing.T) {
	var f exitFixture
	healthErr := make(chan error, 1)
	healthErr <- nil

	err := waitForExit(context.Background(), healthErr, func() { f.cancelled.Store(true) }, f.shutdown)

	if err != nil {
		t.Errorf("waitForExit = %v, want nil for a clean listener close", err)
	}
	if got := f.shutdowns.Load(); got != 1 {
		t.Errorf("shutdown ran %d times, want exactly 1", got)
	}
}

// sidecarStub is a fake sidecar whose /health answers 503 until the nth probe,
// counting every probe so a test can assert the retry cadence.
type sidecarStub struct {
	*httptest.Server
	mu      sync.Mutex
	probes  int
	serveAt int // the probe number from which /health answers 200; 0 = never
}

func newSidecarStub(t *testing.T, serveAt int) *sidecarStub {
	t.Helper()
	s := &sidecarStub{serveAt: serveAt}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		s.probes++
		n := s.probes
		s.mu.Unlock()
		if s.serveAt > 0 && n >= s.serveAt {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"ok":true,"inverter_reachable":true,"mode":"mock"}`)
			return
		}
		http.Error(w, `{"error":{"code":"starting","message":"not ready"}}`, http.StatusServiceUnavailable)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *sidecarStub) probeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.probes
}

// TestWaitForSidecarReturnsOnTheFirst200 pins the happy path of REQ-LC-11: one
// probe is enough, and startup logs how long it waited.
func TestWaitForSidecarReturnsOnTheFirst200(t *testing.T) {
	stub := newSidecarStub(t, 1)
	logger, logged := capturingLogger()

	waitForSidecar(t.Context(), sidecarclient.New(stub.URL), 5*time.Second, logger)

	if got := stub.probeCount(); got != 1 {
		t.Errorf("probes = %d, want 1: a serving sidecar ends the wait immediately", got)
	}
	if out := logged(); !strings.Contains(out, "sidecar serving") {
		t.Errorf("log = %q, want it to report the sidecar serving", out)
	}
}

// TestWaitForSidecarRetriesUntilServing pins the cadence of REQ-LC-11: a sidecar
// that is still starting is re-probed every sidecarProbeInterval until it
// answers, rather than being given up on after the first refusal.
func TestWaitForSidecarRetriesUntilServing(t *testing.T) {
	stub := newSidecarStub(t, 2) // the second probe succeeds
	logger, logged := capturingLogger()

	started := time.Now()
	waitForSidecar(t.Context(), sidecarclient.New(stub.URL), 5*time.Second, logger)
	elapsed := time.Since(started)

	if got := stub.probeCount(); got != 2 {
		t.Errorf("probes = %d, want 2: the first 503 must be retried", got)
	}
	if elapsed < sidecarProbeInterval {
		t.Errorf("waited %s before the second probe, want at least the %s probe interval", elapsed, sidecarProbeInterval)
	}
	if out := logged(); !strings.Contains(out, "sidecar serving") {
		t.Errorf("log = %q, want it to report the sidecar serving", out)
	}
}

// TestWaitForSidecarContinuesAfterTheTimeout: a sidecar that never answers is not
// fatal (REQ-LC-11) — startup warns and carries on, because the scheduler's
// retries and the /readyz gate already cover it.
func TestWaitForSidecarContinuesAfterTheTimeout(t *testing.T) {
	stub := newSidecarStub(t, 0) // never serves
	logger, logged := capturingLogger()

	waitForSidecar(t.Context(), sidecarclient.New(stub.URL), 50*time.Millisecond, logger)

	out := logged()
	if !strings.Contains(out, "sidecar not serving after startup timeout") {
		t.Errorf("log = %q, want the startup-timeout warning", out)
	}
	if strings.Contains(out, "sidecar serving") {
		t.Errorf("log = %q, want no serving line for a sidecar that never answered", out)
	}
}

// TestWaitForSidecarCancelledContextDoesNotWarn: a SIGTERM during the wait is not
// a sidecar problem, so it falls through to the teardown path silently
// (REQ-LC-11).
func TestWaitForSidecarCancelledContextDoesNotWarn(t *testing.T) {
	stub := newSidecarStub(t, 0)
	logger, logged := capturingLogger()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	waitForSidecar(ctx, sidecarclient.New(stub.URL), 5*time.Second, logger)

	if out := logged(); out != "" {
		t.Errorf("log = %q, want nothing: a cancelled startup is not a sidecar warning", out)
	}
}

// TestWaitForSidecarZeroTimeoutSkipsTheWait: SIDECAR_STARTUP_TIMEOUT=0 disables
// the wait outright (REQ-CF-09/REQ-LC-11), so not even one probe is issued.
func TestWaitForSidecarZeroTimeoutSkipsTheWait(t *testing.T) {
	stub := newSidecarStub(t, 1)
	logger, logged := capturingLogger()

	waitForSidecar(t.Context(), sidecarclient.New(stub.URL), 0, logger)

	if got := stub.probeCount(); got != 0 {
		t.Errorf("probes = %d with the wait disabled, want 0", got)
	}
	if out := logged(); out != "" {
		t.Errorf("log = %q, want nothing when the wait is disabled", out)
	}
}

// fakeBus is a registerBus backed by canned register words: every read returns a
// zero-filled block with the seeded addresses set, or the configured error.
type fakeBus struct {
	input      map[int]uint16
	holding    map[int]uint16
	inputErr   error
	holdingErr error
}

func (b *fakeBus) block(regs map[int]uint16, addr, count int) []uint16 {
	out := make([]uint16, count)
	for i := range out {
		out[i] = regs[addr+i]
	}
	return out
}

func (b *fakeBus) ReadInput(_ context.Context, addr, count int) ([]uint16, error) {
	if b.inputErr != nil {
		return nil, b.inputErr
	}
	return b.block(b.input, addr, count), nil
}

func (b *fakeBus) ReadHolding(_ context.Context, addr, count int) ([]uint16, error) {
	if b.holdingErr != nil {
		return nil, b.holdingErr
	}
	return b.block(b.holding, addr, count), nil
}

func (b *fakeBus) WriteHolding(context.Context, int, uint16) error { return nil }

// stateReaderFixture wires a state reader over a fake bus and a last-good cache
// the test controls.
type stateReaderFixture struct {
	bus    *fakeBus
	fresh  *setpointFreshness
	read   readerFunc
	cached homeassistant.Setpoints
}

// regBatterySOC is the input register the telemetry's SOC is decoded from, used
// to prove the telemetry half of a read is unaffected by a setpoints failure.
const regBatterySOC = 33139

func newStateReaderFixture(bus *fakeBus, haveCache bool) *stateReaderFixture {
	f := &stateReaderFixture{
		bus:    bus,
		fresh:  &setpointFreshness{},
		cached: homeassistant.Setpoints{SetChargeCurrent: 35, SetDischargeCurrent: 40, OptimalIncome: true},
	}
	f.read = newStateReader(bus, func() (inverter.Telemetry, homeassistant.Setpoints, bool) {
		if !haveCache {
			return inverter.Telemetry{}, homeassistant.Setpoints{}, false
		}
		return inverter.Telemetry{}, f.cached, true
	}, f.fresh, stateTestNow, discardLogger())
	return f
}

// TestStateReaderReusesSetpointsWhenTheHoldingReadFails is the point of
// REQ-SC-03's second half: a failed setpoints sub-read must not blank the
// controls. Folding zeros in would tell Home Assistant the charge current is 0 A
// and optimal-income is off — values nobody set, which the schedule reconcile
// would then act on.
func TestStateReaderReusesSetpointsWhenTheHoldingReadFails(t *testing.T) {
	bus := &fakeBus{
		input:      map[int]uint16{regBatterySOC: 64},
		holdingErr: errors.New("illegal address"),
	}
	f := newStateReaderFixture(bus, true)

	tel, sp, err := f.read(context.Background())

	if err != nil {
		t.Fatalf("read = %v, want nil: a setpoints failure is not a poll failure", err)
	}
	if tel.Battery.SOCPercent != 64 {
		t.Errorf("battery SOC = %v, want the telemetry read to survive at 64", tel.Battery.SOCPercent)
	}
	if sp != f.cached {
		t.Errorf("setpoints = %+v, want the last-known %+v", sp, f.cached)
	}
	if !f.fresh.isStale() {
		t.Error("setpoints were reused from cache but the reading is not marked stale")
	}
}

// TestStateReaderDecodesFreshSetpoints: the ordinary path still reads the holding
// bank and marks the reading fresh, so the reuse above cannot be achieved by
// never reading setpoints at all.
func TestStateReaderDecodesFreshSetpoints(t *testing.T) {
	bus := &fakeBus{
		input: map[int]uint16{regBatterySOC: 64},
		holding: map[int]uint16{
			inverter.RegTimedChargeCurrent:    123, // 12.3 A
			inverter.RegTimedDischargeCurrent: 456, // 45.6 A
		},
	}
	f := newStateReaderFixture(bus, true)
	f.fresh.markStale()

	_, sp, err := f.read(context.Background())

	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if sp.SetChargeCurrent != 12.3 || sp.SetDischargeCurrent != 45.6 {
		t.Errorf("setpoints = (%v, %v) A, want the decoded (12.3, 45.6)", sp.SetChargeCurrent, sp.SetDischargeCurrent)
	}
	if f.fresh.isStale() {
		t.Error("a successful setpoints read must clear the stale flag")
	}
}

// TestStateReaderFirstPollFallsBackToZero: on the very first poll there is no
// last-known value to reuse, so zero is the only honest answer — but the reading
// is still marked stale so the page and the reconcile know.
func TestStateReaderFirstPollFallsBackToZero(t *testing.T) {
	bus := &fakeBus{holdingErr: errors.New("illegal address")}
	f := newStateReaderFixture(bus, false)

	_, sp, err := f.read(context.Background())

	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if sp.SetChargeCurrent != 0 || sp.OptimalIncome {
		t.Errorf("setpoints = %+v, want the zero value with no cache to reuse", sp)
	}
	if !f.fresh.isStale() {
		t.Error("the first poll reused nothing, but the reading is still not fresh")
	}
}

// TestStateReaderFailsWhenTelemetryFails: telemetry is the read the poll exists
// for, so its failure is the one that must reach the scheduler and drive the
// backoff.
func TestStateReaderFailsWhenTelemetryFails(t *testing.T) {
	boom := errors.New("connection refused")
	f := newStateReaderFixture(&fakeBus{inputErr: boom}, true)

	_, _, err := f.read(context.Background())

	if !errors.Is(err, boom) {
		t.Fatalf("read = %v, want it to wrap %v", err, boom)
	}
}

// serveFixtureName is the Phase 0 capture the fake sidecar seeds its registers
// from, so a poll driven by runServe decodes real live values rather than zeros.
const serveFixtureName = "live-snapshot-comprehensive.json"

// serveFixtureRow is the status-page table row the fixture's battery SOC (input
// register 33139 = 99) must end up rendering as. Asserting the rendered row —
// rather than merely "a reading exists" — proves the words the fake sidecar
// served travelled the whole pipeline: sidecar client, decode, state document,
// statePublisher and the page.
const serveFixtureRow = "<td>battery_soc</td><td>99</td>"

// serveWaitTimeout bounds every wait-for-a-condition in the runServe tests. Each
// transition really takes milliseconds; the generous bound exists so a loaded CI
// box cannot make the test flaky, and is never waited out on the happy path.
const serveWaitTimeout = 10 * time.Second

// serveWaitStep is how often those conditions are re-checked.
const serveWaitStep = 5 * time.Millisecond

// fixtureRegisters loads a Phase 0 fixture's by_addr blocks into one
// address->word map. It is the same seeding the MODE=mock Python sidecar does
// (sidecar/mock.py), and the input and holding banks share one map because the
// two address ranges (33xxx, 43xxx) are disjoint.
func fixtureRegisters(t *testing.T, name string) map[int]uint16 {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "phase0", "fixtures", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var f struct {
		Blocks []struct {
			ByAddr map[string]uint16 `json:"by_addr"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	regs := make(map[int]uint16)
	for _, b := range f.Blocks {
		for addr, word := range b.ByAddr {
			n, err := strconv.Atoi(addr)
			if err != nil {
				t.Fatalf("fixture %s: bad register address %q: %v", name, addr, err)
			}
			regs[n] = word
		}
	}
	if len(regs) == 0 {
		t.Fatalf("fixture %s seeded no registers", name)
	}
	return regs
}

// serveSidecar is a fake Python sidecar over HTTP, implementing enough of
// docs/specs/01-sidecar-contract.md for runServe to poll it: /health always
// answers serving, and the two block reads answer from the fixture registers.
//
// Every register read blocks until release is called, which is what makes the
// "not ready before the first poll" assertion deterministic: while the gate is
// shut no poll can possibly complete, so a 200 from /readyz at that point is a
// real failure rather than a lost race.
type serveSidecar struct {
	*httptest.Server
	regs     map[int]uint16
	gate     chan struct{}
	released sync.Once
}

func newServeSidecar(t *testing.T) *serveSidecar {
	t.Helper()
	s := &serveSidecar{regs: fixtureRegisters(t, serveFixtureName), gate: make(chan struct{})}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"ok":true,"inverter_reachable":true,"mode":"mock"}`)
	})
	mux.HandleFunc("POST /read_input", s.handleRead)
	mux.HandleFunc("POST /read_holding", s.handleRead)
	s.Server = httptest.NewServer(mux)

	// Registered after the Close cleanup so it runs first (cleanups are LIFO):
	// Close blocks on in-flight requests, and a test that fails before releasing
	// the gate would otherwise deadlock the teardown.
	t.Cleanup(s.Close)
	t.Cleanup(s.release)
	return s
}

// handleRead answers one {addr, count} block read once the gate is open.
func (s *serveSidecar) handleRead(w http.ResponseWriter, r *http.Request) {
	select {
	case <-s.gate:
	case <-r.Context().Done():
		return
	}
	var req struct {
		Addr  int `json:"addr"`
		Count int `json:"count"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":{"code":"bad_request","message":"undecodable body"}}`, http.StatusBadRequest)
		return
	}
	regs := make([]uint16, req.Count)
	for i := range regs {
		regs[i] = s.regs[req.Addr+i]
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"addr": req.Addr, "count": req.Count, "regs": regs})
}

// release opens the gate so register reads are answered. It is idempotent so the
// cleanup can call it after the test already has.
func (s *serveSidecar) release() {
	s.released.Do(func() { close(s.gate) })
}

// freeHealthAddr reserves a loopback address for HEALTH_ADDR. runServe hands
// HEALTH_ADDR straight to http.Server.Addr and never reports the bound port, so
// ":0" would be undiscoverable; binding and immediately releasing a port is the
// only way to learn one the test can then probe.
func freeHealthAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a health port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("release the reserved health port: %v", err)
	}
	return addr
}

// serveHarness runs runServe in its own goroutine against a fake sidecar and a
// private health port, and gives the test an HTTP client for the health/status
// endpoints.
type serveHarness struct {
	sidecar *serveSidecar
	addr    string
	cancel  context.CancelFunc
	done    <-chan error
	client  *http.Client

	exited  sync.Once
	exitErr error
}

// startServe stands up the harness. Every environment variable runServe reads is
// pinned with t.Setenv rather than inherited: godotenv.Load runs in the cobra
// PersistentPreRun, not in runServe, so calling runServe directly must not depend
// on a developer's .env or exported shell values. MODE=mock drops the broker and
// inverter-identity requirements, so the pipeline publishes through
// publisher.Discard and no MQTT broker is involved.
func startServe(t *testing.T) *serveHarness {
	t.Helper()
	h := &serveHarness{
		sidecar: newServeSidecar(t),
		addr:    freeHealthAddr(t),
		client:  &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 5 * time.Second},
	}

	t.Setenv("MODE", "mock")
	t.Setenv("SIDECAR_URL", h.sidecar.URL)
	t.Setenv("SIDECAR_STARTUP_TIMEOUT", "5s")
	t.Setenv("HEALTH_ADDR", h.addr)
	t.Setenv("POLL_INTERVAL", "5s")
	t.Setenv("POLL_MAX_RETRIES", "0")
	t.Setenv("FAILURE_THRESHOLD", "1")
	t.Setenv("CONTROLS_ENABLED", "false")
	t.Setenv("RTC_SYNC_ENABLED", "false")
	t.Setenv("TOU_WINDOW", "")
	t.Setenv("MQTT_BROKER_URL", "")
	t.Setenv("LOG_LEVEL", "error")

	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	done := make(chan error, 1)
	go func() { done <- runServe(ctx) }()
	h.done = done

	t.Cleanup(func() {
		cancel()
		h.sidecar.release()
		_ = h.wait(t)
	})
	return h
}

// wait blocks until runServe has returned and reports its exit error, failing the
// test if it never does. The cleanup always calls it, so the result is cached: a
// test that waits on the exit itself must not leave the cleanup blocked on an
// already-drained channel.
func (h *serveHarness) wait(t *testing.T) error {
	t.Helper()
	h.exited.Do(func() {
		select {
		case h.exitErr = <-h.done:
		case <-time.After(serveWaitTimeout):
			t.Error("runServe did not return after the run context was cancelled")
		}
	})
	return h.exitErr
}

// get issues one request against the health/status server, reporting status 0
// when the listener refused the connection.
func (h *serveHarness) get(path string) (int, string) {
	resp, err := h.client.Get("http://" + h.addr + path)
	if err != nil {
		return 0, ""
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, ""
	}
	return resp.StatusCode, string(body)
}

// waitFor re-checks cond until it holds, failing the test with what it was
// waiting for if serveWaitTimeout passes first. Polling rather than sleeping a
// fixed time is what keeps the runServe tests both fast and non-flaky.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(serveWaitTimeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", serveWaitTimeout, what)
		}
		time.Sleep(serveWaitStep)
	}
}

// TestRunServeEndToEnd is the composition-root test: every seam runServe builds
// is unit-tested elsewhere, so what is under test here is the wiring between
// them. It runs the real runServe against a fake sidecar with no broker and no
// inverter, and asserts the observable consequences of that wiring:
//
//   - /healthz answers 200 while the manager runs (REQ-LC-01);
//   - /readyz is 503 before the first poll and 200 after it (REQ-LC-02,
//     REQ-LC-09) — the load-bearing assertion, since it only passes when config,
//     the sidecar client, the startup wait, the scheduler, the state read and the
//     readiness reporter are all connected to each other;
//   - the status page shows the reading that poll produced (REQ-LC-12), proving
//     statePublisher fans the built document out to the page as well as the
//     publisher;
//   - cancelling the context exits zero and stops the health listener
//     (REQ-LC-10's signal branch).
//
// A mis-plumbed composition root — a scheduler handed the wrong reporter, a
// statePublisher without the status server — leaves every existing unit test
// green and fails here.
func TestRunServeEndToEnd(t *testing.T) {
	h := startServe(t)

	waitFor(t, "the health server to start listening", func() bool {
		code, _ := h.get("/healthz")
		return code == http.StatusOK
	})

	if code, _ := h.get("/readyz"); code != http.StatusServiceUnavailable {
		t.Errorf("/readyz = %d with the first poll still blocked, want %d", code, http.StatusServiceUnavailable)
	}

	h.sidecar.release()

	waitFor(t, "/readyz to report ready after the first successful poll", func() bool {
		code, _ := h.get("/readyz")
		return code == http.StatusOK
	})

	code, body := h.get("/")
	if code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", code)
	}
	if strings.Contains(body, "no readings yet") {
		t.Errorf("status page still reports no readings after a successful poll:\n%s", body)
	}
	if !strings.Contains(body, serveFixtureRow) {
		t.Errorf("status page does not show the fixture's reading %q:\n%s", serveFixtureRow, body)
	}

	h.cancel()
	if err := h.wait(t); err != nil {
		t.Fatalf("runServe = %v, want nil on the signal branch", err)
	}
	waitFor(t, "the health listener to stop accepting connections", func() bool {
		code, _ := h.get("/healthz")
		return code == 0
	})
}

// TestRunServeRejectsABadConfig covers the other end of the composition root: a
// configuration that fails validation must abort startup with a wrapped config
// error and leave nothing running — in particular no health listener, which is
// started immediately after Load and would otherwise hold the port.
func TestRunServeRejectsABadConfig(t *testing.T) {
	addr := freeHealthAddr(t)
	t.Setenv("MODE", "bogus")
	t.Setenv("HEALTH_ADDR", addr)

	err := runServe(context.Background())

	if err == nil {
		t.Fatal("runServe = nil for MODE=bogus, want a config error")
	}
	if !strings.Contains(err.Error(), "config:") {
		t.Errorf("runServe = %v, want the error wrapped with \"config:\"", err)
	}
	if conn, dialErr := net.DialTimeout("tcp", addr, time.Second); dialErr == nil {
		_ = conn.Close()
		t.Errorf("a listener is still bound to %s after a config failure", addr)
	}
}
