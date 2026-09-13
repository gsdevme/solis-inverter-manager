package inverter

import "testing"

// timedSlotWindows are the H/M field offsets shared by every timed slot, derived
// from the confirmed slot-1 addresses so slots 2 and 3 are checked against the
// same layout rather than hand-typed constants.
var timedSlotWindows = []struct {
	name       string
	hOff, mOff int
}{
	{"charge start", RegTimedChargeStartHour - RegTimedChargeCurrent, RegTimedChargeStartMinute - RegTimedChargeCurrent},
	{"charge end", RegTimedChargeEndHour - RegTimedChargeCurrent, RegTimedChargeEndMinute - RegTimedChargeCurrent},
	{"discharge start", RegTimedDischargeStartHour - RegTimedChargeCurrent, RegTimedDischargeStartMin - RegTimedChargeCurrent},
	{"discharge end", RegTimedDischargeEndHour - RegTimedChargeCurrent, RegTimedDischargeEndMin - RegTimedChargeCurrent},
}

// TestTimedSlotStride checks the Stage A (#27) capture against the stride-10 slot
// layout: every H/M pair in slots 1–3 is in range, the owner-set windows sit at
// the addresses the stride predicts, and the tail past slot 3 is empty.
func TestTimedSlotStride(t *testing.T) {
	f := loadFixture(t, "live-snapshot-holding-stage-a.json")
	s := f.snapshot()
	u16 := func(addr int) uint16 {
		t.Helper()
		v, err := s.U16(addr)
		if err != nil {
			t.Fatalf("U16(%d): %v", addr, err)
		}
		if want := f.raw(t, addr); v != want {
			t.Fatalf("U16(%d) = %d, by_addr says %d", addr, v, want)
		}
		return v
	}

	if RegTimedSlot2Base != 43151 || RegTimedSlot3Base != 43161 {
		t.Fatalf("slot bases = %d/%d, want 43151/43161", RegTimedSlot2Base, RegTimedSlot3Base)
	}

	for i, base := range []int{RegTimedChargeCurrent, RegTimedSlot2Base, RegTimedSlot3Base} {
		for _, w := range timedSlotWindows {
			h, m := u16(base+w.hOff), u16(base+w.mOff)
			if h > 23 || m > 59 {
				t.Errorf("slot %d %s = %02d:%02d out of range", i+1, w.name, h, m)
			}
		}
	}

	chargeStartH, chargeStartM := timedSlotWindows[0].hOff, timedSlotWindows[0].mOff
	chargeEndH, chargeEndM := timedSlotWindows[1].hOff, timedSlotWindows[1].mOff
	for _, tc := range []struct {
		addr int
		want uint16
		why  string
	}{
		{RegTimedChargeCurrent, 350, "slot 1 charge current 35.0 A"},
		{RegTimedChargeStartHour, 23, "slot 1 charge start 23:31 (the ToU)"},
		{RegTimedChargeStartMinute, 31, "slot 1 charge start 23:31 (the ToU)"},
		{RegTimedSlot2Base + chargeEndH, 5, "slot 2 charge end 05:29 (owner-set)"},
		{RegTimedSlot2Base + chargeEndM, 29, "slot 2 charge end 05:29 (owner-set)"},
		{RegTimedSlot3Base + chargeStartH, 14, "slot 3 charge start 14:02 (owner-set)"},
		{RegTimedSlot3Base + chargeStartM, 2, "slot 3 charge start 14:02 (owner-set)"},
		{RegTimedSlot3Base + chargeEndH, 14, "slot 3 charge end 14:56 (owner-set)"},
		{RegTimedSlot3Base + chargeEndM, 56, "slot 3 charge end 14:56 (owner-set)"},
	} {
		if got := u16(tc.addr); got != tc.want {
			t.Errorf("%d = %d, want %d: %s", tc.addr, got, tc.want, tc.why)
		}
	}

	for addr := RegTimedSlot3Base + RegTimedSlotStride; addr <= 43195; addr++ {
		if v := u16(addr); v != 0 {
			t.Errorf("%d = %d past slot 3, want 0 (no slot 4)", addr, v)
		}
	}
}
