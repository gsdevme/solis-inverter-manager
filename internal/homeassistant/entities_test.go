package homeassistant

import "testing"

// wantKeys is the full, ordered catalogue every other test and the state DTO must
// agree with. It is the single place the expected key set is written down.
var wantKeys = []string{
	"battery_voltage", "battery_current", "battery_power", "battery_charging",
	"battery_soc", "battery_soh", "bms_voltage", "bms_current",
	"pv1_voltage", "pv1_current", "pv2_voltage", "pv2_current", "pv_total_power",
	"grid_power", "grid_total_import", "grid_import_today", "grid_total_export", "grid_export_today",
	"ac_active_power", "inverter_temperature", "grid_frequency", "house_load",
	"generation_today", "generation_yesterday", "battery_total_charge", "battery_charge_today",
	"battery_total_discharge", "battery_discharge_today",
	"status", "operating_status", "work_mode", "rtc", "rtc_drift",
}

func TestEntitiesCoverEveryTelemetryField(t *testing.T) {
	entities := Entities()
	if len(entities) != len(wantKeys) {
		t.Fatalf("got %d entities, want %d", len(entities), len(wantKeys))
	}

	got := map[string]bool{}
	for _, e := range entities {
		if got[e.Key] {
			t.Errorf("duplicate entity key %q", e.Key)
		}
		got[e.Key] = true
		if e.Component != Sensor && e.Component != BinarySensor {
			t.Errorf("entity %q has unexpected component %q", e.Key, e.Component)
		}
	}
	for _, k := range wantKeys {
		if !got[k] {
			t.Errorf("missing entity key %q", k)
		}
	}
}

func TestEntitiesDeterministicOrder(t *testing.T) {
	a := Entities()
	b := Entities()
	for i := range a {
		if a[i].Key != b[i].Key {
			t.Fatalf("order not stable at %d: %q vs %q", i, a[i].Key, b[i].Key)
		}
		if a[i].Key != wantKeys[i] {
			t.Errorf("entity %d = %q, want %q", i, a[i].Key, wantKeys[i])
		}
	}
}

func TestTopicHelpers(t *testing.T) {
	c := Config{DiscoveryPrefix: "homeassistant", TopicPrefix: "solis", Serial: "1234567890"}
	if got := c.BaseTopic(); got != "solis/1234567890" {
		t.Errorf("BaseTopic = %q", got)
	}
	if got := c.StateTopic(); got != "solis/1234567890/state" {
		t.Errorf("StateTopic = %q", got)
	}
	if got := c.AvailabilityTopic(); got != "solis/1234567890/availability" {
		t.Errorf("AvailabilityTopic = %q", got)
	}
}
