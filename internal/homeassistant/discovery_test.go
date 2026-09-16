package homeassistant

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func testConfig() Config {
	return Config{
		DiscoveryPrefix: "homeassistant",
		TopicPrefix:     "solis",
		Serial:          "1234567890",
		ObjectIDPrefix:  "solis_inverter",
	}
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
	if p["unique_id"] != "1234567890_battery_soc" {
		t.Errorf("unique_id = %v", p["unique_id"])
	}
	if p["object_id"] != "solis_inverter_battery_soc" {
		t.Errorf("object_id = %v", p["object_id"])
	}
	if p["default_entity_id"] != "sensor.solis_inverter_battery_soc" {
		t.Errorf("default_entity_id = %v", p["default_entity_id"])
	}
	if p["value_template"] != "{{ value_json.battery_soc }}" {
		t.Errorf("value_template = %v", p["value_template"])
	}
	if p["device_class"] != "battery" || p["state_class"] != "measurement" || p["unit_of_measurement"] != "%" {
		t.Errorf("classes = %v / %v / %v", p["device_class"], p["state_class"], p["unit_of_measurement"])
	}
}

// TestDiscoveryEntityIDPrefix pins the entity-id derivation: object_id and
// default_entity_id follow ObjectIDPrefix, unique_id stays serial-scoped, and the
// discovery topic keeps the serial node segment.
func TestDiscoveryEntityIDPrefix(t *testing.T) {
	c := testConfig()
	c.ObjectIDPrefix = "house_solis"
	c.ControlsEnabled = true
	byTopic := mustBuildDiscoveryFor(t, c)

	cases := map[string]string{
		"homeassistant/sensor/1234567890_battery_soc/config":             "sensor.house_solis_battery_soc",
		"homeassistant/binary_sensor/1234567890_battery_charging/config": "binary_sensor.house_solis_battery_charging",
		"homeassistant/number/1234567890_set_charge_current/config":      "number.house_solis_set_charge_current",
		"homeassistant/select/1234567890_optimal_income/config":          "select.house_solis_optimal_income",
		"homeassistant/button/1234567890_rtc_sync/config":                "button.house_solis_rtc_sync",
	}
	for topic, wantEntityID := range cases {
		p := byTopic[topic]
		if p == nil {
			t.Fatalf("missing %s", topic)
		}
		if p["default_entity_id"] != wantEntityID {
			t.Errorf("%s default_entity_id = %v, want %v", topic, p["default_entity_id"], wantEntityID)
		}
		_, key, _ := strings.Cut(wantEntityID, ".")
		if p["object_id"] != key {
			t.Errorf("%s object_id = %v, want %v", topic, p["object_id"], key)
		}
	}

	// unique_id is registry-stable and must not follow the prefix.
	if uid := byTopic["homeassistant/sensor/1234567890_battery_soc/config"]["unique_id"]; uid != "1234567890_battery_soc" {
		t.Errorf("unique_id = %v, want 1234567890_battery_soc", uid)
	}
}

// TestDiscoveryEntityIDPrefixFallback covers a Config built without an explicit
// prefix: it must still emit a valid entity id, never a bare "_key".
func TestDiscoveryEntityIDPrefixFallback(t *testing.T) {
	c := testConfig()
	c.ObjectIDPrefix = ""
	p := mustBuildDiscoveryFor(t, c)["homeassistant/sensor/1234567890_battery_soc/config"]
	if p["object_id"] != DefaultObjectIDPrefix+"_battery_soc" {
		t.Errorf("object_id = %v", p["object_id"])
	}
	if p["default_entity_id"] != "sensor."+DefaultObjectIDPrefix+"_battery_soc" {
		t.Errorf("default_entity_id = %v", p["default_entity_id"])
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

// TestDiscoveryBMSFaultBits covers the per-bit fault sensors: each is a
// problem-class diagnostic binary_sensor reading its own boolean from the shared
// state document, so an active protection shows up in Home Assistant as a
// problem rather than as a number a template has to decode.
func TestDiscoveryBMSFaultBits(t *testing.T) {
	byTopic := mustBuildDiscovery(t)
	keys := []string{
		"bms_over_voltage", "bms_under_voltage", "bms_over_temp", "bms_under_temp",
		"bms_charge_over_temp", "bms_charge_under_temp", "bms_discharge_over_current",
		"bms_charge_over_current", "bms_internal_protection", "bms_module_unbalanced",
	}
	for _, key := range keys {
		p := byTopic["homeassistant/binary_sensor/1234567890_"+key+"/config"]
		if p == nil {
			t.Fatalf("missing %s binary_sensor", key)
		}
		if p["device_class"] != "problem" {
			t.Errorf("%s device_class = %v, want problem", key, p["device_class"])
		}
		if p["entity_category"] != "diagnostic" {
			t.Errorf("%s entity_category = %v, want diagnostic", key, p["entity_category"])
		}
		if want := "{{ 'ON' if value_json." + key + " else 'OFF' }}"; p["value_template"] != want {
			t.Errorf("%s value_template = %v, want %q", key, p["value_template"], want)
		}
	}
}

// TestDiscoverySOCThresholdMirrors pins the two read-only SOC settings as
// percentage diagnostics with neither device_class nor state_class: they are
// near-static configuration, so recording long-term statistics for them would be
// noise, and a "battery" device_class would make Home Assistant read the
// threshold as the device's remaining charge.
func TestDiscoverySOCThresholdMirrors(t *testing.T) {
	byTopic := mustBuildDiscovery(t)
	for _, key := range []string{"overdischarge_soc", "force_charge_soc"} {
		p := byTopic["homeassistant/sensor/1234567890_"+key+"/config"]
		if p == nil {
			t.Fatalf("missing %s sensor", key)
		}
		if _, present := p["device_class"]; present {
			t.Errorf("%s should not carry device_class, got %v", key, p["device_class"])
		}
		if p["unit_of_measurement"] != "%" {
			t.Errorf("%s unit_of_measurement = %v, want %%", key, p["unit_of_measurement"])
		}
		if p["entity_category"] != "diagnostic" {
			t.Errorf("%s entity_category = %v, want diagnostic", key, p["entity_category"])
		}
		if _, present := p["state_class"]; present {
			t.Errorf("%s should not carry state_class, got %v", key, p["state_class"])
		}
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
	for _, key := range []string{"status", "status_text", "operating_status", "work_mode", "bms_fault_1", "bms_fault_2"} {
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
// ControlsEnabled unset, only the 57 read-only entities are published and no
// message carries a command_topic.
func TestDiscoveryControlsDisabledByDefault(t *testing.T) {
	byTopic := mustBuildDiscoveryFor(t, testConfig())
	if len(byTopic) != 57 {
		t.Fatalf("got %d discovery messages, want 57", len(byTopic))
	}
	for topic, p := range byTopic {
		if _, present := p["command_topic"]; present {
			t.Errorf("%s should not carry command_topic when controls are disabled", topic)
		}
	}
	for _, key := range []string{"set_charge_current", "set_discharge_current", "optimal_income", "boost_select", "rtc_sync"} {
		for _, comp := range []string{"number", "switch", "select", "button"} {
			topic := "homeassistant/" + comp + "/1234567890_" + key + "/config"
			if _, present := byTopic[topic]; present {
				t.Errorf("command entity %q should not be published when controls are disabled", key)
			}
		}
	}
}

// TestDiscoveryControlsEnabled verifies the five command entities and their
// component-specific discovery keys when ControlsEnabled is true.
func TestDiscoveryControlsEnabled(t *testing.T) {
	c := testConfig()
	c.ControlsEnabled = true
	byTopic := mustBuildDiscoveryFor(t, c)
	if len(byTopic) != 62 {
		t.Fatalf("got %d discovery messages, want 62", len(byTopic))
	}

	num := byTopic["homeassistant/number/1234567890_set_charge_current/config"]
	if num == nil {
		t.Fatal("missing set_charge_current number")
	}
	if num["command_topic"] != "~/set_charge_current/set" {
		t.Errorf("command_topic = %v", num["command_topic"])
	}
	if num["min"] != float64(0) || num["max"] != 62.5 || num["step"] != 0.1 {
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

	sel := byTopic["homeassistant/select/1234567890_optimal_income/config"]
	if sel == nil {
		t.Fatal("missing optimal_income select")
	}
	if sel["command_topic"] != "~/optimal_income/set" {
		t.Errorf("select command_topic = %v", sel["command_topic"])
	}
	if got := optionsOf(t, sel); !reflect.DeepEqual(got, []string{"Run", "Stop"}) {
		t.Errorf("optimal_income options = %v, want [Run Stop]", got)
	}
	if sel["value_template"] != "{{ value_json.optimal_income }}" {
		t.Errorf("select value_template = %v", sel["value_template"])
	}
	if sel["state_topic"] != "~/state" {
		t.Errorf("select state_topic = %v", sel["state_topic"])
	}
	// A select is not a switch: the on/off payload keys must be gone entirely.
	for _, absent := range []string{"payload_on", "payload_off", "state_on", "state_off"} {
		if _, present := sel[absent]; present {
			t.Errorf("optimal_income select should not carry %s, got %v", absent, sel[absent])
		}
	}
	if _, present := byTopic["homeassistant/switch/1234567890_optimal_income/config"]; present {
		t.Error("optimal_income must no longer be published as a switch")
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

// optionsOf reads a select payload's options array back as strings.
func optionsOf(t *testing.T, p map[string]any) []string {
	t.Helper()
	raw, ok := p["options"].([]any)
	if !ok {
		t.Fatalf("options = %v, want an array of strings", p["options"])
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("option %v is not a string", v)
		}
		out = append(out, s)
	}
	return out
}

// TestDiscoveryBoostSelect covers the boost select: the nine schedule options in
// display order, reading its state from the shared state document.
func TestDiscoveryBoostSelect(t *testing.T) {
	c := testConfig()
	c.ControlsEnabled = true
	byTopic := mustBuildDiscoveryFor(t, c)

	p := byTopic["homeassistant/select/1234567890_boost_select/config"]
	if p == nil {
		t.Fatal("missing boost_select select")
	}
	if p["name"] != "Boost control" {
		t.Errorf("name = %v, want Boost control", p["name"])
	}
	if p["command_topic"] != "~/boost_select/set" {
		t.Errorf("command_topic = %v", p["command_topic"])
	}
	if p["state_topic"] != "~/state" {
		t.Errorf("state_topic = %v", p["state_topic"])
	}
	if p["value_template"] != "{{ value_json.boost_select }}" {
		t.Errorf("value_template = %v", p["value_template"])
	}
	want := []string{
		"Off",
		"Charge 15 min", "Charge 30 min", "Charge 45 min", "Charge 60 min",
		"Discharge 15 min", "Discharge 30 min", "Discharge 45 min", "Discharge 60 min",
	}
	if got := optionsOf(t, p); !reflect.DeepEqual(got, want) {
		t.Errorf("boost_select options = %v, want %v", got, want)
	}
}

// TestDiscoveryTopic pins the topic helper to the shape every discovery message
// and every removal uses.
func TestDiscoveryTopic(t *testing.T) {
	got := testConfig().DiscoveryTopic(Select, "boost_select")
	if want := "homeassistant/select/1234567890_boost_select/config"; got != want {
		t.Errorf("DiscoveryTopic = %q, want %q", got, want)
	}
}

// TestBuildDiscoveryRemovals covers the stale-entity cleanup: one empty retained
// payload to the retired switch's config topic, emitted whether or not controls
// are enabled.
func TestBuildDiscoveryRemovals(t *testing.T) {
	for _, controls := range []bool{false, true} {
		c := testConfig()
		c.ControlsEnabled = controls
		msgs := c.BuildDiscoveryRemovals()
		if len(msgs) != 1 {
			t.Fatalf("ControlsEnabled=%v: got %d removals, want 1", controls, len(msgs))
		}
		if want := "homeassistant/switch/1234567890_optimal_income/config"; msgs[0].Topic != want {
			t.Errorf("removal topic = %q, want %q", msgs[0].Topic, want)
		}
		if len(msgs[0].Payload) != 0 {
			t.Errorf("removal payload = %q, want empty", msgs[0].Payload)
		}
	}
}

// TestDiscoveryWorkModeRenamed pins the B2 rename: the work_mode sensor keeps its
// key and diagnostic category but presents as "Energy storage mode".
func TestDiscoveryWorkModeRenamed(t *testing.T) {
	p := mustBuildDiscovery(t)["homeassistant/sensor/1234567890_work_mode/config"]
	if p == nil {
		t.Fatal("missing work_mode sensor")
	}
	if p["name"] != "Energy storage mode" {
		t.Errorf("work_mode name = %v, want Energy storage mode", p["name"])
	}
	if p["entity_category"] != "diagnostic" {
		t.Errorf("work_mode entity_category = %v", p["entity_category"])
	}
}

// TestDiscoverySuggestedDisplayPrecision pins the display precision to the
// register scale: ÷10 registers publish 1, ÷100 registers publish 2, so HA keeps
// the trailing zero (49.0 V, not 49 V). Integer-scale entities carry none.
func TestDiscoverySuggestedDisplayPrecision(t *testing.T) {
	byTopic := mustBuildDiscovery(t)
	want := map[string]float64{
		"battery_voltage":                1,
		"battery_current":                1,
		"bms_voltage":                    2,
		"bms_current":                    1,
		"bms_charge_current_limit":       1,
		"bms_discharge_current_limit":    1,
		"inverter_max_charge_current":    1,
		"inverter_max_discharge_current": 1,
		"pv1_voltage":                    1,
		"pv1_current":                    1,
		"pv2_voltage":                    1,
		"pv2_current":                    1,
		"grid_import_today":              1,
		"grid_export_today":              1,
		"inverter_temperature":           1,
		"grid_frequency":                 2,
		"generation_today":               1,
		"generation_yesterday":           1,
		"battery_charge_today":           1,
		"battery_discharge_today":        1,
	}
	for key, prec := range want {
		p := byTopic["homeassistant/sensor/1234567890_"+key+"/config"]
		if got := p["suggested_display_precision"]; got != prec {
			t.Errorf("%s suggested_display_precision = %v, want %v", key, got, prec)
		}
	}
	for _, key := range []string{"battery_power", "battery_soc", "grid_total_import", "house_load", "status"} {
		p := byTopic["homeassistant/sensor/1234567890_"+key+"/config"]
		if got, present := p["suggested_display_precision"]; present {
			t.Errorf("%s should carry no suggested_display_precision, got %v", key, got)
		}
	}
}
