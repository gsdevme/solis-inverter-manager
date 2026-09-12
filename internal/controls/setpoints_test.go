package controls_test

import (
	"context"
	"testing"

	"github.com/gsdevme/solis-inverter-manager/internal/controls"
)

// TestReadSetpoints decodes the one-block read into charge/discharge amps and
// the optimal-income flag.
func TestReadSetpoints(t *testing.T) {
	f := newFakeRW()
	f.regs[43110] = 35  // timed on -> OptimalIncome true
	f.regs[43141] = 300 // 30.0 A
	f.regs[43142] = 155 // 15.5 A

	sp, err := controls.ReadSetpoints(context.Background(), f)
	if err != nil {
		t.Fatalf("ReadSetpoints: %v", err)
	}
	if !sp.OptimalIncome {
		t.Errorf("OptimalIncome = false, want true (43110=35)")
	}
	if sp.SetChargeCurrent != 30.0 {
		t.Errorf("SetChargeCurrent = %v, want 30.0", sp.SetChargeCurrent)
	}
	if sp.SetDischargeCurrent != 15.5 {
		t.Errorf("SetDischargeCurrent = %v, want 15.5", sp.SetDischargeCurrent)
	}

	if len(f.holdCalls) != 1 || f.holdCalls[0] != (holdCall{43110, 33}) {
		t.Errorf("holdCalls = %v, want one read of 43110 count 33", f.holdCalls)
	}
}

// TestReadSetpointsShortBlock treats a short read as an error, not a panic.
func TestReadSetpointsShortBlock(t *testing.T) {
	f := &fakeRW{regs: map[int]uint16{}, shortHolding: true}
	if _, err := controls.ReadSetpoints(context.Background(), f); err == nil {
		t.Fatal("expected error on short block")
	}
}
