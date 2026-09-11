package inverter

// Modbus register addresses confirmed against the live inverter in
// docs/phase0/findings.md and specified in docs/specs/02-register-map.md.
// Addresses are absolute Modbus addresses; the sidecar returns raw uint16 slices
// keyed from the block base address.
//
// Only registers the spec's input/holding tables confirm are named here. The
// deferred set (serial ASCII, model/firmware, the multi-slot schedule table, and
// various limit/config registers) is out of scope until a later phase confirms
// its decode.

// Input registers (fc04, read-only).
const (
	// RegRTCRead is the first of six input registers (y/mo/d/h/mi/s) holding the
	// inverter real-time clock; mirrors the holding block at RegRTCSet.
	RegRTCRead = 33022

	RegGenerationToday     = 33035
	RegGenerationYesterday = 33036

	RegPV1Voltage = 33049
	RegPV1Current = 33050
	RegPV2Voltage = 33051
	RegPV2Current = 33052

	// RegTotalPVPower is the MSW of the U32 total DC (PV) power in watts.
	RegTotalPVPower = 33057

	// RegACActivePower is the MSW of the S32 inverter AC active power in watts.
	RegACActivePower = 33079

	RegInverterTemperature = 33093
	RegGridFrequency       = 33094
	RegInverterStatus      = 33095
	RegOperatingStatus     = 33121

	// RegGridPower is the MSW of the S32 grid meter power in watts
	// (+ = export, − = import).
	RegGridPower = 33130

	// RegWorkModeReadback mirrors the RegWorkMode holding bitfield on the input bank.
	RegWorkModeReadback = 33132

	RegBatteryVoltage    = 33133
	RegBatteryCurrent    = 33134 // S16, ÷10 A (+ = charge, − = discharge)
	RegBatteryDirection  = 33135 // 0 = charge, 1 = discharge
	RegBatterySOC        = 33139
	RegBatterySOH        = 33140
	RegBMSBatteryVoltage = 33141 // U16, ÷100 V
	RegBMSBatteryCurrent = 33142 // S16, ÷10 A
	RegHouseLoadPower    = 33147

	// RegBatteryPower is the MSW of the S32 battery power in watts
	// (+ = charge, − = discharge).
	RegBatteryPower = 33149

	RegBatteryTotalChargeEnergy    = 33161 // MSW of U32 kWh
	RegBatteryChargeToday          = 33163
	RegBatteryTotalDischargeEnergy = 33165 // MSW of U32 kWh
	RegBatteryDischargeToday       = 33167
	RegGridTotalImportEnergy       = 33169 // MSW of U32 kWh
	RegGridImportToday             = 33171
	RegGridTotalExportEnergy       = 33173 // MSW of U32 kWh
	RegGridExportToday             = 33175
)

// Holding registers (fc03 read / fc06 write).
const (
	// RegRTCSet is the first of six holding registers (y/mo/d/h/mi/s); writing the
	// block corrects the inverter clock. Mirrors input RegRTCRead.
	RegRTCSet = 43000

	RegMinSOC = 43011

	// RegWorkMode is the energy-storage work-mode bitfield; reads back here and at
	// RegWorkModeReadback.
	RegWorkMode = 43110

	RegChargeDischargeEnable    = 43114
	RegChargeDischargeDirection = 43115
	RegInstantCurrent           = 43116
	RegMaxChargeCurrent         = 43117
	RegMaxDischargeCurrent      = 43118

	RegTimedChargeCurrent      = 43141 // U16, ÷10 A
	RegTimedDischargeCurrent   = 43142 // U16, ÷10 A
	RegTimedChargeStartHour    = 43143
	RegTimedChargeStartMinute  = 43144
	RegTimedChargeEndHour      = 43145
	RegTimedChargeEndMinute    = 43146
	RegTimedDischargeStartHour = 43147
	RegTimedDischargeStartMin  = 43148
	RegTimedDischargeEndHour   = 43149
	RegTimedDischargeEndMin    = 43150
)

// rtcRegisterCount is the number of consecutive registers in an RTC block
// (y/mo/d/h/mi/s).
const rtcRegisterCount = 6
