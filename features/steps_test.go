package features

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"time"

	"github.com/cucumber/godog"

	"github.com/gsdevme/solis-inverter-manager/internal/controls"
	"github.com/gsdevme/solis-inverter-manager/internal/homeassistant"
	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
	"github.com/gsdevme/solis-inverter-manager/internal/publisher"
	"github.com/gsdevme/solis-inverter-manager/internal/schedule"
	"github.com/gsdevme/solis-inverter-manager/internal/scheduler"
	"github.com/gsdevme/solis-inverter-manager/internal/server"
)

// stubReader is a fake publisher.RegisterReader. ReadInput returns a zero-filled
// slice of the requested length — which decodes cleanly since every register is
// present — overlaying any seeded registers that fall inside the requested run.
// Seeds are keyed by absolute Modbus address (e.g. 33139 for battery SOC).
type stubReader struct {
	seed map[int]uint16
}

func (r *stubReader) ReadInput(_ context.Context, addr, count int) ([]uint16, error) {
	regs := make([]uint16, count)
	for a, v := range r.seed {
		if a >= addr && a < addr+count {
			regs[a-addr] = v
		}
	}
	return regs, nil
}

// write is one recorded fc06: the address and the value written to it.
type write struct {
	addr  int
	value uint16
}

// fakeHRW is a programmable, recording controls.HoldingReadWriter for the controls
// scenarios. regs maps absolute holding-register address to its current value;
// reads return zero-filled slices for unseeded addresses. WriteHolding mutates
// regs so the guard's confirming re-read observes the written value, and records
// every write and count-1 read so the steps can assert the guard's behaviour.
type fakeHRW struct {
	regs       map[int]uint16
	writeCalls []write
	reads1     []int // addresses of every count==1 ReadHolding
}

func newFakeHRW() *fakeHRW { return &fakeHRW{regs: map[int]uint16{}} }

func (f *fakeHRW) ReadHolding(_ context.Context, addr, count int) ([]uint16, error) {
	if count == 1 {
		f.reads1 = append(f.reads1, addr)
	}
	out := make([]uint16, count)
	for i := range out {
		out[i] = f.regs[addr+i]
	}
	return out, nil
}

func (f *fakeHRW) WriteHolding(_ context.Context, addr int, value uint16) error {
	f.writeCalls = append(f.writeCalls, write{addr, value})
	f.regs[addr] = value
	return nil
}

// reads1Count counts count-1 reads issued at addr (a re-read of the same register).
func (f *fakeHRW) reads1Count(addr int) int {
	n := 0
	for _, a := range f.reads1 {
		if a == addr {
			n++
		}
	}
	return n
}

// pollingReader is a scheduler.StateReader for the resilience scenarios. It fails
// its first failN reads (transient errors the scheduler retries with backoff) then
// returns telemetry carrying a fixed battery SOC. startFailing() makes every
// subsequent read fail, so a scenario can drive persistent failure after a good poll.
type pollingReader struct {
	failN int
	calls int
	soc   float64
}

func (r *pollingReader) Read(context.Context) (inverter.Telemetry, homeassistant.Setpoints, error) {
	r.calls++
	if r.calls <= r.failN {
		return inverter.Telemetry{}, homeassistant.Setpoints{}, fmt.Errorf("stub transient read failure")
	}
	var tel inverter.Telemetry
	tel.Battery.SOCPercent = r.soc
	return tel, homeassistant.Setpoints{}, nil
}

func (r *pollingReader) startFailing() { r.failN = r.calls + 1_000_000 }

// nopPublisher is a scheduler.StatePublisher that never errors (no broker in the
// suite); the resilience scenarios assert readiness and the cache, not payloads.
type nopPublisher struct{ count int }

func (p *nopPublisher) PublishState(context.Context, inverter.Telemetry, homeassistant.Setpoints) error {
	p.count++
	return nil
}

// world holds per-scenario state. The scaffold wires the real status server behind
// an httptest server and exercises its probes — no inverter, sidecar or broker.
// The MQTT fields drive the publisher against a recording client and the stub
// reader, again with no network.
type world struct {
	stat *server.Server
	srv  *httptest.Server

	cfg    homeassistant.Config
	rec    *publisher.RecordingPublisher
	svc    *publisher.Service
	reader *stubReader

	hrw     *fakeHRW
	handler *controls.Handler
	sp      homeassistant.Setpoints
	// now and tou are the handler's fixed clock and TOU_WINDOW setting, kept so a
	// scenario can rebuild the handler at another instant over the same bank.
	now time.Time
	tou string

	sched   *scheduler.Scheduler
	sreader *pollingReader
}

func (w *world) reset() {
	w.stat = nil
	w.srv = nil
	w.cfg = homeassistant.Config{}
	w.rec = nil
	w.svc = nil
	w.reader = nil
	w.hrw = nil
	w.handler = nil
	w.sp = homeassistant.Setpoints{}
	w.now = time.Time{}
	w.tou = ""
	w.sched = nil
	w.sreader = nil
}

func (w *world) cleanup() {
	if w.srv != nil {
		w.srv.Close()
	}
}

// --- Given / When ---

func (w *world) statusServerRunning() error {
	w.stat = server.New(server.Config{FailureThreshold: 3})
	w.srv = httptest.NewServer(w.stat.Handler())
	return nil
}

func (w *world) markedReady() error {
	w.stat.SetReady(true)
	return nil
}

// --- Then ---

func (w *world) endpointReturns(path string, want int) error {
	resp, err := http.Get(w.srv.URL + path)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != want {
		return fmt.Errorf("GET %s = %d, want %d", path, resp.StatusCode, want)
	}
	return nil
}

func (w *world) livenessReturns(code int) error  { return w.endpointReturns("/healthz", code) }
func (w *world) readinessReturns(code int) error { return w.endpointReturns("/readyz", code) }

// --- MQTT / Home Assistant discovery steps ---

func (w *world) configuredPublisher() error {
	w.cfg = homeassistant.Config{
		DiscoveryPrefix: "homeassistant",
		TopicPrefix:     "solis",
		Serial:          "1234567890",
		ControlsEnabled: true,
	}
	w.rec = publisher.NewRecordingPublisher()
	w.svc = publisher.New(w.rec, w.cfg)
	w.reader = &stubReader{seed: map[int]uint16{}}
	return nil
}

func (w *world) reportsSOC(pct int) error {
	w.reader.seed[inverter.RegBatterySOC] = uint16(pct)
	return nil
}

func (w *world) discoveryPublished() error {
	return w.svc.PublishDiscovery(context.Background())
}

func (w *world) pollCollectedAndStatePublished() error {
	return w.collectAndPublishState(homeassistant.Setpoints{})
}

// collectAndPublishState collects a poll from the stub reader and publishes state
// with the given setpoints, shared by the Phase-4 (zero setpoints) and controls
// (known setpoints) scenarios.
func (w *world) collectAndPublishState(sp homeassistant.Setpoints) error {
	tel, err := publisher.Collect(context.Background(), w.reader)
	if err != nil {
		return fmt.Errorf("collect: %w", err)
	}
	return w.svc.PublishState(context.Background(), tel, sp)
}

func (w *world) availabilityPublished(state string) error {
	return w.svc.PublishAvailability(context.Background(), state == "online")
}

func (w *world) discoveryForEveryEntity() error {
	entities := homeassistant.Entities()
	topics := w.rec.Topics()
	if len(topics) != len(entities) {
		return fmt.Errorf("recorded %d discovery topics, want %d (one per entity)", len(topics), len(entities))
	}
	prefix := w.cfg.DiscoveryPrefix + "/"
	for _, topic := range topics {
		if !strings.HasPrefix(topic, prefix) || !strings.HasSuffix(topic, "/config") {
			return fmt.Errorf("topic %q is not a discovery config under %q", topic, prefix)
		}
		rc, ok := w.rec.Get(topic)
		if !ok {
			return fmt.Errorf("topic %q listed but not retrievable", topic)
		}
		if !rc.Retain {
			return fmt.Errorf("discovery %q retain = false, want true", topic)
		}
	}
	return nil
}

func (w *world) retainedStateAtStateTopic() error {
	rc, ok := w.rec.Get(w.cfg.StateTopic())
	if !ok {
		return fmt.Errorf("no message at state topic %q", w.cfg.StateTopic())
	}
	if !rc.Retain {
		return fmt.Errorf("state retain = false, want true")
	}
	return nil
}

func (w *world) stateDoc() (map[string]any, error) {
	rc, ok := w.rec.Get(w.cfg.StateTopic())
	if !ok {
		return nil, fmt.Errorf("no message at state topic %q", w.cfg.StateTopic())
	}
	var doc map[string]any
	if err := json.Unmarshal(rc.Payload, &doc); err != nil {
		return nil, fmt.Errorf("state payload is not valid JSON: %w", err)
	}
	return doc, nil
}

func (w *world) stateContainsKeys(csv string) error {
	doc, err := w.stateDoc()
	if err != nil {
		return err
	}
	for _, key := range strings.Split(csv, ",") {
		key = strings.TrimSpace(key)
		if _, ok := doc[key]; !ok {
			return fmt.Errorf("state doc missing key %q", key)
		}
	}
	return nil
}

func (w *world) stateReportsSOC(want int) error {
	doc, err := w.stateDoc()
	if err != nil {
		return err
	}
	got, ok := doc["battery_soc"].(float64)
	if !ok {
		return fmt.Errorf("battery_soc is %T, want number", doc["battery_soc"])
	}
	if got != float64(want) {
		return fmt.Errorf("battery_soc = %v, want %d", got, want)
	}
	return nil
}

func (w *world) retainedAvailability(want string) error {
	rc, ok := w.rec.Get(w.cfg.AvailabilityTopic())
	if !ok {
		return fmt.Errorf("no message at availability topic %q", w.cfg.AvailabilityTopic())
	}
	if got := string(rc.Payload); got != want {
		return fmt.Errorf("availability payload = %q, want %q", got, want)
	}
	if !rc.Retain {
		return fmt.Errorf("availability retain = false, want true")
	}
	return nil
}

// --- MQTT writable-control steps ---

// quietLogger discards handler log output so the suite stays silent.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// controlsHandler wires a controls.Handler over the fake holding bank with the
// kill-switch enabled and a nil refresh (the scenarios drive publishing directly).
func (w *world) controlsHandler() error {
	w.hrw = newFakeHRW()
	w.handler = controls.NewHandler(w.hrw, quietLogger(), true, nil)
	return nil
}

// controlsHandlerAt wires a controls.Handler over a fresh fake holding bank whose
// clock is pinned to the given RFC3339 instant and whose Time-of-Use tariff is the
// given TOU_WINDOW setting, so boost planning and reconcile expiry are
// deterministic. The bank starts empty, so a scenario seeds its registers after
// this step, not before.
func (w *world) controlsHandlerAt(at, tou string) error {
	now, err := parseInstant(at)
	if err != nil {
		return err
	}
	w.hrw = newFakeHRW()
	return w.buildHandler(now, tou)
}

// buildHandler points a new handler at the bank the world already holds. The clock
// and the tariff are fixed at construction, so a scenario reconciling at another
// instant rebuilds rather than reseeding — the registers it seeded survive.
func (w *world) buildHandler(now time.Time, tou string) error {
	window, assert, err := schedule.ParseToUWindow(tou)
	if err != nil {
		return err
	}
	w.now, w.tou = now, tou
	w.handler = controls.NewHandler(w.hrw, quietLogger(), true, nil,
		controls.WithNow(func() time.Time { return now }),
		controls.WithToU(window, assert),
	)
	return nil
}

// parseInstant parses a step's RFC3339 timestamp, naming the offending text so a
// typo in a feature file reads as a step error rather than a zero clock.
func parseInstant(at string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse %q as RFC3339: %w", at, err)
	}
	return t, nil
}

func (w *world) holdingReads(addr, value int) error {
	w.hrw.regs[addr] = uint16(value)
	return nil
}

// scheduleReconciled runs one post-poll reconcile against the fake bank at the
// given instant, reading the slots first exactly as the scheduler does. When the
// instant differs from the handler's fixed clock the handler is rebuilt over the
// same bank, since the clock cannot be changed after construction.
func (w *world) scheduleReconciled(at string) error {
	now, err := parseInstant(at)
	if err != nil {
		return err
	}
	if !now.Equal(w.now) {
		if err := w.buildHandler(now, w.tou); err != nil {
			return err
		}
	}
	ctx := context.Background()
	sp, err := controls.ReadSetpoints(ctx, w.hrw)
	if err != nil {
		return err
	}
	_, err = w.handler.Reconcile(ctx, sp.Slots)
	return err
}

// commandArrives delivers one command to the handler on the conventional
// `solis/cmd/<key>/set` topic.
func (w *world) commandArrives(key, payload string) error {
	topic := "solis/cmd/" + key + "/set"
	w.handler.OnMessage(context.Background(), topic, []byte(payload))
	return nil
}

func (w *world) writtenOnce(addr, want int) error {
	if len(w.hrw.writeCalls) != 1 {
		return fmt.Errorf("writeCalls = %v, want exactly one write", w.hrw.writeCalls)
	}
	got := w.hrw.writeCalls[0]
	if got.addr != addr || int(got.value) != want {
		return fmt.Errorf("write = {addr:%d value:%d}, want {addr:%d value:%d}", got.addr, got.value, addr, want)
	}
	return nil
}

// writtenInOrder asserts the recorded writes are exactly one per register across
// the inclusive address range, in ascending address order, carrying the
// comma-separated values. It is the ordered-write contract a timed slot depends
// on: start hour, start minute, end hour, end minute, and no other fc06.
func (w *world) writtenInOrder(lo, hi int, values string) error {
	fields := strings.Split(values, ",")
	if len(fields) != hi-lo+1 {
		return fmt.Errorf("step lists %d values for registers %d to %d, want %d", len(fields), lo, hi, hi-lo+1)
	}
	want := make([]write, 0, len(fields))
	for i, field := range fields {
		value, err := parseRegisterValue(field)
		if err != nil {
			return err
		}
		want = append(want, write{addr: lo + i, value: value})
	}
	return w.writesAre(want)
}

// writePairsInOrder asserts the exact ordered write sequence from a list of
// "addr=value" pairs, for the sequences whose addresses are not contiguous — a
// Time-of-Use window re-asserted across its midnight split touches one register in
// each of two slots.
func (w *world) writePairsInOrder(pairs string) error {
	var want []write
	for _, pair := range strings.Split(pairs, ",") {
		addrText, valueText, found := strings.Cut(pair, "=")
		if !found {
			return fmt.Errorf("write %q is not addr=value", pair)
		}
		addr, err := strconv.Atoi(strings.TrimSpace(addrText))
		if err != nil {
			return fmt.Errorf("write %q: address: %w", pair, err)
		}
		value, err := parseRegisterValue(valueText)
		if err != nil {
			return fmt.Errorf("write %q: %w", pair, err)
		}
		want = append(want, write{addr: addr, value: value})
	}
	return w.writesAre(want)
}

// writesAre asserts the recorded fc06 calls are exactly want: same length, same
// order, same addresses and values. An extra or missing write fails, so a
// scenario pins the whole write sequence rather than a count.
func (w *world) writesAre(want []write) error {
	got := w.hrw.writeCalls
	if len(got) != len(want) {
		return fmt.Errorf("writeCalls = %v, want exactly %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			return fmt.Errorf("write %d = {addr:%d value:%d}, want {addr:%d value:%d}", i, got[i].addr, got[i].value, want[i].addr, want[i].value)
		}
	}
	return nil
}

// parseRegisterValue parses one unsigned 16-bit register value from a step list.
func parseRegisterValue(text string) (uint16, error) {
	value, err := strconv.ParseUint(strings.TrimSpace(text), 10, 16)
	if err != nil {
		return 0, fmt.Errorf("value %q is not a register value: %w", strings.TrimSpace(text), err)
	}
	return uint16(value), nil
}

func (w *world) noWrites() error {
	if len(w.hrw.writeCalls) != 0 {
		return fmt.Errorf("writeCalls = %v, want ZERO (no fc06)", w.hrw.writeCalls)
	}
	return nil
}

// writeConfirmed asserts the last write was confirmed, for the single-write
// command scenarios.
func (w *world) writeConfirmed() error {
	if len(w.hrw.writeCalls) == 0 {
		return fmt.Errorf("no write to confirm")
	}
	return w.confirmed(w.hrw.writeCalls[len(w.hrw.writeCalls)-1])
}

// everyWriteConfirmed asserts that every recorded write was confirmed, not just
// the last. A multi-register sequence such as a boost slot must pass the guard
// register by register, so a confirming re-read skipped on an interior register
// has to fail the scenario.
func (w *world) everyWriteConfirmed() error {
	if len(w.hrw.writeCalls) == 0 {
		return fmt.Errorf("no write to confirm")
	}
	for _, got := range w.hrw.writeCalls {
		if err := w.confirmed(got); err != nil {
			return err
		}
	}
	return nil
}

// confirmed asserts one write landed in the register bank and that the guard
// re-read that register (current read + confirming re-read).
func (w *world) confirmed(got write) error {
	if w.hrw.regs[got.addr] != got.value {
		return fmt.Errorf("register %d = %d after write, want %d", got.addr, w.hrw.regs[got.addr], got.value)
	}
	if n := w.hrw.reads1Count(got.addr); n < 2 {
		return fmt.Errorf("register %d had %d single-register reads, want >=2 (current + confirming re-read)", got.addr, n)
	}
	return nil
}

// controlsRead seeds the setpoints the state document mirrors, from the Optimal
// Income select's two options.
func (w *world) controlsRead(charge, discharge float64, optimal string) error {
	w.sp = homeassistant.Setpoints{
		SetChargeCurrent:    charge,
		SetDischargeCurrent: discharge,
		OptimalIncome:       optimal == "Run",
	}
	return nil
}

func (w *world) stateWithSetpoints() error {
	return w.collectAndPublishState(w.sp)
}

func (w *world) stateReportsNumber(key string, want float64) error {
	doc, err := w.stateDoc()
	if err != nil {
		return err
	}
	got, ok := doc[key].(float64)
	if !ok {
		return fmt.Errorf("%s is %T, want number", key, doc[key])
	}
	if got != want {
		return fmt.Errorf("%s = %v, want %v", key, got, want)
	}
	return nil
}

func (w *world) stateReportsString(key, want string) error {
	doc, err := w.stateDoc()
	if err != nil {
		return err
	}
	got, ok := doc[key].(string)
	if !ok {
		return fmt.Errorf("%s is %T, want string", key, doc[key])
	}
	if got != want {
		return fmt.Errorf("%s = %q, want %q", key, got, want)
	}
	return nil
}

// --- Readable schedule steps ---

// setpointsReadAt reads the whole setpoint block from the fake holding bank and
// maps it onto the Home Assistant DTO, resolving the boost's end against the
// instant named in the step. It mirrors serve.go's unexported toHASetpoints,
// which this package cannot reach, so the scenarios exercise the same derivation
// with a fixed clock instead of the wall clock.
func (w *world) setpointsReadAt(at string) error {
	now, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return fmt.Errorf("parse %q as RFC3339: %w", at, err)
	}
	sp, err := controls.ReadSetpoints(context.Background(), w.hrw)
	if err != nil {
		return err
	}
	w.sp = homeassistant.Setpoints{
		SetChargeCurrent:    sp.SetChargeCurrent,
		SetDischargeCurrent: sp.SetDischargeCurrent,
		OptimalIncome:       sp.OptimalIncome,
		Slots:               sp.Slots,
		BoostEndsAt:         schedule.BoostOf(sp.Slots).EndsAt(now),
	}
	return nil
}

// stateReportsNull asserts the key is published with an explicit JSON null, so
// the entity resolves to "unknown" rather than being absent or stale.
func (w *world) stateReportsNull(key string) error {
	doc, err := w.stateDoc()
	if err != nil {
		return err
	}
	got, ok := doc[key]
	if !ok {
		return fmt.Errorf("state doc missing key %q", key)
	}
	if got != nil {
		return fmt.Errorf("%s = %#v, want null", key, got)
	}
	return nil
}

// --- Resilient scheduling / readiness steps ---

// immediateAfter fires the backoff channel instantly, so retries run synchronously
// within a PollNow call with no wall-clock wait. Timing is covered by the
// scheduler's synctest unit tests; here we assert behaviour (retry, cache, readiness).
func immediateAfter(time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- time.Now()
	return ch
}

func (w *world) resilientScheduler(failN, threshold int) error {
	w.stat = server.New(server.Config{FailureThreshold: threshold})
	w.srv = httptest.NewServer(w.stat.Handler())
	w.sreader = &pollingReader{failN: failN, soc: 47}
	w.sched = scheduler.New(w.sreader, &nopPublisher{}, w.stat, nil, nil, nil, scheduler.Config{
		PollInterval: time.Minute,
		MaxRetries:   3,
		Logger:       quietLogger(),
		Now:          time.Now,
		After:        immediateAfter,
	})
	return nil
}

func (w *world) schedulerCompletesPolls(n int) error {
	for range n {
		w.sched.PollNow(context.Background())
	}
	return nil
}

func (w *world) inverterStartsFailing() error {
	w.sreader.startFailing()
	return nil
}

func (w *world) lastGoodReportsSOC(want int) error {
	tel, _, have := w.sched.LastState()
	if !have {
		return fmt.Errorf("no last-good state cached")
	}
	if int(tel.Battery.SOCPercent) != want {
		return fmt.Errorf("last-good battery SOC = %v, want %d", tel.Battery.SOCPercent, want)
	}
	return nil
}

func TestFeatures(t *testing.T) {
	w := &world{}
	suite := godog.TestSuite{
		ScenarioInitializer: func(ctx *godog.ScenarioContext) {
			ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
				w.reset()
				return c, nil
			})
			ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
				w.cleanup()
				return c, err
			})

			ctx.Step(`^the manager status server is running$`, w.statusServerRunning)
			ctx.Step(`^the manager is marked ready$`, w.markedReady)
			ctx.Step(`^the liveness endpoint returns (\d+)$`, w.livenessReturns)
			ctx.Step(`^the readiness endpoint returns (\d+)$`, w.readinessReturns)

			ctx.Step(`^a configured publisher with a recording MQTT client and a stub inverter reader$`, w.configuredPublisher)
			ctx.Step(`^the inverter reports a battery state of charge of (\d+) percent$`, w.reportsSOC)
			ctx.Step(`^discovery is published$`, w.discoveryPublished)
			ctx.Step(`^a poll is collected and state is published$`, w.pollCollectedAndStatePublished)
			ctx.Step(`^availability is published as (online|offline)$`, w.availabilityPublished)
			ctx.Step(`^a retained discovery config is published for every entity$`, w.discoveryForEveryEntity)
			ctx.Step(`^a retained state document is published at the state topic$`, w.retainedStateAtStateTopic)
			ctx.Step(`^the state document contains the keys "([^"]*)"$`, w.stateContainsKeys)
			ctx.Step(`^the state document reports battery_soc as (\d+)$`, w.stateReportsSOC)
			ctx.Step(`^a retained "(online|offline)" message is published at the availability topic$`, w.retainedAvailability)

			ctx.Step(`^a controls handler over a fake inverter holding-register bank$`, w.controlsHandler)
			ctx.Step(`^a controls handler over a fake inverter holding-register bank with the clock at (\S+) and TOU window "([^"]*)"$`, w.controlsHandlerAt)
			ctx.Step(`^holding register (\d+) currently reads (\d+)$`, w.holdingReads)
			ctx.Step(`^an? "([^"]*)" command arrives with payload "([^"]*)"$`, w.commandArrives)
			ctx.Step(`^the schedule is reconciled at (\S+)$`, w.scheduleReconciled)
			ctx.Step(`^holding register (\d+) is written once with (\d+)$`, w.writtenOnce)
			ctx.Step(`^holding registers (\d+) to (\d+) are written in order with "([^"]*)"$`, w.writtenInOrder)
			ctx.Step(`^the holding registers written in order are "([^"]*)"$`, w.writePairsInOrder)
			ctx.Step(`^no holding register is written$`, w.noWrites)
			ctx.Step(`^the write is confirmed by a re-read$`, w.writeConfirmed)
			ctx.Step(`^every write is confirmed by a re-read$`, w.everyWriteConfirmed)
			ctx.Step(`^the writable controls read charge ([-\d.]+) A, discharge ([-\d.]+) A, optimal income (Run|Stop)$`, w.controlsRead)
			ctx.Step(`^a poll is collected and state is published with those setpoints$`, w.stateWithSetpoints)
			ctx.Step(`^the state document reports (set_charge_current|set_discharge_current) as ([-\d.]+)$`, w.stateReportsNumber)
			ctx.Step(`^the state document reports (optimal_income|tou_window|boost_select|boost_ends_at|boost) as "([^"]*)"$`, w.stateReportsString)

			ctx.Step(`^the setpoints are read from the holding bank at (\S+)$`, w.setpointsReadAt)
			ctx.Step(`^the state document reports (\w+) as null$`, w.stateReportsNull)

			ctx.Step(`^a resilient scheduler whose inverter fails the first (\d+) reads and a failure threshold of (\d+)$`, w.resilientScheduler)
			ctx.Step(`^the scheduler completes (\d+) polls?$`, w.schedulerCompletesPolls)
			ctx.Step(`^the inverter starts failing every read$`, w.inverterStartsFailing)
			ctx.Step(`^the last-good state reports battery SOC (\d+)$`, w.lastGoodReportsSOC)
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"."},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog acceptance suite failed")
	}
}
