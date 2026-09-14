package inverter

import "testing"

func TestAmpsRoundTrip(t *testing.T) {
	tests := []struct {
		amps float64
		raw  uint16
	}{
		{35.0, 350},
		{60.0, 600},
		{45.0, 450},
		{0.0, 0},
		{3.5, 35},
	}
	for _, tc := range tests {
		if got := EncodeAmps(tc.amps); got != tc.raw {
			t.Errorf("EncodeAmps(%v) = %d, want %d", tc.amps, got, tc.raw)
		}
		if got := DecodeAmps(tc.raw); got != tc.amps {
			t.Errorf("DecodeAmps(%d) = %v, want %v", tc.raw, got, tc.amps)
		}
	}
}

func TestEncodeAmpsRounds(t *testing.T) {
	if got := EncodeAmps(35.04); got != 350 {
		t.Errorf("EncodeAmps(35.04) = %d, want 350", got)
	}
	if got := EncodeAmps(35.06); got != 351 {
		t.Errorf("EncodeAmps(35.06) = %d, want 351", got)
	}
}

func TestClampHAChargeAmps(t *testing.T) {
	tests := []struct {
		in, want float64
	}{
		{-5, 0},
		{0, 0},
		{35, 35},
		{60, 60},
		{75, 60},
	}
	for _, tc := range tests {
		if got := ClampHAChargeAmps(tc.in); got != tc.want {
			t.Errorf("ClampHAChargeAmps(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestWritePathAmps decodes the 43141 timed-charge current from the comprehensive
// fixture and the write-path probe's baseline (350 = 35.0 A).
func TestWritePathAmps(t *testing.T) {
	f := loadFixture(t, "live-snapshot-comprehensive.json")
	raw := f.raw(t, RegTimedChargeCurrent)
	if got := DecodeAmps(raw); got != 35.0 {
		t.Errorf("timed charge current = %v A, want 35.0 A", got)
	}
	if EncodeAmps(35.0) != raw {
		t.Errorf("EncodeAmps(35.0) = %d, want %d", EncodeAmps(35.0), raw)
	}
}
