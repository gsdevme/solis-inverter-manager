package homeassistant

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
)

// State is the flat state document published to the shared state topic. Every
// json tag equals an Entity.Key, which is the contract each entity's
// value_template relies on (value_json.<Key>). No entity may exist without a
// field here, and no field here without an entity.
type State struct {
	BatteryVoltage  float64 `json:"battery_voltage"`
	BatteryCurrent  float64 `json:"battery_current"`
	BatteryPower    float64 `json:"battery_power"`
	BatteryCharging bool    `json:"battery_charging"`
	BatterySOC      float64 `json:"battery_soc"`
	BatterySOH      float64 `json:"battery_soh"`
	BMSVoltage      float64 `json:"bms_voltage"`
	BMSCurrent      float64 `json:"bms_current"`

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

	SetChargeCurrent    float64 `json:"set_charge_current"`
	SetDischargeCurrent float64 `json:"set_discharge_current"`
	OptimalIncome       string  `json:"optimal_income"`
}

// Setpoints is the current writable-control state, read from the holding bank
// by the caller and folded into the shared state document.
type Setpoints struct {
	SetChargeCurrent    float64
	SetDischargeCurrent float64
	OptimalIncome       bool
}

// BuildState marshals a decoded Telemetry (plus the externally computed clock
// drift) into the retained state document. The topic is the shared StateTopic.
// drift is supplied by the caller because this package is pure and must not read
// the wall clock.
func (c Config) BuildState(t inverter.Telemetry, drift time.Duration, sp Setpoints) (Message, error) {
	optimalIncome := "OFF"
	if sp.OptimalIncome {
		optimalIncome = "ON"
	}
	st := State{
		BatteryVoltage:  t.Battery.VoltageV,
		BatteryCurrent:  t.Battery.CurrentA,
		BatteryPower:    t.Battery.PowerW,
		BatteryCharging: t.Battery.Charging,
		BatterySOC:      t.Battery.SOCPercent,
		BatterySOH:      t.Battery.SOHPercent,
		BMSVoltage:      t.Battery.BMSVoltageV,
		BMSCurrent:      t.Battery.BMSCurrentA,

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

		SetChargeCurrent:    sp.SetChargeCurrent,
		SetDischargeCurrent: sp.SetDischargeCurrent,
		OptimalIncome:       optimalIncome,
	}
	body, err := json.Marshal(st)
	if err != nil {
		return Message{}, fmt.Errorf("marshal state: %w", err)
	}
	return Message{Topic: c.StateTopic(), Payload: body}, nil
}

// workModeLabel renders a WorkMode as a stable human label. The two named
// setpoints map to fixed strings; any other combination joins the active flags in
// a deterministic order, falling back to the raw register value when no known
// flag is set.
func workModeLabel(w inverter.WorkMode) string {
	switch w.Raw {
	case inverter.WorkModeTimedOn:
		return "Optimal income ON"
	case inverter.WorkModeTimedOff:
		return "Optimal income OFF"
	}
	var flags []string
	if w.SelfUse {
		flags = append(flags, "self_use")
	}
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
