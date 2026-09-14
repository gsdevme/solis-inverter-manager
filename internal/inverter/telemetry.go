package inverter

import (
	"math"
	"time"
)

// Telemetry is one decoded inverter reading, grouped by subsystem. Physical
// values are float64 at the package boundary with scale already applied; a 0.0
// value is a real reading, not "unknown" (a failed read surfaces as an error from
// the decode, never a zero field).
type Telemetry struct {
	// Time is the inverter real-time clock (naive local datetime, no timezone).
	Time    time.Time
	Battery Battery
	PV      PV
	Grid    Grid
	AC      AC
	Energy  Energy
	System  System
}

// Battery holds the decoded battery subsystem values. This firmware reports the
// battery current and power as magnitudes, so CurrentA and PowerW take their
// sign from the 33135 direction flag rather than from the register word: +
// = charge, − = discharge.
type Battery struct {
	VoltageV    float64 // 33133, ÷10 V
	CurrentA    float64 // 33134, ÷10 A, magnitude signed by 33135 (+ = charge, − = discharge)
	Charging    bool    // 33135, 0 = charge, 1 = discharge
	SOCPercent  float64 // 33139
	SOHPercent  float64 // 33140
	BMSVoltageV float64 // 33141, ÷100 V
	BMSCurrentA float64 // 33142 S16, ÷10 A
	PowerW      float64 // 33149·33150, W, magnitude signed by 33135 (+ = charge, − = discharge)
}

// PV holds the decoded photovoltaic (DC) values.
type PV struct {
	PV1VoltageV float64 // 33049, ÷10 V
	PV1CurrentA float64 // 33050, ÷10 A
	PV2VoltageV float64 // 33051, ÷10 V
	PV2CurrentA float64 // 33052, ÷10 A
	TotalPowerW float64 // 33057·33058 U32, W
}

// Grid holds the decoded grid meter values. PowerW is a single signed 32-bit
// value: + = export, − = import.
type Grid struct {
	PowerW         float64 // 33130·33131 S32 (+ = export, − = import)
	TotalImportKWh float64 // 33169·33170 U32
	ImportTodayKWh float64 // 33171, ÷10
	TotalExportKWh float64 // 33173·33174 U32
	ExportTodayKWh float64 // 33175, ÷10
}

// AC holds the decoded inverter AC-side values and house load.
type AC struct {
	ActivePowerW float64 // 33079·33080 S32, W
	TemperatureC float64 // 33093 S16, ÷10 °C
	FrequencyHz  float64 // 33094, ÷100 Hz
	HouseLoadW   float64 // 33147, W
}

// Energy holds the decoded lifetime and today energy counters.
type Energy struct {
	GenerationTodayKWh       float64 // 33035, ÷10
	GenerationYesterdayKWh   float64 // 33036, ÷10
	BatteryTotalChargeKWh    float64 // 33161·33162 U32
	BatteryChargeTodayKWh    float64 // 33163, ÷10
	BatteryTotalDischargeKWh float64 // 33165·33166 U32
	BatteryDischargeTodayKWh float64 // 33167, ÷10
}

// System holds status and mode registers. Status enums and the operating-status
// bitfield are surfaced raw; the work mode is decoded from its mirror register.
type System struct {
	Status          uint16 // 33095 enum (3 = running)
	OperatingStatus uint16 // 33121 bitfield (raw)
	WorkMode        WorkMode
}

// div10 applies the common ÷10 scale.
func div10(v float64) float64 { return v / 10.0 }

// div100 applies the ÷100 scale (BMS voltage, grid frequency).
func div100(v float64) float64 { return v / 100.0 }

// signedByDirection resolves a battery reading against the 33135 direction flag,
// returning a positive value while charging and a negative one while
// discharging. The raw register is treated as a magnitude — this firmware leaves
// battery power and current unsigned even when discharging — so the flag, not the
// register's own sign, decides the direction. Zero is returned unsigned so a
// resting battery never publishes a negative zero.
func signedByDirection(v float64, charging bool) float64 {
	magnitude := math.Abs(v)
	if charging || magnitude == 0 {
		return magnitude
	}
	return -magnitude
}

// reader accumulates the first read error while decoding, so the field
// assignments below stay declarative instead of interleaving error checks. Once
// err is set, subsequent reads return zero without overwriting it.
type reader struct {
	s   Snapshot
	err error
}

func (r *reader) join(err error) {
	if err != nil && r.err == nil {
		r.err = err
	}
}

func (r *reader) u16(addr int) uint16 {
	if r.err != nil {
		return 0
	}
	v, err := r.s.U16(addr)
	r.join(err)
	return v
}

func (r *reader) s16(addr int) int16 {
	if r.err != nil {
		return 0
	}
	v, err := r.s.S16(addr)
	r.join(err)
	return v
}

func (r *reader) u32(addr int) uint32 {
	if r.err != nil {
		return 0
	}
	v, err := r.s.U32(addr)
	r.join(err)
	return v
}

func (r *reader) s32(addr int) int32 {
	if r.err != nil {
		return 0
	}
	v, err := r.s.S32(addr)
	r.join(err)
	return v
}

// DecodeTelemetry decodes a full reading from a snapshot of register blocks. It
// returns an error if any confirmed register is missing from the snapshot; the
// blocks a caller passes must cover every telemetry register.
func DecodeTelemetry(s Snapshot) (Telemetry, error) {
	r := &reader{s: s}
	var t Telemetry

	rtc, err := DecodeRTC(s, RegRTCRead)
	r.join(err)
	t.Time = rtc

	charging := r.u16(RegBatteryDirection) == 0
	t.Battery = Battery{
		VoltageV:    div10(float64(r.u16(RegBatteryVoltage))),
		CurrentA:    signedByDirection(div10(float64(r.s16(RegBatteryCurrent))), charging),
		Charging:    charging,
		SOCPercent:  float64(r.u16(RegBatterySOC)),
		SOHPercent:  float64(r.u16(RegBatterySOH)),
		BMSVoltageV: div100(float64(r.u16(RegBMSBatteryVoltage))),
		BMSCurrentA: div10(float64(r.s16(RegBMSBatteryCurrent))),
		PowerW:      signedByDirection(float64(r.s32(RegBatteryPower)), charging),
	}

	t.PV = PV{
		PV1VoltageV: div10(float64(r.u16(RegPV1Voltage))),
		PV1CurrentA: div10(float64(r.u16(RegPV1Current))),
		PV2VoltageV: div10(float64(r.u16(RegPV2Voltage))),
		PV2CurrentA: div10(float64(r.u16(RegPV2Current))),
		TotalPowerW: float64(r.u32(RegTotalPVPower)),
	}

	t.Grid = Grid{
		PowerW:         float64(r.s32(RegGridPower)),
		TotalImportKWh: float64(r.u32(RegGridTotalImportEnergy)),
		ImportTodayKWh: div10(float64(r.u16(RegGridImportToday))),
		TotalExportKWh: float64(r.u32(RegGridTotalExportEnergy)),
		ExportTodayKWh: div10(float64(r.u16(RegGridExportToday))),
	}

	t.AC = AC{
		ActivePowerW: float64(r.s32(RegACActivePower)),
		TemperatureC: div10(float64(r.s16(RegInverterTemperature))),
		FrequencyHz:  div100(float64(r.u16(RegGridFrequency))),
		HouseLoadW:   float64(r.u16(RegHouseLoadPower)),
	}

	t.Energy = Energy{
		GenerationTodayKWh:       div10(float64(r.u16(RegGenerationToday))),
		GenerationYesterdayKWh:   div10(float64(r.u16(RegGenerationYesterday))),
		BatteryTotalChargeKWh:    float64(r.u32(RegBatteryTotalChargeEnergy)),
		BatteryChargeTodayKWh:    div10(float64(r.u16(RegBatteryChargeToday))),
		BatteryTotalDischargeKWh: float64(r.u32(RegBatteryTotalDischargeEnergy)),
		BatteryDischargeTodayKWh: div10(float64(r.u16(RegBatteryDischargeToday))),
	}

	t.System = System{
		Status:          r.u16(RegInverterStatus),
		OperatingStatus: r.u16(RegOperatingStatus),
		WorkMode:        DecodeWorkMode(r.u16(RegWorkModeReadback)),
	}

	if r.err != nil {
		return Telemetry{}, r.err
	}
	return t, nil
}
