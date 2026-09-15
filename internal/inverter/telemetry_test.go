package inverter

import (
	"testing"
	"time"
)

func TestDecodeTelemetryComprehensive(t *testing.T) {
	f := loadFixture(t, "live-snapshot-comprehensive.json")
	// The comprehensive capture stops at 33180, so the SOC-threshold mirrors
	// (33213/33214) come from the probe capture that does cover them.
	s := f.snapshotWith(t, "bms-limit-probe.json")

	tel, err := DecodeTelemetry(s)
	if err != nil {
		t.Fatalf("DecodeTelemetry: %v", err)
	}

	// Expected values are derived by decoding the fixture's OWN raw registers, so
	// the test proves the arithmetic rather than pasting numbers from a different
	// capture. findings.md is the sanity anchor (see the explicit anchors below).

	t.Run("battery", func(t *testing.T) {
		assertFloat(t, "voltage", tel.Battery.VoltageV, float64(f.raw(t, RegBatteryVoltage))/10)
		assertFloat(t, "current", tel.Battery.CurrentA, float64(int16(f.raw(t, RegBatteryCurrent)))/10)
		assertFloat(t, "soc", tel.Battery.SOCPercent, float64(f.raw(t, RegBatterySOC)))
		assertFloat(t, "soh", tel.Battery.SOHPercent, float64(f.raw(t, RegBatterySOH)))
		assertFloat(t, "bms_voltage", tel.Battery.BMSVoltageV, float64(f.raw(t, RegBMSBatteryVoltage))/100)
		assertFloat(t, "bms_current", tel.Battery.BMSCurrentA, float64(int16(f.raw(t, RegBMSBatteryCurrent)))/10)
		assertFloat(t, "bms_charge_limit", tel.Battery.BMSChargeCurrentLimitA, float64(f.raw(t, RegBMSChargeCurrentLimit))/10)
		assertFloat(t, "bms_discharge_limit", tel.Battery.BMSDischargeCurrentLimitA, float64(f.raw(t, RegBMSDischargeCurrentLimit))/10)
		wantPower := float64(int32(uint32(f.raw(t, RegBatteryPower))<<16 | uint32(f.raw(t, RegBatteryPower+1))))
		assertFloat(t, "power", tel.Battery.PowerW, wantPower)
		if !tel.Battery.Charging {
			t.Errorf("Charging = false, want true (direction flag = %d)", f.raw(t, RegBatteryDirection))
		}
	})

	t.Run("pv", func(t *testing.T) {
		assertFloat(t, "pv1_v", tel.PV.PV1VoltageV, float64(f.raw(t, RegPV1Voltage))/10)
		assertFloat(t, "pv1_a", tel.PV.PV1CurrentA, float64(f.raw(t, RegPV1Current))/10)
		assertFloat(t, "pv2_v", tel.PV.PV2VoltageV, float64(f.raw(t, RegPV2Voltage))/10)
		assertFloat(t, "pv2_a", tel.PV.PV2CurrentA, float64(f.raw(t, RegPV2Current))/10)
		wantTotal := float64(uint32(f.raw(t, RegTotalPVPower))<<16 | uint32(f.raw(t, RegTotalPVPower+1)))
		assertFloat(t, "total_power", tel.PV.TotalPowerW, wantTotal)
	})

	t.Run("grid", func(t *testing.T) {
		wantPower := float64(int32(uint32(f.raw(t, RegGridPower))<<16 | uint32(f.raw(t, RegGridPower+1))))
		assertFloat(t, "power", tel.Grid.PowerW, wantPower)
		wantImport := float64(uint32(f.raw(t, RegGridTotalImportEnergy))<<16 | uint32(f.raw(t, RegGridTotalImportEnergy+1)))
		assertFloat(t, "total_import", tel.Grid.TotalImportKWh, wantImport)
		assertFloat(t, "import_today", tel.Grid.ImportTodayKWh, float64(f.raw(t, RegGridImportToday))/10)
		wantExport := float64(uint32(f.raw(t, RegGridTotalExportEnergy))<<16 | uint32(f.raw(t, RegGridTotalExportEnergy+1)))
		assertFloat(t, "total_export", tel.Grid.TotalExportKWh, wantExport)
		assertFloat(t, "export_today", tel.Grid.ExportTodayKWh, float64(f.raw(t, RegGridExportToday))/10)
	})

	t.Run("ac", func(t *testing.T) {
		wantAC := float64(int32(uint32(f.raw(t, RegACActivePower))<<16 | uint32(f.raw(t, RegACActivePower+1))))
		assertFloat(t, "active_power", tel.AC.ActivePowerW, wantAC)
		assertFloat(t, "temperature", tel.AC.TemperatureC, float64(int16(f.raw(t, RegInverterTemperature)))/10)
		assertFloat(t, "frequency", tel.AC.FrequencyHz, float64(f.raw(t, RegGridFrequency))/100)
		assertFloat(t, "house_load", tel.AC.HouseLoadW, float64(f.raw(t, RegHouseLoadPower)))
	})

	t.Run("energy", func(t *testing.T) {
		assertFloat(t, "gen_today", tel.Energy.GenerationTodayKWh, float64(f.raw(t, RegGenerationToday))/10)
		assertFloat(t, "gen_yesterday", tel.Energy.GenerationYesterdayKWh, float64(f.raw(t, RegGenerationYesterday))/10)
		wantChg := float64(uint32(f.raw(t, RegBatteryTotalChargeEnergy))<<16 | uint32(f.raw(t, RegBatteryTotalChargeEnergy+1)))
		assertFloat(t, "batt_total_charge", tel.Energy.BatteryTotalChargeKWh, wantChg)
		assertFloat(t, "batt_charge_today", tel.Energy.BatteryChargeTodayKWh, float64(f.raw(t, RegBatteryChargeToday))/10)
		wantDis := float64(uint32(f.raw(t, RegBatteryTotalDischargeEnergy))<<16 | uint32(f.raw(t, RegBatteryTotalDischargeEnergy+1)))
		assertFloat(t, "batt_total_discharge", tel.Energy.BatteryTotalDischargeKWh, wantDis)
		assertFloat(t, "batt_discharge_today", tel.Energy.BatteryDischargeTodayKWh, float64(f.raw(t, RegBatteryDischargeToday))/10)
	})

	t.Run("system", func(t *testing.T) {
		if tel.System.Status != f.raw(t, RegInverterStatus) {
			t.Errorf("Status = %d, want %d", tel.System.Status, f.raw(t, RegInverterStatus))
		}
		if tel.System.OperatingStatus != f.raw(t, RegOperatingStatus) {
			t.Errorf("OperatingStatus = %d, want %d", tel.System.OperatingStatus, f.raw(t, RegOperatingStatus))
		}
		if tel.System.WorkMode.Raw != f.raw(t, RegWorkModeReadback) {
			t.Errorf("WorkMode.Raw = %d, want %d", tel.System.WorkMode.Raw, f.raw(t, RegWorkModeReadback))
		}
		if got := StatusLabel(tel.System.Status); got != "Generating" {
			t.Errorf("StatusLabel(%d) = %q, want %q", tel.System.Status, got, "Generating")
		}
	})

	t.Run("bms faults", func(t *testing.T) {
		// Both words read 0 in every capture — a healthy pack — so the fixture
		// pins the addressing and the "no fault" decode, not the bit map.
		if got := f.raw(t, RegBMSFault1); got != 0 {
			t.Fatalf("fixture 33145 = %d, want 0", got)
		}
		if got := f.raw(t, RegBMSFault2); got != 0 {
			t.Fatalf("fixture 33146 = %d, want 0", got)
		}
		if tel.Battery.BMSFault1.Any() || tel.Battery.BMSFault2.Any() {
			t.Errorf("faults = %+v / %+v, want both clear", tel.Battery.BMSFault1, tel.Battery.BMSFault2)
		}
	})

	t.Run("rtc", func(t *testing.T) {
		want := time.Date(
			2000+int(f.raw(t, RegRTCRead)), time.Month(f.raw(t, RegRTCRead+1)), int(f.raw(t, RegRTCRead+2)),
			int(f.raw(t, RegRTCRead+3)), int(f.raw(t, RegRTCRead+4)), int(f.raw(t, RegRTCRead+5)), 0, time.Local)
		if !tel.Time.Equal(want) {
			t.Errorf("Time = %s, want %s", tel.Time, want)
		}
	})
}

// TestDecodeTelemetryAnchors pins the sign/word-order decode against the specific
// live values recorded in findings.md, proving the two's-complement and MSW-first
// handling against known ground truth (not just self-consistent arithmetic).
func TestDecodeTelemetryAnchors(t *testing.T) {
	tel, err := DecodeTelemetry(loadFixture(t, "live-snapshot-comprehensive.json").snapshotWith(t, "bms-limit-probe.json"))
	if err != nil {
		t.Fatalf("DecodeTelemetry: %v", err)
	}

	assertFloat(t, "grid power (import, S32 65535/65404)", tel.Grid.PowerW, -132)
	assertFloat(t, "battery power (charge, S32 0/802)", tel.Battery.PowerW, 802)
	assertFloat(t, "battery voltage", tel.Battery.VoltageV, 51.8)
	assertFloat(t, "battery current (charge)", tel.Battery.CurrentA, 15.5)
	assertFloat(t, "soc", tel.Battery.SOCPercent, 99)
	assertFloat(t, "soh", tel.Battery.SOHPercent, 97)
	assertFloat(t, "bms voltage", tel.Battery.BMSVoltageV, 51.05)
	assertFloat(t, "bms charge current limit", tel.Battery.BMSChargeCurrentLimitA, 15.0)
	assertFloat(t, "bms discharge current limit", tel.Battery.BMSDischargeCurrentLimitA, 112.5)
	assertFloat(t, "pv1 voltage", tel.PV.PV1VoltageV, 205.8)
	assertFloat(t, "pv1 current", tel.PV.PV1CurrentA, 2.9)
	assertFloat(t, "pv2 voltage", tel.PV.PV2VoltageV, 201.0)
	assertFloat(t, "pv2 current", tel.PV.PV2CurrentA, 3.0)
	assertFloat(t, "temperature", tel.AC.TemperatureC, 28.9)
	assertFloat(t, "frequency", tel.AC.FrequencyHz, 50.14)
	assertFloat(t, "house load", tel.AC.HouseLoadW, 385)

	// Cross-check from findings.md: battery power ≈ current × voltage. The decoded
	// S32 (802 W) is within rounding of 15.5 A × 51.8 V (802.9 W).
	product := tel.Battery.CurrentA * tel.Battery.VoltageV
	if diff := product - tel.Battery.PowerW; diff > 5 || diff < -5 {
		t.Errorf("battery power %v W diverges from I×V %v W by more than 5 W", tel.Battery.PowerW, product)
	}
}

func TestDecodeTelemetryMissingBlock(t *testing.T) {
	// A snapshot missing telemetry registers must surface an error, not zero fields.
	s := Snapshot{{Base: RegRTCRead, Regs: []uint16{26, 9, 8, 15, 38, 59}}}
	if _, err := DecodeTelemetry(s); err == nil {
		t.Fatal("DecodeTelemetry with incomplete snapshot returned nil error")
	}
}

// withRegs returns a copy of s with the given absolute addresses overwritten, so
// a decode case can pose a register combination no fixture captured.
func withRegs(t *testing.T, s Snapshot, regs map[int]uint16) Snapshot {
	t.Helper()
	out := make(Snapshot, len(s))
	for i, b := range s {
		out[i] = Block{Base: b.Base, Regs: append([]uint16(nil), b.Regs...)}
	}
	for addr, v := range regs {
		placed := false
		for _, b := range out {
			if i, ok := b.index(addr); ok {
				b.Regs[i] = v
				placed = true
				break
			}
		}
		if !placed {
			t.Fatalf("register %d not present in snapshot", addr)
		}
	}
	return out
}

// TestDecodeTelemetryBatteryDirection pins the battery sign convention to the
// 33135 direction flag. The live capture of 2026-09-14 (house load 448 W, PV
// 189 W, direction = 1 = discharge) reported 33149·33150 as +381 W and 33134 as
// +7.6 A, so the power and current registers are magnitudes on this firmware and
// the decode must apply the sign itself — for a positive or a negative raw word.
func TestDecodeTelemetryBatteryDirection(t *testing.T) {
	base := loadFixture(t, "live-snapshot-comprehensive.json").snapshotWith(t, "bms-limit-probe.json")

	cases := []struct {
		name             string
		direction        uint16
		currentRaw       uint16
		powerHi, powerLo uint16
		wantCharging     bool
		wantCurrentA     float64
		wantPowerW       float64
	}{
		{
			name: "charging", direction: 0, currentRaw: 155, powerHi: 0, powerLo: 802,
			wantCharging: true, wantCurrentA: 15.5, wantPowerW: 802,
		},
		{
			name: "discharging with a positive magnitude", direction: 1, currentRaw: 76, powerHi: 0, powerLo: 381,
			wantCharging: false, wantCurrentA: -7.6, wantPowerW: -381,
		},
		{
			name: "discharging with an already-negative raw", direction: 1, currentRaw: 0xFFB4, powerHi: 0xFFFF, powerLo: 0xFE83,
			wantCharging: false, wantCurrentA: -7.6, wantPowerW: -381,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := withRegs(t, base, map[int]uint16{
				RegBatteryDirection: tc.direction,
				RegBatteryCurrent:   tc.currentRaw,
				RegBatteryPower:     tc.powerHi,
				RegBatteryPower + 1: tc.powerLo,
			})
			tel, err := DecodeTelemetry(s)
			if err != nil {
				t.Fatalf("DecodeTelemetry: %v", err)
			}
			if tel.Battery.Charging != tc.wantCharging {
				t.Errorf("Charging = %v, want %v", tel.Battery.Charging, tc.wantCharging)
			}
			assertFloat(t, "current", tel.Battery.CurrentA, tc.wantCurrentA)
			assertFloat(t, "power", tel.Battery.PowerW, tc.wantPowerW)
		})
	}
}

// TestBMSCurrentLimitsProbeFixture pins the 2026-09-15 read-only probe, whose
// capture was confirmed against the Solis app showing 0 A charge / 112.5 A
// discharge for the BMS. It is the ground truth for both the addressing and the
// ÷10 scale, and it is a different pack state (SOC 100 %, idle) from the
// comprehensive fixture, so together they prove the charge limit tracks the pack
// rather than being a constant.
func TestBMSCurrentLimitsProbeFixture(t *testing.T) {
	s := loadFixture(t, "bms-limit-probe.json").snapshot()

	charge, err := s.U16(RegBMSChargeCurrentLimit)
	if err != nil {
		t.Fatalf("read %d: %v", RegBMSChargeCurrentLimit, err)
	}
	discharge, err := s.U16(RegBMSDischargeCurrentLimit)
	if err != nil {
		t.Fatalf("read %d: %v", RegBMSDischargeCurrentLimit, err)
	}
	assertFloat(t, "bms charge current limit", div10(float64(charge)), 0)
	assertFloat(t, "bms discharge current limit", div10(float64(discharge)), 112.5)

	// The inverter's own ceiling, re-confirmed in the same probe and distinct
	// from both the BMS limits above and the timed setpoints 43141/43142.
	maxCharge, err := s.U16(RegMaxChargeCurrent)
	if err != nil {
		t.Fatalf("read %d: %v", RegMaxChargeCurrent, err)
	}
	maxDischarge, err := s.U16(RegMaxDischargeCurrent)
	if err != nil {
		t.Fatalf("read %d: %v", RegMaxDischargeCurrent, err)
	}
	assertFloat(t, "inverter max charge current", DecodeAmps(maxCharge), 100)
	assertFloat(t, "inverter max discharge current", DecodeAmps(maxDischarge), 100)
}

// TestSOCThresholdMirrorsProbeFixture pins input 33213/33214 as read-only
// mirrors of the over-discharge and force-charge SOC settings. Their ground
// truth is agreement with the holding registers they mirror in the same capture
// (43011 = 20, 43018 = 19), re-read unchanged in a probe taken a week later.
func TestSOCThresholdMirrorsProbeFixture(t *testing.T) {
	// The holding-bank settings the mirrors must agree with.
	const (
		holdingOverdischargeSOC = RegMinSOC                // 43011
		holdingForceChargeSOC   = RegForceChargeSOCSetting // 43018
	)

	sweep := loadFixture(t, "live-snapshot-full-sweep.json")
	if got, want := sweep.raw(t, RegOverdischargeSOC), sweep.raw(t, holdingOverdischargeSOC); got != want {
		t.Errorf("input %d = %d, want holding %d = %d", RegOverdischargeSOC, got, holdingOverdischargeSOC, want)
	}
	if got, want := sweep.raw(t, RegForceChargeSOC), sweep.raw(t, holdingForceChargeSOC); got != want {
		t.Errorf("input %d = %d, want holding %d = %d", RegForceChargeSOC, got, holdingForceChargeSOC, want)
	}

	probe := loadFixture(t, "bms-limit-probe.json")
	tel, err := DecodeTelemetry(probe.snapshotWith(t, "live-snapshot-comprehensive.json"))
	if err != nil {
		t.Fatalf("DecodeTelemetry: %v", err)
	}
	assertFloat(t, "overdischarge soc", tel.Battery.OverdischargeSOCPercent, float64(probe.raw(t, RegOverdischargeSOC)))
	assertFloat(t, "force charge soc", tel.Battery.ForceChargeSOCPercent, float64(probe.raw(t, RegForceChargeSOC)))
}

// TestDecodeTelemetryLivePollBlocks decodes the two production poll blocks
// exactly as the scheduler reads them — input 33022+100 and 33122+93, with no
// complementary fixture merged in — so a decode that reached outside those two
// widths would fail here rather than in production. The capture is the second of
// two sequential live passes 3 s apart that returned identical words.
func TestDecodeTelemetryLivePollBlocks(t *testing.T) {
	f := loadFixture(t, "live-poll-blocks-2026-09-15.json")

	tel, err := DecodeTelemetry(f.snapshot())
	if err != nil {
		t.Fatalf("DecodeTelemetry: %v", err)
	}

	assertFloat(t, "battery soc", tel.Battery.SOCPercent, float64(f.raw(t, RegBatterySOC)))
	if got, want := StatusLabel(tel.System.Status), "Generating"; got != want {
		t.Errorf("status label = %q, want %q", got, want)
	}
	assertFloat(t, "overdischarge soc", tel.Battery.OverdischargeSOCPercent, float64(f.raw(t, RegOverdischargeSOC)))
	assertFloat(t, "force charge soc", tel.Battery.ForceChargeSOCPercent, float64(f.raw(t, RegForceChargeSOC)))

	if tel.Battery.BMSFault1.Raw != 0 || tel.Battery.BMSFault1.Any() {
		t.Errorf("bms fault 1 = %+v, want no fault", tel.Battery.BMSFault1)
	}
	if tel.Battery.BMSFault2.Raw != 0 || tel.Battery.BMSFault2.Any() {
		t.Errorf("bms fault 2 = %+v, want no fault", tel.Battery.BMSFault2)
	}

	assertFloat(t, "bms charge current limit", tel.Battery.BMSChargeCurrentLimitA, div10(float64(f.raw(t, RegBMSChargeCurrentLimit))))
}
