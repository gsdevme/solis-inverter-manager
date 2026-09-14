package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gsdevme/solis-inverter-manager/internal/homeassistant"
	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
	"github.com/gsdevme/solis-inverter-manager/internal/publisher"
	"github.com/gsdevme/solis-inverter-manager/internal/server"
)

// stateTestConfig mirrors the discovery config serve.go builds, with the fields
// the state topic and document depend on.
func stateTestConfig() homeassistant.Config {
	return homeassistant.Config{
		DiscoveryPrefix: "homeassistant",
		TopicPrefix:     "solis",
		Serial:          "1234567890",
		ControlsEnabled: true,
	}
}

// quietLogger keeps the telemetry log out of the test output.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// stateTestTelemetry is a decoded reading with a fixed RTC, so the document the
// publisher builds is deterministic against the fixed clock below.
func stateTestTelemetry(soc float64) inverter.Telemetry {
	var tel inverter.Telemetry
	tel.Time = time.Date(2026, 9, 12, 10, 0, 0, 0, time.Local)
	tel.Battery.SOCPercent = soc
	return tel
}

func stateTestNow() time.Time { return time.Date(2026, 9, 12, 10, 0, 0, 0, time.Local) }

// capturingRecorder forwards every reading to the real status server and keeps the
// document it was handed, so a test can assert both what the page renders and that
// the page was given the very bytes the broker got.
type capturingRecorder struct {
	*server.Server
	docs [][]byte
}

func (c *capturingRecorder) RecordReading(r server.Reading) error {
	c.docs = append(c.docs, r.Doc)
	return c.Server.RecordReading(r)
}

// errorPublisher is a publisher.Publisher whose every publish fails.
type errorPublisher struct{ err error }

func (p errorPublisher) Publish(context.Context, string, []byte, bool) error { return p.err }

// statePublisherFixture wires a statePublisher the way serve.go does, over a
// recording MQTT client and a real status server.
type statePublisherFixture struct {
	pub    *statePublisher
	rec    *publisher.RecordingPublisher
	status *capturingRecorder
	fresh  *setpointFreshness
	cfg    homeassistant.Config
}

func newStatePublisherFixture(t *testing.T, mqttPub publisher.Publisher) *statePublisherFixture {
	t.Helper()
	cfg := stateTestConfig()
	rec := publisher.NewRecordingPublisher()
	if mqttPub == nil {
		mqttPub = rec
	}
	svc := publisher.New(mqttPub, cfg, publisher.WithNow(stateTestNow))
	status := &capturingRecorder{Server: server.New(server.Config{FailureThreshold: 1})}
	fresh := &setpointFreshness{}

	return &statePublisherFixture{
		pub: &statePublisher{
			svc:          func() *publisher.Service { return svc },
			status:       status,
			fresh:        fresh,
			logTelemetry: true,
			logger:       quietLogger(),
		},
		rec:    rec,
		status: status,
		fresh:  fresh,
		cfg:    cfg,
	}
}

// page renders the status page the way an operator's browser would.
func (f *statePublisherFixture) page(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(f.status.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET / error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

// TestStatePublisherRecordsTheDocumentItPublished is the point of the type: Home
// Assistant and the status page must see one document built once, not two builds
// that could drift apart.
func TestStatePublisherRecordsTheDocumentItPublished(t *testing.T) {
	f := newStatePublisherFixture(t, nil)

	if err := f.pub.PublishState(context.Background(), stateTestTelemetry(57), homeassistant.Setpoints{}); err != nil {
		t.Fatalf("PublishState: %v", err)
	}

	rc, ok := f.rec.Get(f.cfg.StateTopic())
	if !ok {
		t.Fatalf("nothing published to the state topic %q", f.cfg.StateTopic())
	}
	if len(f.status.docs) != 1 {
		t.Fatalf("recorded %d readings, want 1", len(f.status.docs))
	}
	if !bytes.Equal(f.status.docs[0], rc.Payload) {
		t.Errorf("the recorded document is not the published one:\n%s\n%s", f.status.docs[0], rc.Payload)
	}

	body := f.page(t)
	if !strings.Contains(body, "<tr><td>battery_soc</td><td>57</td></tr>") {
		t.Errorf("status page does not show the telemetry's battery_soc:\n%s", body)
	}

	// The published document is what the page rendered, so every key in it should
	// have a row.
	var doc map[string]any
	if err := json.Unmarshal(rc.Payload, &doc); err != nil {
		t.Fatalf("published payload is not valid JSON: %v", err)
	}
	for key := range doc {
		if !strings.Contains(body, "<td>"+key+"</td>") {
			t.Errorf("status page is missing a row for %q:\n%s", key, body)
		}
	}
}

// TestStatePublisherFlagsStaleSetpoints checks the freshness flag reaches the page,
// which is the only signal an operator gets that the setpoints on screen are older
// than the telemetry beside them.
func TestStatePublisherFlagsStaleSetpoints(t *testing.T) {
	f := newStatePublisherFixture(t, nil)
	ctx := context.Background()

	f.fresh.markFresh()
	if err := f.pub.PublishState(ctx, stateTestTelemetry(57), homeassistant.Setpoints{}); err != nil {
		t.Fatalf("PublishState: %v", err)
	}
	if body := f.page(t); strings.Contains(body, "Setpoints:") {
		t.Fatalf("a freshly read poll should carry no setpoints note:\n%s", body)
	}

	f.fresh.markStale()
	if err := f.pub.PublishState(ctx, stateTestTelemetry(58), homeassistant.Setpoints{}); err != nil {
		t.Fatalf("PublishState after markStale: %v", err)
	}
	if body := f.page(t); !strings.Contains(body, "Setpoints: last read") {
		t.Errorf("a poll that reused cached setpoints should date them separately:\n%s", body)
	}
}

// TestStatePublisherRecordsDespiteAPublishFailure covers an unreachable broker: the
// page reports the inverter, so it must still show the reading, and the error must
// still reach the scheduler.
func TestStatePublisherRecordsDespiteAPublishFailure(t *testing.T) {
	boom := errors.New("broker unreachable")
	f := newStatePublisherFixture(t, errorPublisher{err: boom})

	err := f.pub.PublishState(context.Background(), stateTestTelemetry(57), homeassistant.Setpoints{})
	if !errors.Is(err, boom) {
		t.Fatalf("PublishState error = %v, want it to wrap %v", err, boom)
	}

	body := f.page(t)
	if !strings.Contains(body, "<tr><td>battery_soc</td><td>57</td></tr>") {
		t.Errorf("a publish failure must not cost the page its reading:\n%s", body)
	}
}

// TestStatePublisherWithoutAService covers the startup window before the publisher
// is stored: there is nothing to build a document with, so nothing is recorded.
func TestStatePublisherWithoutAService(t *testing.T) {
	f := newStatePublisherFixture(t, nil)
	f.pub.svc = func() *publisher.Service { return nil }

	if err := f.pub.PublishState(context.Background(), stateTestTelemetry(57), homeassistant.Setpoints{}); err == nil {
		t.Fatal("PublishState with no publisher = nil, want an error")
	}
	if len(f.status.docs) != 0 {
		t.Errorf("recorded %d readings with no publisher, want 0", len(f.status.docs))
	}
	if body := f.page(t); !strings.Contains(body, "no readings yet") {
		t.Errorf("page should have no reading to show:\n%s", body)
	}
}
