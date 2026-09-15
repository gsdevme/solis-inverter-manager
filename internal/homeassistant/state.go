package homeassistant

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
	"github.com/gsdevme/solis-inverter-manager/internal/schedule"
)

// State is the flat state document published to the shared state topic. Every
// json tag equals an Entity.Key, which is the contract each entity's
// value_template relies on (value_json.<Key>). No entity may exist without a
// field here, and no field here without an entity.
type State struct {
	BatteryVoltage float64 `json:"battery_voltage"`
	BatteryCurrent float64 `json:"battery_current"`
	BatteryPower   float64 `json:"battery_power"`
	// BatteryChargePower and BatteryDischargePower are the unsigned halves of
	// BatteryPower, for Home Assistant integrations that meter each direction
	// separately. Exactly one is non-zero at a time; see splitBatteryPower.
	BatteryChargePower    float64 `json:"battery_charge_power"`
	BatteryDischargePower float64 `json:"battery_discharge_power"`
	BatteryCharging       bool    `json:"battery_charging"`
	BatterySOC            float64 `json:"battery_soc"`
	BatterySOH            float64 `json:"battery_soh"`
	BMSVoltage            float64 `json:"bms_voltage"`
	BMSCurrent            float64 `json:"bms_current"`
	BMSChargeCurrentLimit float64 `json:"bms_charge_current_limit"`

	BMSDischargeCurrentLimit float64 `json:"bms_discharge_current_limit"`

	PV1Voltage   float64 `json:"pv1_voltage"`
	PV1Current   float64 `json:"pv1_current"`
	PV2Voltage   float64 `json:"pv2_voltage"`
	PV2Current   float64 `json:"pv2_current"`
	PVTotalPower float64 `json:"pv_total_power"`

	GridPower       float64 `json:"grid_power"`
	GridTotalImport float64 `json:"grid_total_import"`
	GridImportToday float64 `json:"grid_import_today"`
	GridTotalExport float64 `json:"grid_total_export"`
	GridExportToday float64 `json:"grid_export_today"`

	ACActivePower       float64 `json:"ac_active_power"`
	InverterTemperature float64 `json:"inverter_temperature"`
	GridFrequency       float64 `json:"grid_frequency"`
	HouseLoad           float64 `json:"house_load"`

	GenerationToday       float64 `json:"generation_today"`
	GenerationYesterday   float64 `json:"generation_yesterday"`
	BatteryTotalCharge    float64 `json:"battery_total_charge"`
	BatteryChargeToday    float64 `json:"battery_charge_today"`
	BatteryTotalDischarge float64 `json:"battery_total_discharge"`
	BatteryDischargeToday float64 `json:"battery_discharge_today"`

	Status          uint16  `json:"status"`
	OperatingStatus uint16  `json:"operating_status"`
	WorkMode        string  `json:"work_mode"`
	RTC             string  `json:"rtc"`
	RTCDrift        float64 `json:"rtc_drift"`

	// The derived schedule fields. TouWindow, BoostEndsAt and BoostSelect are
	// pointers and carry no omitempty, so a schedule they cannot express publishes
	// an explicit null and Home Assistant resolves the entity to "unknown" rather
	// than keeping a stale value. BoostSelect is null only for a boost window that
	// matches no select option; sensor.boost still reports the real window.
	TouWindow   *string `json:"tou_window"`
	Boost       string  `json:"boost"`
	BoostEndsAt *string `json:"boost_ends_at"`
	BoostSelect *string `json:"boost_select"`

	SetChargeCurrent    float64 `json:"set_charge_current"`
	SetDischargeCurrent float64 `json:"set_discharge_current"`
	// The inverter's configured ceiling, read-only (holding 43117/43118).
	InverterMaxChargeCurrent    float64 `json:"inverter_max_charge_current"`
	InverterMaxDischargeCurrent float64 `json:"inverter_max_discharge_current"`
	// OptimalIncome is the select's option: "Run" or "Stop".
	OptimalIncome string `json:"optimal_income"`
}

// Setpoints is the current writable-control state, read from the holding bank
// by the caller and folded into the shared state document. Slots carries the
// three timed slots the derived schedule sensors are rendered from, and
// BoostEndsAt the boost's resolved end instant — resolved by the caller, because
// this package must not read the wall clock; it is the zero time when no boost
// is configured.
type Setpoints struct {
	SetChargeCurrent    float64
	SetDischargeCurrent float64
	// MaxChargeCurrent and MaxDischargeCurrent are the inverter's own configured
	// ceiling (holding 43117/43118), published read-only.
	MaxChargeCurrent    float64
	MaxDischargeCurrent float64
	// OptimalIncome is the work-mode timed bit: true publishes the select's
	// "Run" option, false its "Stop".
	OptimalIncome bool
	Slots         inverter.TimedSlots
	BoostEndsAt   time.Time
}

// BuildState marshals a decoded Telemetry (plus the externally computed clock
// drift) into the retained state document. The topic is the shared StateTopic.
// drift is supplied by the caller because this package is pure and must not read
// the wall clock.
func (c Config) BuildState(t inverter.Telemetry, drift time.Duration, sp Setpoints) (Message, error) {
	optimalIncome := "Stop"
	if sp.OptimalIncome {
		optimalIncome = "Run"
	}
	chargeW, dischargeW := splitBatteryPower(t.Battery.PowerW)
	st := State{
		BatteryVoltage:        t.Battery.VoltageV,
		BatteryCurrent:        t.Battery.CurrentA,
		BatteryPower:          t.Battery.PowerW,
		BatteryChargePower:    chargeW,
		BatteryDischargePower: dischargeW,
		BatteryCharging:       t.Battery.Charging,
		BatterySOC:            t.Battery.SOCPercent,
		BatterySOH:            t.Battery.SOHPercent,
		BMSVoltage:            t.Battery.BMSVoltageV,
		BMSCurrent:            t.Battery.BMSCurrentA,

		BMSChargeCurrentLimit:    t.Battery.BMSChargeCurrentLimitA,
		BMSDischargeCurrentLimit: t.Battery.BMSDischargeCurrentLimitA,

		PV1Voltage:   t.PV.PV1VoltageV,
		PV1Current:   t.PV.PV1CurrentA,
		PV2Voltage:   t.PV.PV2VoltageV,
		PV2Current:   t.PV.PV2CurrentA,
		PVTotalPower: t.PV.TotalPowerW,

		GridPower:       t.Grid.PowerW,
		GridTotalImport: t.Grid.TotalImportKWh,
		GridImportToday: t.Grid.ImportTodayKWh,
		GridTotalExport: t.Grid.TotalExportKWh,
		GridExportToday: t.Grid.ExportTodayKWh,

		ACActivePower:       t.AC.ActivePowerW,
		InverterTemperature: t.AC.TemperatureC,
		GridFrequency:       t.AC.FrequencyHz,
		HouseLoad:           t.AC.HouseLoadW,

		GenerationToday:       t.Energy.GenerationTodayKWh,
		GenerationYesterday:   t.Energy.GenerationYesterdayKWh,
		BatteryTotalCharge:    t.Energy.BatteryTotalChargeKWh,
		BatteryChargeToday:    t.Energy.BatteryChargeTodayKWh,
		BatteryTotalDischarge: t.Energy.BatteryTotalDischargeKWh,
		BatteryDischargeToday: t.Energy.BatteryDischargeTodayKWh,

		Status:          t.System.Status,
		OperatingStatus: t.System.OperatingStatus,
		WorkMode:        workModeLabel(t.System.WorkMode),
		RTC:             t.Time.Format(time.RFC3339),
		RTCDrift:        drift.Seconds(),

		TouWindow:   touWindowLabel(sp.Slots),
		Boost:       schedule.BoostOf(sp.Slots).String(),
		BoostEndsAt: timestampLabel(sp.BoostEndsAt),
		BoostSelect: schedule.BoostSelectState(sp.Slots),

		SetChargeCurrent:    sp.SetChargeCurrent,
		SetDischargeCurrent: sp.SetDischargeCurrent,

		InverterMaxChargeCurrent:    sp.MaxChargeCurrent,
		InverterMaxDischargeCurrent: sp.MaxDischargeCurrent,
		OptimalIncome:               optimalIncome,
	}
	body, err := json.Marshal(st)
	if err != nil {
		return Message{}, fmt.Errorf("marshal state: %w", err)
	}
	return Message{Topic: c.StateTopic(), Payload: body}, nil
}

// splitBatteryPower splits the correctly-decoded signed battery power (positive
// charging, negative discharging) into the two unsigned sensors Home Assistant's
// Riemann-sum integrations consume, so a template does not have to. This is a
// presentation split of one decoded S32, not a second reading: the signed
// battery_power sensor remains the source of truth, and the legacy app's
// independent-halves decode bug is not reintroduced.
func splitBatteryPower(powerW float64) (chargeW, dischargeW float64) {
	return max(powerW, 0), max(-powerW, 0)
}

// touWindowLabel renders the Time-of-Use charge window as "23:31–05:29", or nil
// when the slots hold no recognisable Time-of-Use schedule, so an unconfigured
// inverter publishes null instead of an invented window.
func touWindowLabel(slots inverter.TimedSlots) *string {
	window, ok := schedule.ToU(slots)
	if !ok {
		return nil
	}
	label := schedule.FormatWindow(window)
	return &label
}

// timestampLabel renders an instant as the RFC3339 string a timestamp
// device-class sensor expects, or nil for the zero time (no value).
func timestampLabel(at time.Time) *string {
	if at.IsZero() {
		return nil
	}
	label := at.Format(time.RFC3339)
	return &label
}

// workModeLabel renders a WorkMode as the energy-storage mode it reports. Bit 0
// is Self Use, the only storage mode ever observed on this inverter, and covers
// both observed register values (35 and 33, which differ only in the Optimal
// Income bit that select.optimal_income owns). Any other combination joins the
// active flags in a deterministic order, falling back to the raw register value
// when no known flag is set, so an unknown mode is never mislabelled.
func workModeLabel(w inverter.WorkMode) string {
	if w.SelfUse {
		return "Self Use"
	}
	var flags []string
	if w.Timed {
		flags = append(flags, "timed")
	}
	if w.AllowGridCharge {
		flags = append(flags, "allow_grid_charge")
	}
	if len(flags) == 0 {
		return fmt.Sprintf("%d", w.Raw)
	}
	return strings.Join(flags, "+")
}
