package inverter

import (
	"testing"
	"time"
)

func TestDecodeTelemetryComprehensive(t *testing.T) {
	f := loadFixture(t, "live-snapshot-comprehensive.json")
	s := f.snapshot()

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
	tel, err := DecodeTelemetry(loadFixture(t, "live-snapshot-comprehensive.json").snapshot())
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
