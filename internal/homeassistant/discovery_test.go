package homeassistant

import (
	"encoding/json"
	"testing"
)

func testConfig() Config {
	return Config{DiscoveryPrefix: "homeassistant", TopicPrefix: "solis", Serial: "1234567890"}
}

func mustBuildDiscovery(t *testing.T) map[string]map[string]any {
	t.Helper()
	msgs, err := testConfig().BuildDiscovery()
	if err != nil {
		t.Fatalf("BuildDiscovery: %v", err)
	}
	if len(msgs) != len(Entities()) {
		t.Fatalf("got %d messages, want %d", len(msgs), len(Entities()))
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
