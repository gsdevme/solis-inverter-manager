package publisher_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gsdevme/solis-inverter-manager/internal/homeassistant"
	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
	"github.com/gsdevme/solis-inverter-manager/internal/publisher"
)

func testConfig() homeassistant.Config {
	return homeassistant.Config{
		DiscoveryPrefix: "homeassistant",
		TopicPrefix:     "solis",
		Serial:          "1234567890",
	}
}

func TestPublishDiscovery(t *testing.T) {
	rec := publisher.NewRecordingPublisher()
	svc := publisher.New(rec, testConfig())

	if err := svc.PublishDiscovery(context.Background()); err != nil {
		t.Fatalf("PublishDiscovery: %v", err)
	}

	topics := rec.Topics()
	const wantCount = 36
	if len(topics) != wantCount {
		t.Fatalf("discovery topic count = %d, want %d", len(topics), wantCount)
	}

	for _, topic := range topics {
		rc, ok := rec.Get(topic)
		if !ok {
			t.Fatalf("topic %q recorded in order but not retrievable", topic)
		}
		if !rc.Retain {
			t.Errorf("discovery %q retain = false, want true", topic)
		}
	}

	// Spot-check a couple of topics look like
	// <DiscoveryPrefix>/<component>/<Serial>_<key>/config.
	wantTopics := []string{
		"homeassistant/sensor/1234567890_battery_soc/config",
		"homeassistant/binary_sensor/1234567890_battery_charging/config",
	}
	for _, want := range wantTopics {
		if _, ok := rec.Get(want); !ok {
			t.Errorf("expected discovery topic %q not published", want)
		}
	}
}

func TestPublishAvailability(t *testing.T) {
	cfg := testConfig()
	tests := []struct {
		online bool
		want   string
	}{
		{online: true, want: "online"},
		{online: false, want: "offline"},
	}
	for _, tc := range tests {
		rec := publisher.NewRecordingPublisher()
		svc := publisher.New(rec, cfg)
		if err := svc.PublishAvailability(context.Background(), tc.online); err != nil {
			t.Fatalf("PublishAvailability(%v): %v", tc.online, err)
		}
		rc, ok := rec.Get(cfg.AvailabilityTopic())
		if !ok {
			t.Fatalf("no message at availability topic %q", cfg.AvailabilityTopic())
		}
		if got := string(rc.Payload); got != tc.want {
			t.Errorf("availability payload = %q, want %q", got, tc.want)
		}
		if !rc.Retain {
			t.Errorf("availability retain = false, want true")
		}
	}
}

func TestAvailabilityTopic(t *testing.T) {
	cfg := testConfig()
	svc := publisher.New(publisher.NewRecordingPublisher(), cfg)
	if got, want := svc.AvailabilityTopic(), cfg.AvailabilityTopic(); got != want {
		t.Errorf("AvailabilityTopic() = %q, want %q", got, want)
	}
}

func TestPublishState(t *testing.T) {
	cfg := testConfig()
	rec := publisher.NewRecordingPublisher()
	svc := publisher.New(rec, cfg)

	var tel inverter.Telemetry
	tel.Time = time.Date(2026, 9, 12, 10, 0, 0, 0, time.Local)
	tel.Battery.SOCPercent = 87
	tel.Grid.PowerW = -1500

	if err := svc.PublishState(context.Background(), tel, homeassistant.Setpoints{}); err != nil {
		t.Fatalf("PublishState: %v", err)
	}

	rc, ok := rec.Get(cfg.StateTopic())
	if !ok {
		t.Fatalf("no message at state topic %q", cfg.StateTopic())
	}
	if !rc.Retain {
		t.Errorf("state retain = false, want true")
	}

	var doc map[string]any
	if err := json.Unmarshal(rc.Payload, &doc); err != nil {
		t.Fatalf("state payload is not valid JSON: %v", err)
	}
	for _, key := range []string{"battery_soc", "grid_power", "rtc", "rtc_drift"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("state doc missing key %q", key)
		}
	}
	if got, ok := doc["battery_soc"].(float64); !ok || got != 87 {
		t.Errorf("battery_soc = %v, want 87", doc["battery_soc"])
	}
	if got, ok := doc["grid_power"].(float64); !ok || got != -1500 {
		t.Errorf("grid_power = %v, want -1500", doc["grid_power"])
	}
	if rtc, _ := doc["rtc"].(string); !strings.HasPrefix(rtc, "2026-09-12T10:00:00") {
		t.Errorf("rtc = %q, want RFC3339 for 2026-09-12T10:00:00", rtc)
	}
}

// TestPublishDiscoveryRemovals covers the stale-entity cleanup: one empty
// retained payload to the retired switch's discovery topic, which is what tells
// Home Assistant to delete the entity.
func TestPublishDiscoveryRemovals(t *testing.T) {
	rec := publisher.NewRecordingPublisher()
	svc := publisher.New(rec, testConfig())

	if err := svc.PublishDiscoveryRemovals(context.Background()); err != nil {
		t.Fatalf("PublishDiscoveryRemovals: %v", err)
	}

	topics := rec.Topics()
	if len(topics) != 1 {
		t.Fatalf("removal topic count = %d, want 1", len(topics))
	}
	const want = "homeassistant/switch/1234567890_optimal_income/config"
	if topics[0] != want {
		t.Fatalf("removal topic = %q, want %q", topics[0], want)
	}
	rc, ok := rec.Get(want)
	if !ok {
		t.Fatalf("topic %q recorded in order but not retrievable", want)
	}
	if !rc.Retain {
		t.Error("removal retain = false, want true")
	}
	if len(rc.Payload) != 0 {
		t.Errorf("removal payload = %q, want empty", rc.Payload)
	}
}
