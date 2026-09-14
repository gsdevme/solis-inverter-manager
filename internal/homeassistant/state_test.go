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
	if len(keys) != 42 {
		t.Errorf("got %d stateful entity keys, want 42", len(keys))
	}
	if len(tags) != 42 {
		t.Errorf("got %d state json tags, want 42", len(tags))
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
	if got["work_mode"] != "Self Use" {
		t.Errorf("work_mode = %v, want Self Use", got["work_mode"])
	}
	// Raw system enums as numbers.
	if got["status"] != float64(3) || got["operating_status"] != float64(4099) {
		t.Errorf("status/operating_status = %v / %v", got["status"], got["operating_status"])
	}
	// Setpoints folded in from Setpoints.
	if got["set_charge_current"] != 25.5 || got["set_discharge_current"] != float64(40) {
		t.Errorf("setpoints = %v / %v", got["set_charge_current"], got["set_discharge_current"])
	}
	// OptimalIncome bool rendered as the select's Run/Stop option.
	if got["optimal_income"] != "Run" {
		t.Errorf("optimal_income = %v, want Run", got["optimal_income"])
	}
	// rtc_sync (button) must NOT appear in the state document.
	if _, present := got["rtc_sync"]; present {
		t.Errorf("state should not carry rtc_sync, got %v", got["rtc_sync"])
	}
}

// TestBuildStateDerivedBatteryPower covers the two unsigned convenience sensors
// split out of the signed battery_power: exactly one is non-zero at a time, and
// the signed sensor is untouched.
func TestBuildStateDerivedBatteryPower(t *testing.T) {
	cases := []struct {
		name                      string
		powerW                    float64
		wantCharge, wantDischarge float64
	}{
		{"discharging", -174, 0, 174},
		{"charging", 2100, 2100, 0},
		{"idle", 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tel := sampleTelemetry()
			tel.Battery.PowerW = tc.powerW
			msg, err := testConfig().BuildState(tel, 0, Setpoints{})
			if err != nil {
				t.Fatalf("BuildState: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(msg.Payload, &got); err != nil {
				t.Fatalf("bad payload: %v", err)
			}
			if got["battery_power"] != tc.powerW {
				t.Errorf("battery_power = %v, want %v", got["battery_power"], tc.powerW)
			}
			if got["battery_charge_power"] != tc.wantCharge {
				t.Errorf("battery_charge_power = %v, want %v", got["battery_charge_power"], tc.wantCharge)
			}
			if got["battery_discharge_power"] != tc.wantDischarge {
				t.Errorf("battery_discharge_power = %v, want %v", got["battery_discharge_power"], tc.wantDischarge)
			}
		})
	}
}

func TestBuildStateOptimalIncomeStop(t *testing.T) {
	msg, err := testConfig().BuildState(sampleTelemetry(), 0, Setpoints{OptimalIncome: false})
	if err != nil {
		t.Fatalf("BuildState: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(msg.Payload, &got); err != nil {
		t.Fatalf("bad payload: %v", err)
	}
	if got["optimal_income"] != "Stop" {
		t.Errorf("optimal_income = %v, want Stop", got["optimal_income"])
	}
}

func TestWorkModeLabel(t *testing.T) {
	cases := []struct {
		raw  uint16
		want string
	}{
		// Both observed values set bit 0, so both read as the storage mode.
		{inverter.WorkModeTimedOn, "Self Use"},
		{inverter.WorkModeTimedOff, "Self Use"},
		{1, "Self Use"},
		{3, "Self Use"},
		// Anything without bit 0 falls back to the flag list or the raw value.
		{2, "timed"},
		{0, "0"},
		{1 << 5, "allow_grid_charge"},
	}
	for _, tc := range cases {
		if got := workModeLabel(inverter.DecodeWorkMode(tc.raw)); got != tc.want {
			t.Errorf("workModeLabel(%d) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

// touSlots is the owner's live schedule: the Time-of-Use tariff split across
// slots 1 and 2 at midnight, plus an afternoon boost in slot 3.
func touSlots() inverter.TimedSlots {
	clock := func(hour, minute uint8) inverter.Clock {
		return inverter.Clock{Hour: hour, Minute: minute}
	}
	return inverter.TimedSlots{
		{Charge: inverter.TimedWindow{Start: clock(23, 31), End: clock(0, 0)}},
		{Charge: inverter.TimedWindow{Start: clock(0, 0), End: clock(5, 29)}},
		{Charge: inverter.TimedWindow{Start: clock(14, 2), End: clock(14, 56)}},
	}
}

// TestBuildStateSchedule covers the derived schedule fields for a configured
// schedule: the midnight-split Time-of-Use pair reads as one window, slot 3
// reads as the boost and as the matching select option, and boost_ends_at is the
// RFC3339 form of the caller-supplied end (BuildState is pure and never resolves
// it itself).
func TestBuildStateSchedule(t *testing.T) {
	endsAt := time.Date(2026, 9, 13, 14, 56, 0, 0, time.UTC)
	msg, err := testConfig().BuildState(sampleTelemetry(), 0, Setpoints{
		Slots:       touSlots(),
		BoostEndsAt: endsAt,
	})
	if err != nil {
		t.Fatalf("BuildState: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(msg.Payload, &got); err != nil {
		t.Fatalf("bad payload: %v", err)
	}
	if got["tou_window"] != "23:31–05:29" {
		t.Errorf("tou_window = %v, want 23:31–05:29", got["tou_window"])
	}
	if got["boost"] != "Charge until 14:56" {
		t.Errorf("boost = %v, want Charge until 14:56", got["boost"])
	}
	if got["boost_ends_at"] != endsAt.Format(time.RFC3339) {
		t.Errorf("boost_ends_at = %v, want %s", got["boost_ends_at"], endsAt.Format(time.RFC3339))
	}
	// 14:02-14:56 is 54 minutes, which snaps up to the 60 min option.
	if got["boost_select"] != "Charge 60 min" {
		t.Errorf("boost_select = %v, want Charge 60 min", got["boost_select"])
	}
}

// TestBuildStateBoostSelectUnmappable covers a boost the select cannot express:
// a 90-minute window is longer than any option, so boost_select publishes null
// (HA "unknown") while sensor.boost still reports the real window.
func TestBuildStateBoostSelectUnmappable(t *testing.T) {
	slots := touSlots()
	slots[2].Charge.End = inverter.Clock{Hour: 15, Minute: 32}
	msg, err := testConfig().BuildState(sampleTelemetry(), 0, Setpoints{Slots: slots})
	if err != nil {
		t.Fatalf("BuildState: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(msg.Payload, &got); err != nil {
		t.Fatalf("bad payload: %v", err)
	}
	if _, present := got["boost_select"]; !present {
		t.Fatal("state is missing key \"boost_select\"; it must never be omitted")
	}
	if got["boost_select"] != nil {
		t.Errorf("boost_select = %v, want null", got["boost_select"])
	}
}

// TestBuildStateScheduleUnset covers an unconfigured schedule: tou_window and
// boost_ends_at marshal as JSON null rather than being omitted, because Home
// Assistant needs the keys present to resolve the entities to "unknown".
func TestBuildStateScheduleUnset(t *testing.T) {
	msg, err := testConfig().BuildState(sampleTelemetry(), 0, Setpoints{})
	if err != nil {
		t.Fatalf("BuildState: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(msg.Payload, &got); err != nil {
		t.Fatalf("bad payload: %v", err)
	}
	for _, key := range []string{"tou_window", "boost", "boost_ends_at", "boost_select"} {
		if _, present := got[key]; !present {
			t.Errorf("state is missing key %q; it must never be omitted", key)
		}
	}
	if got["tou_window"] != nil {
		t.Errorf("tou_window = %v, want null", got["tou_window"])
	}
	if got["boost_ends_at"] != nil {
		t.Errorf("boost_ends_at = %v, want null", got["boost_ends_at"])
	}
	if got["boost"] != "Off" {
		t.Errorf("boost = %v, want Off", got["boost"])
	}
	// An empty slot 3 is a real select state, not unknown.
	if got["boost_select"] != "Off" {
		t.Errorf("boost_select = %v, want Off", got["boost_select"])
	}
}
