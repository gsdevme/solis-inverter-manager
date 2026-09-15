package homeassistant

import "testing"

// wantKeys is the full, ordered catalogue every other test and the state DTO must
// agree with. It is the single place the expected key set is written down.
var wantKeys = []string{
	"battery_voltage", "battery_current", "battery_power",
	"battery_charge_power", "battery_discharge_power", "battery_charging",
	"battery_soc", "battery_soh", "bms_voltage", "bms_current",
	"bms_charge_current_limit", "bms_discharge_current_limit",
	"bms_fault_1", "bms_fault_2",
	"bms_over_voltage", "bms_under_voltage", "bms_over_temp", "bms_under_temp",
	"bms_charge_over_temp", "bms_charge_under_temp", "bms_discharge_over_current",
	"bms_charge_over_current", "bms_internal_protection", "bms_module_unbalanced",
	"overdischarge_soc", "force_charge_soc",
	"pv1_voltage", "pv1_current", "pv2_voltage", "pv2_current", "pv_total_power",
	"grid_power", "grid_total_import", "grid_import_today", "grid_total_export", "grid_export_today",
	"ac_active_power", "inverter_temperature", "grid_frequency", "house_load",
	"generation_today", "generation_yesterday", "battery_total_charge", "battery_charge_today",
	"battery_total_discharge", "battery_discharge_today",
	"status", "status_text", "operating_status", "work_mode", "rtc", "rtc_drift",
	"tou_window", "boost", "boost_ends_at",
	"inverter_max_charge_current", "inverter_max_discharge_current",
	"set_charge_current", "set_discharge_current", "optimal_income", "boost_select", "rtc_sync",
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
		switch e.Component {
		case Sensor, BinarySensor, Number, Switch, Button, Select:
		default:
			t.Errorf("entity %q has unexpected component %q", e.Key, e.Component)
		}
	}
	for _, k := range wantKeys {
		if !got[k] {
			t.Errorf("missing entity key %q", k)
		}
	}
}

// TestSelectEntitiesCarryOptions guards the Select invariant: a select with no
// options discovers an unusable entity in Home Assistant.
func TestSelectEntitiesCarryOptions(t *testing.T) {
	for _, e := range Entities() {
		if e.Component == Select && len(e.Options) == 0 {
			t.Errorf("select %q carries no options", e.Key)
		}
		if e.Component != Select && len(e.Options) != 0 {
			t.Errorf("%s %q carries options but is not a select", e.Component, e.Key)
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
