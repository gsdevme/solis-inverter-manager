package homeassistant

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
)

// stateJSONTags returns the set of json tags declared on the State DTO.
func stateJSONTags(t *testing.T) map[string]bool {
	t.Helper()
	tags := map[string]bool{}
	rt := reflect.TypeOf(State{})
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			t.Fatalf("field %s has no usable json tag (%q)", rt.Field(i).Name, tag)
		}
		tags[name] = true
	}
	return tags
}

// TestStateTagsEqualEntityKeys is the load-bearing round-trip contract: every
// stateful entity has a state field and every state field has an entity. Buttons
// are stateless (they publish a press payload, never read from ~/state), so they
// are exempt from the "every key has a state field" direction.
func TestStateTagsEqualEntityKeys(t *testing.T) {
	tags := stateJSONTags(t)
	keys := map[string]bool{}
	for _, e := range Entities() {
		if e.Component == Button {
			// Stateless: has no json tag on the State DTO by design.
			if tags[e.Key] {
				t.Errorf("button %q must not have a State field", e.Key)
			}
			continue
		}
		keys[e.Key] = true
	}
	if len(keys) != 36 {
		t.Errorf("got %d stateful entity keys, want 36", len(keys))
	}
	if len(tags) != 36 {
		t.Errorf("got %d state json tags, want 36", len(tags))
	}
	for k := range keys {
		if !tags[k] {
			t.Errorf("entity key %q has no State field", k)
		}
	}
	for tag := range tags {
		if !keys[tag] {
			t.Errorf("State field %q has no (stateful) entity", tag)
		}
	}
}

func sampleTelemetry() inverter.Telemetry {
	return inverter.Telemetry{
		Time: time.Date(2026, 9, 12, 13, 45, 30, 0, time.UTC),
		Battery: inverter.Battery{
			VoltageV: 51.2, CurrentA: -3.4, Charging: false, SOCPercent: 87, SOHPercent: 99,
			BMSVoltageV: 51.25, BMSCurrentA: -3.4, PowerW: -174,
		},
		PV:     inverter.PV{PV1VoltageV: 320.5, PV1CurrentA: 4.1, PV2VoltageV: 0, PV2CurrentA: 0, TotalPowerW: 1314},
		Grid:   inverter.Grid{PowerW: -250, TotalImportKWh: 1234, ImportTodayKWh: 5.6, TotalExportKWh: 890, ExportTodayKWh: 7.8},
		AC:     inverter.AC{ActivePowerW: 1200, TemperatureC: 34.5, FrequencyHz: 50.01, HouseLoadW: 450},
		Energy: inverter.Energy{GenerationTodayKWh: 12.3, GenerationYesterdayKWh: 20.1, BatteryTotalChargeKWh: 500, BatteryChargeTodayKWh: 3.2, BatteryTotalDischargeKWh: 480, BatteryDischargeTodayKWh: 2.9},
		System: inverter.System{Status: 3, OperatingStatus: 4099, WorkMode: inverter.DecodeWorkMode(inverter.WorkModeTimedOn)},
	}
}

func TestBuildStateTopicAndValues(t *testing.T) {
	c := testConfig()
	sp := Setpoints{SetChargeCurrent: 25.5, SetDischargeCurrent: 40, OptimalIncome: true}
	msg, err := c.BuildState(sampleTelemetry(), 90*time.Second, sp)
	if err != nil {
		t.Fatalf("BuildState: %v", err)
	}
	if msg.Topic != c.StateTopic() {
		t.Errorf("topic = %q, want %q", msg.Topic, c.StateTopic())
	}

	var got map[string]any
	if err := json.Unmarshal(msg.Payload, &got); err != nil {
		t.Fatalf("bad payload: %v", err)
	}

	// Key set equals the stateful entity keys (buttons carry no state field).
	keys := map[string]bool{}
	for _, e := range Entities() {
		if e.Component == Button {
			continue
		}
		keys[e.Key] = true
	}
	if len(got) != len(keys) {
		t.Errorf("state has %d keys, want %d", len(got), len(keys))
	}
	for k := range got {
		if !keys[k] {
			t.Errorf("unexpected state key %q", k)
		}
	}

	// Signed values preserved.
	if got["battery_power"] != float64(-174) {
		t.Errorf("battery_power = %v, want -174", got["battery_power"])
	}
	if got["grid_power"] != float64(-250) {
		t.Errorf("grid_power = %v, want -250", got["grid_power"])
	}
	// RFC3339 rtc.
	if got["rtc"] != "2026-09-12T13:45:30Z" {
		t.Errorf("rtc = %v", got["rtc"])
	}
	// Drift in seconds.
	if got["rtc_drift"] != float64(90) {
		t.Errorf("rtc_drift = %v, want 90", got["rtc_drift"])
	}
	// battery_charging bool.
	if got["battery_charging"] != false {
		t.Errorf("battery_charging = %v, want false", got["battery_charging"])
	}
	// work_mode label.
	if got["work_mode"] != "Optimal income ON" {
		t.Errorf("work_mode = %v, want Optimal income ON", got["work_mode"])
	}
	// Raw system enums as numbers.
	if got["status"] != float64(3) || got["operating_status"] != float64(4099) {
		t.Errorf("status/operating_status = %v / %v", got["status"], got["operating_status"])
	}
	// Setpoints folded in from Setpoints.
	if got["set_charge_current"] != 25.5 || got["set_discharge_current"] != float64(40) {
		t.Errorf("setpoints = %v / %v", got["set_charge_current"], got["set_discharge_current"])
	}
	// OptimalIncome bool rendered as ON/OFF string.
	if got["optimal_income"] != "ON" {
		t.Errorf("optimal_income = %v, want ON", got["optimal_income"])
	}
	// rtc_sync (button) must NOT appear in the state document.
	if _, present := got["rtc_sync"]; present {
		t.Errorf("state should not carry rtc_sync, got %v", got["rtc_sync"])
	}
}

func TestBuildStateOptimalIncomeOff(t *testing.T) {
	msg, err := testConfig().BuildState(sampleTelemetry(), 0, Setpoints{OptimalIncome: false})
	if err != nil {
		t.Fatalf("BuildState: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(msg.Payload, &got); err != nil {
		t.Fatalf("bad payload: %v", err)
	}
	if got["optimal_income"] != "OFF" {
		t.Errorf("optimal_income = %v, want OFF", got["optimal_income"])
	}
}

func TestWorkModeLabel(t *testing.T) {
	cases := []struct {
		raw  uint16
		want string
	}{
		{inverter.WorkModeTimedOn, "Optimal income ON"},
		{inverter.WorkModeTimedOff, "Optimal income OFF"},
		{1, "self_use"},
		{2, "timed"},
		{3, "self_use+timed"},
		{0, "0"},
		{1 << 5, "allow_grid_charge"},
	}
	for _, tc := range cases {
		if got := workModeLabel(inverter.DecodeWorkMode(tc.raw)); got != tc.want {
			t.Errorf("workModeLabel(%d) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
