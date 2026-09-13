package features

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

// fakeHRW is a programmable, recording controls.HoldingReadWriter for the controls
// scenarios. regs maps absolute holding-register address to its current value;
// reads return zero-filled slices for unseeded addresses. WriteHolding mutates
// regs so the guard's confirming re-read observes the written value, and records
// every write and count-1 read so the steps can assert the guard's behaviour.
type fakeHRW struct {
	regs       map[int]uint16
	writeCalls []struct {
		addr  int
		value uint16
	}
	reads1 []int // addresses of every count==1 ReadHolding
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
	f.writeCalls = append(f.writeCalls, struct {
		addr  int
		value uint16
	}{addr, value})
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

func (w *world) holdingReads(addr, value int) error {
	w.hrw.regs[addr] = uint16(value)
	return nil
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

func (w *world) noWrites() error {
	if len(w.hrw.writeCalls) != 0 {
		return fmt.Errorf("writeCalls = %v, want ZERO (no fc06)", w.hrw.writeCalls)
	}
	return nil
}

// writeConfirmed asserts the last write landed in the register bank and that the
// guard re-read that register (current read + confirming re-read).
func (w *world) writeConfirmed() error {
	if len(w.hrw.writeCalls) == 0 {
		return fmt.Errorf("no write to confirm")
	}
	last := w.hrw.writeCalls[len(w.hrw.writeCalls)-1]
	if w.hrw.regs[last.addr] != last.value {
		return fmt.Errorf("register %d = %d after write, want %d", last.addr, w.hrw.regs[last.addr], last.value)
	}
	if n := w.hrw.reads1Count(last.addr); n < 2 {
		return fmt.Errorf("register %d had %d single-register reads, want >=2 (current + confirming re-read)", last.addr, n)
	}
	return nil
}

func (w *world) controlsRead(charge, discharge float64, optimal string) error {
	w.sp = homeassistant.Setpoints{
		SetChargeCurrent:    charge,
		SetDischargeCurrent: discharge,
		OptimalIncome:       strings.EqualFold(optimal, "ON"),
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
	w.sched = scheduler.New(w.sreader, &nopPublisher{}, w.stat, nil, nil, scheduler.Config{
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
			ctx.Step(`^holding register (\d+) currently reads (\d+)$`, w.holdingReads)
			ctx.Step(`^an? "([^"]*)" command arrives with payload "([^"]*)"$`, w.commandArrives)
			ctx.Step(`^holding register (\d+) is written once with (\d+)$`, w.writtenOnce)
			ctx.Step(`^no holding register is written$`, w.noWrites)
			ctx.Step(`^the write is confirmed by a re-read$`, w.writeConfirmed)
			ctx.Step(`^the writable controls read charge ([-\d.]+) A, discharge ([-\d.]+) A, optimal income (ON|OFF)$`, w.controlsRead)
			ctx.Step(`^a poll is collected and state is published with those setpoints$`, w.stateWithSetpoints)
			ctx.Step(`^the state document reports (set_charge_current|set_discharge_current) as ([-\d.]+)$`, w.stateReportsNumber)
			ctx.Step(`^the state document reports (optimal_income|tou_window|boost|boost_ends_at) as "([^"]*)"$`, w.stateReportsString)

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
