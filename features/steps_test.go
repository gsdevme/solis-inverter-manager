package features

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/gsdevme/solis-inverter-manager/internal/homeassistant"
	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
	"github.com/gsdevme/solis-inverter-manager/internal/publisher"
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
}

func (w *world) reset() {
	w.stat = nil
	w.srv = nil
	w.cfg = homeassistant.Config{}
	w.rec = nil
	w.svc = nil
	w.reader = nil
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
	tel, err := publisher.Collect(context.Background(), w.reader)
	if err != nil {
		return fmt.Errorf("collect: %w", err)
	}
	return w.svc.PublishState(context.Background(), tel)
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
