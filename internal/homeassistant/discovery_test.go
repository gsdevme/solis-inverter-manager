package homeassistant

import (
	"encoding/json"
	"testing"
)

func testConfig() Config {
	return Config{DiscoveryPrefix: "homeassistant", TopicPrefix: "solis", Serial: "1234567890"}
}

// wantMessages counts the discovery messages a config should emit: every entity
// unless it is a gated command entity and controls are disabled.
func wantMessages(c Config) int {
	n := 0
	for _, e := range Entities() {
		if e.Command && !c.ControlsEnabled {
			continue
		}
		n++
	}
	return n
}

func mustBuildDiscoveryFor(t *testing.T, c Config) map[string]map[string]any {
	t.Helper()
	msgs, err := c.BuildDiscovery()
	if err != nil {
		t.Fatalf("BuildDiscovery: %v", err)
	}
	if want := wantMessages(c); len(msgs) != want {
		t.Fatalf("got %d messages, want %d", len(msgs), want)
	}
	out := map[string]map[string]any{}
	for _, m := range msgs {
		var p map[string]any
		if err := json.Unmarshal(m.Payload, &p); err != nil {
			t.Fatalf("bad payload for %s: %v", m.Topic, err)
		}
		out[m.Topic] = p
	}
	return out
}

func mustBuildDiscovery(t *testing.T) map[string]map[string]any {
	t.Helper()
	msgs, err := testConfig().BuildDiscovery()
	if err != nil {
		t.Fatalf("BuildDiscovery: %v", err)
	}
	if want := wantMessages(testConfig()); len(msgs) != want {
		t.Fatalf("got %d messages, want %d", len(msgs), want)
	}
	out := map[string]map[string]any{}
	for _, m := range msgs {
		var p map[string]any
		if err := json.Unmarshal(m.Payload, &p); err != nil {
			t.Fatalf("bad payload for %s: %v", m.Topic, err)
		}
		out[m.Topic] = p
	}
	return out
}

func TestDiscoveryTopicAndSharedFields(t *testing.T) {
	byTopic := mustBuildDiscovery(t)
	p, ok := byTopic["homeassistant/sensor/1234567890_battery_soc/config"]
	if !ok {
		t.Fatal("missing battery_soc discovery topic")
	}
	if p["~"] != "solis/1234567890" {
		t.Errorf("~ = %v", p["~"])
	}
	if p["state_topic"] != "~/state" {
		t.Errorf("state_topic = %v", p["state_topic"])
	}
	if p["availability_topic"] != "~/availability" {
		t.Errorf("availability_topic = %v", p["availability_topic"])
	}
	if p["payload_available"] != "online" || p["payload_not_available"] != "offline" {
		t.Errorf("availability payloads = %v / %v", p["payload_available"], p["payload_not_available"])
	}
	if p["unique_id"] != "1234567890_battery_soc" || p["object_id"] != "1234567890_battery_soc" {
		t.Errorf("unique_id/object_id = %v / %v", p["unique_id"], p["object_id"])
	}
	if p["value_template"] != "{{ value_json.battery_soc }}" {
		t.Errorf("value_template = %v", p["value_template"])
	}
	if p["device_class"] != "battery" || p["state_class"] != "measurement" || p["unit_of_measurement"] != "%" {
		t.Errorf("classes = %v / %v / %v", p["device_class"], p["state_class"], p["unit_of_measurement"])
	}
}

func TestDiscoveryDeviceBlockIdenticalAcrossEntities(t *testing.T) {
	byTopic := mustBuildDiscovery(t)
	var first string
	for topic, p := range byTopic {
		dev, _ := p["device"].(map[string]any)
		if dev == nil {
			t.Fatalf("%s has no device block", topic)
		}
		if dev["manufacturer"] != Manufacturer || dev["model"] != Model || dev["name"] != DeviceName {
			t.Errorf("%s device = %v", topic, dev)
		}
		ids, _ := dev["identifiers"].([]any)
		if len(ids) != 1 || ids[0] != "1234567890" {
			t.Errorf("%s identifiers = %v", topic, dev["identifiers"])
		}
		b, _ := json.Marshal(dev)
		if first == "" {
			first = string(b)
		} else if string(b) != first {
			t.Errorf("%s device block differs: %s vs %s", topic, b, first)
		}
	}
}

func TestDiscoveryEnergyStateClasses(t *testing.T) {
	byTopic := mustBuildDiscovery(t)
	// Lifetime counter -> total_increasing.
	if sc := byTopic["homeassistant/sensor/1234567890_grid_total_import/config"]["state_class"]; sc != "total_increasing" {
		t.Errorf("grid_total_import state_class = %v, want total_increasing", sc)
	}
	// Daily counter -> total (tolerates the midnight reset).
	if sc := byTopic["homeassistant/sensor/1234567890_grid_import_today/config"]["state_class"]; sc != "total" {
		t.Errorf("grid_import_today state_class = %v, want total", sc)
	}
	// Fixed-per-day snapshot -> no state_class at all.
	yst := byTopic["homeassistant/sensor/1234567890_generation_yesterday/config"]
	if _, present := yst["state_class"]; present {
		t.Errorf("generation_yesterday should not carry a state_class, got %v", yst["state_class"])
	}
	if yst["device_class"] != "energy" || yst["entity_category"] != "diagnostic" {
		t.Errorf("generation_yesterday device_class/category = %v / %v", yst["device_class"], yst["entity_category"])
	}
}

func TestDiscoveryBinarySensor(t *testing.T) {
	byTopic := mustBuildDiscovery(t)
	p := byTopic["homeassistant/binary_sensor/1234567890_battery_charging/config"]
	if p == nil {
		t.Fatal("missing battery_charging binary_sensor")
	}
	if p["value_template"] != "{{ 'ON' if value_json.battery_charging else 'OFF' }}" {
		t.Errorf("value_template = %v", p["value_template"])
	}
	if p["payload_on"] != "ON" || p["payload_off"] != "OFF" {
		t.Errorf("payload_on/off = %v / %v", p["payload_on"], p["payload_off"])
	}
	if p["device_class"] != "battery_charging" {
		t.Errorf("device_class = %v", p["device_class"])
	}
	if _, present := p["state_class"]; present {
		t.Errorf("binary_sensor should not carry state_class")
	}
}

func TestDiscoveryTimestampAndDuration(t *testing.T) {
	byTopic := mustBuildDiscovery(t)
	rtc := byTopic["homeassistant/sensor/1234567890_rtc/config"]
	if rtc["device_class"] != "timestamp" || rtc["entity_category"] != "diagnostic" {
		t.Errorf("rtc device_class/category = %v / %v", rtc["device_class"], rtc["entity_category"])
	}
	if _, present := rtc["unit_of_measurement"]; present {
		t.Errorf("rtc should carry no unit, got %v", rtc["unit_of_measurement"])
	}
	drift := byTopic["homeassistant/sensor/1234567890_rtc_drift/config"]
	if drift["device_class"] != "duration" || drift["unit_of_measurement"] != "s" || drift["entity_category"] != "diagnostic" {
		t.Errorf("rtc_drift = %v", drift)
	}
}

// TestDiscoveryBareDiagnosticSensors covers the raw-value sensors that carry no
// device_class/state_class/unit (status, operating_status, work_mode).
func TestDiscoveryBareDiagnosticSensors(t *testing.T) {
	byTopic := mustBuildDiscovery(t)
	for _, key := range []string{"status", "operating_status", "work_mode"} {
		p := byTopic["homeassistant/sensor/1234567890_"+key+"/config"]
		if p == nil {
			t.Fatalf("missing %s", key)
		}
		if p["entity_category"] != "diagnostic" {
			t.Errorf("%s entity_category = %v", key, p["entity_category"])
		}
		for _, absent := range []string{"device_class", "state_class", "unit_of_measurement"} {
			if _, present := p[absent]; present {
				t.Errorf("%s should not carry %s, got %v", key, absent, p[absent])
			}
		}
		if p["value_template"] != "{{ value_json."+key+" }}" {
			t.Errorf("%s value_template = %v", key, p["value_template"])
		}
	}
}

// TestDiscoveryControlsDisabledByDefault covers the Phase-4 behaviour: with
// ControlsEnabled unset, only the 33 read-only entities are published and no
// message carries a command_topic.
func TestDiscoveryControlsDisabledByDefault(t *testing.T) {
	byTopic := mustBuildDiscoveryFor(t, testConfig())
	if len(byTopic) != 33 {
		t.Fatalf("got %d discovery messages, want 33", len(byTopic))
	}
	for topic, p := range byTopic {
		if _, present := p["command_topic"]; present {
			t.Errorf("%s should not carry command_topic when controls are disabled", topic)
		}
	}
	for _, key := range []string{"set_charge_current", "set_discharge_current", "optimal_income", "rtc_sync"} {
		for _, comp := range []string{"number", "switch", "button"} {
			topic := "homeassistant/" + comp + "/1234567890_" + key + "/config"
			if _, present := byTopic[topic]; present {
				t.Errorf("command entity %q should not be published when controls are disabled", key)
			}
		}
	}
}

// TestDiscoveryControlsEnabled verifies the four command entities and their
// component-specific discovery keys when ControlsEnabled is true.
func TestDiscoveryControlsEnabled(t *testing.T) {
	c := testConfig()
	c.ControlsEnabled = true
	byTopic := mustBuildDiscoveryFor(t, c)
	if len(byTopic) != 37 {
		t.Fatalf("got %d discovery messages, want 37", len(byTopic))
	}

	num := byTopic["homeassistant/number/1234567890_set_charge_current/config"]
	if num == nil {
		t.Fatal("missing set_charge_current number")
	}
	if num["command_topic"] != "~/set_charge_current/set" {
		t.Errorf("command_topic = %v", num["command_topic"])
	}
	if num["min"] != float64(0) || num["max"] != float64(60) || num["step"] != 0.1 {
		t.Errorf("min/max/step = %v / %v / %v", num["min"], num["max"], num["step"])
	}
	if num["mode"] != "box" || num["unit_of_measurement"] != "A" {
		t.Errorf("mode/unit = %v / %v", num["mode"], num["unit_of_measurement"])
	}
	if num["value_template"] != "{{ value_json.set_charge_current }}" {
		t.Errorf("value_template = %v", num["value_template"])
	}
	if num["state_topic"] != "~/state" {
		t.Errorf("state_topic = %v", num["state_topic"])
	}

	dis := byTopic["homeassistant/number/1234567890_set_discharge_current/config"]
	if dis == nil || dis["command_topic"] != "~/set_discharge_current/set" {
		t.Errorf("set_discharge_current command_topic = %v", dis["command_topic"])
	}

	sw := byTopic["homeassistant/switch/1234567890_optimal_income/config"]
	if sw == nil {
		t.Fatal("missing optimal_income switch")
	}
	if sw["command_topic"] != "~/optimal_income/set" {
		t.Errorf("switch command_topic = %v", sw["command_topic"])
	}
	if sw["payload_on"] != "ON" || sw["payload_off"] != "OFF" {
		t.Errorf("payload_on/off = %v / %v", sw["payload_on"], sw["payload_off"])
	}
	if sw["state_on"] != "ON" || sw["state_off"] != "OFF" {
		t.Errorf("state_on/off = %v / %v", sw["state_on"], sw["state_off"])
	}
	if sw["value_template"] != "{{ value_json.optimal_income }}" {
		t.Errorf("switch value_template = %v", sw["value_template"])
	}
	if sw["state_topic"] != "~/state" {
		t.Errorf("switch state_topic = %v", sw["state_topic"])
	}

	btn := byTopic["homeassistant/button/1234567890_rtc_sync/config"]
	if btn == nil {
		t.Fatal("missing rtc_sync button")
	}
	if btn["command_topic"] != "~/rtc_sync/set" {
		t.Errorf("button command_topic = %v", btn["command_topic"])
	}
	if btn["payload_press"] != "PRESS" {
		t.Errorf("payload_press = %v", btn["payload_press"])
	}
	if btn["entity_category"] != "diagnostic" {
		t.Errorf("button entity_category = %v", btn["entity_category"])
	}
	if _, present := btn["state_topic"]; present {
		t.Errorf("button should not carry state_topic, got %v", btn["state_topic"])
	}
	if _, present := btn["value_template"]; present {
		t.Errorf("button should not carry value_template, got %v", btn["value_template"])
	}
	// Button keeps the shared device/availability block.
	if btn["availability_topic"] != "~/availability" || btn["device"] == nil {
		t.Errorf("button availability/device = %v / %v", btn["availability_topic"], btn["device"])
	}
}
