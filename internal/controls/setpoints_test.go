package controls_test

import (
	"context"
	"testing"

	"github.com/gsdevme/solis-inverter-manager/internal/controls"
	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
)

// TestReadSetpoints decodes the one-block read into charge/discharge amps, the
// optimal-income flag, and the three timed slots.
func TestReadSetpoints(t *testing.T) {
	f := newFakeRW()
	f.regs[43110] = 35  // timed on -> OptimalIncome true
	f.regs[43141] = 300 // 30.0 A
	f.regs[43142] = 155 // 15.5 A
	f.regs[43143] = 23  // slot 1 charge start hour
	f.regs[43144] = 31  // slot 1 charge start minute
	f.regs[43155] = 5   // slot 2 charge end hour
	f.regs[43156] = 29  // slot 2 charge end minute
	f.regs[43163] = 14  // slot 3 charge start hour
	f.regs[43164] = 2   // slot 3 charge start minute
	f.regs[43165] = 14  // slot 3 charge end hour
	f.regs[43166] = 56  // slot 3 charge end minute

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
	if want := (inverter.Clock{Hour: 23, Minute: 31}); sp.Slots[0].Charge.Start != want {
		t.Errorf("Slots[0].Charge.Start = %v, want %v", sp.Slots[0].Charge.Start, want)
	}
	if want := (inverter.Clock{Hour: 14, Minute: 56}); sp.Slots[2].Charge.End != want {
		t.Errorf("Slots[2].Charge.End = %v, want %v", sp.Slots[2].Charge.End, want)
	}

	if len(f.holdCalls) != 1 || f.holdCalls[0] != (holdCall{43110, 61}) {
		t.Errorf("holdCalls = %v, want one read of 43110 count 61", f.holdCalls)
	}
}

// TestReadSetpointsShortBlock treats a short read as an error, not a panic.
func TestReadSetpointsShortBlock(t *testing.T) {
	f := &fakeRW{regs: map[int]uint16{}, shortHolding: true}
	if _, err := controls.ReadSetpoints(context.Background(), f); err == nil {
		t.Fatal("expected error on short block")
	}
}
