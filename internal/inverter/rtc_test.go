package inverter

import (
	"testing"
	"time"
)

func TestDecodeRTCInput(t *testing.T) {
	f := loadFixture(t, "live-snapshot-comprehensive.json")
	got, err := DecodeRTC(f.snapshot(), RegRTCRead)
	if err != nil {
		t.Fatalf("DecodeRTC: %v", err)
	}
	want := time.Date(
		2000+int(f.raw(t, RegRTCRead)), time.Month(f.raw(t, RegRTCRead+1)), int(f.raw(t, RegRTCRead+2)),
		int(f.raw(t, RegRTCRead+3)), int(f.raw(t, RegRTCRead+4)), int(f.raw(t, RegRTCRead+5)), 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("DecodeRTC = %s, want %s", got, want)
	}
}

// TestRTCHoldingRoundTrip decodes the holding RTC block (43000-43005) from the
// full-sweep fixture and re-encodes it, proving decode/encode are inverse.
func TestRTCHoldingRoundTrip(t *testing.T) {
	f := loadFixture(t, "live-snapshot-full-sweep.json")
	got, err := DecodeRTC(f.snapshot(), RegRTCSet)
	if err != nil {
		t.Fatalf("DecodeRTC(holding): %v", err)
	}

	want := [rtcRegisterCount]uint16{
		f.raw(t, RegRTCSet), f.raw(t, RegRTCSet+1), f.raw(t, RegRTCSet+2),
		f.raw(t, RegRTCSet+3), f.raw(t, RegRTCSet+4), f.raw(t, RegRTCSet+5),
	}
	if enc := EncodeRTC(got); enc != want {
		t.Errorf("EncodeRTC(DecodeRTC(...)) = %v, want %v", enc, want)
	}
}

func TestEncodeRTC(t *testing.T) {
	tm := time.Date(2026, time.September, 8, 15, 43, 17, 0, time.Local)
	want := [rtcRegisterCount]uint16{26, 9, 8, 15, 43, 17}
	if got := EncodeRTC(tm); got != want {
		t.Errorf("EncodeRTC = %v, want %v", got, want)
	}
}

func TestDrift(t *testing.T) {
	now := time.Date(2026, time.September, 8, 15, 40, 0, 0, time.Local)
	rtc := now.Add(205 * time.Second) // findings.md: inverter ran ~+205 s ahead
	if got := Drift(rtc, now); got != 205*time.Second {
		t.Errorf("Drift = %s, want 205s", got)
	}
	if got := Drift(now, rtc); got != -205*time.Second {
		t.Errorf("Drift (behind) = %s, want -205s", got)
	}
}

func TestDecodeRTCMissing(t *testing.T) {
	s := Snapshot{{Base: RegRTCRead, Regs: []uint16{26, 9, 8}}} // only 3 of 6 regs
	if _, err := DecodeRTC(s, RegRTCRead); err == nil {
		t.Fatal("DecodeRTC with short block returned nil error")
	}
}
