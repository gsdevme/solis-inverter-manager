package inverter

import (
	"errors"
	"testing"
)

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

// TestDecodeTimedSlots decodes the Stage A (#27) capture: the owner's Time-of-Use
// pair split across slots 1 and 2, an afternoon window in slot 3, and no discharge
// windows anywhere.
func TestDecodeTimedSlots(t *testing.T) {
	s := loadFixture(t, "live-snapshot-holding-stage-a.json").snapshot()

	got, err := DecodeTimedSlots(s)
	if err != nil {
		t.Fatalf("DecodeTimedSlots: %v", err)
	}

	want := TimedSlots{
		{Charge: TimedWindow{Start: Clock{23, 31}, End: Clock{0, 0}}},
		{Charge: TimedWindow{Start: Clock{0, 0}, End: Clock{5, 29}}},
		{Charge: TimedWindow{Start: Clock{14, 2}, End: Clock{14, 56}}},
	}
	if got != want {
		t.Errorf("DecodeTimedSlots =\n%+v\nwant\n%+v", got, want)
	}
}

// TestDecodeTimedSlotsBases pins the decoded slot bases to the confirmed named
// constants, so the stride arithmetic and the register map cannot drift apart.
func TestDecodeTimedSlotsBases(t *testing.T) {
	for i, want := range []int{RegTimedChargeCurrent, RegTimedSlot2Base, RegTimedSlot3Base} {
		if got := timedSlotBase(i); got != want {
			t.Errorf("timedSlotBase(%d) = %d, want %d", i, got, want)
		}
	}
}

// timedSlotSnapshot returns a Snapshot covering every timed-slot register, so a
// test can poke one field and leave the rest zero.
func timedSlotSnapshot() Snapshot {
	regs := make([]uint16, len(TimedSlots{})*RegTimedSlotStride)
	return Snapshot{{Base: RegTimedChargeCurrent, Regs: regs}}
}

// TestDecodeTimedSlotsDischargeWindow covers a discharge window, which the live
// fixture leaves unset, so the discharge offsets are proven to land in the
// discharge half of the slot.
func TestDecodeTimedSlotsDischargeWindow(t *testing.T) {
	s := timedSlotSnapshot()
	for addr, v := range map[int]uint16{
		RegTimedSlot2Base + RegTimedDischargeStartHour - RegTimedChargeCurrent: 17,
		RegTimedSlot2Base + RegTimedDischargeStartMin - RegTimedChargeCurrent:  0,
		RegTimedSlot2Base + RegTimedDischargeEndHour - RegTimedChargeCurrent:   19,
		RegTimedSlot2Base + RegTimedDischargeEndMin - RegTimedChargeCurrent:    30,
	} {
		s[0].Regs[addr-RegTimedChargeCurrent] = v
	}

	got, err := DecodeTimedSlots(s)
	if err != nil {
		t.Fatalf("DecodeTimedSlots: %v", err)
	}

	want := TimedSlots{1: {Discharge: TimedWindow{Start: Clock{17, 0}, End: Clock{19, 30}}}}
	if got != want {
		t.Errorf("DecodeTimedSlots =\n%+v\nwant\n%+v", got, want)
	}
}

func TestDecodeTimedSlotsRejectsOutOfRangeFields(t *testing.T) {
	for _, tc := range []struct {
		name string
		addr int
		val  uint16
	}{
		{"hour 24", RegTimedChargeStartHour, 24},
		{"minute 60", RegTimedChargeEndMinute, 60},
		{"discharge hour 255", RegTimedSlot3Base + RegTimedDischargeStartHour - RegTimedChargeCurrent, 255},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := timedSlotSnapshot()
			s[0].Regs[tc.addr-RegTimedChargeCurrent] = tc.val

			if _, err := DecodeTimedSlots(s); err == nil {
				t.Fatalf("DecodeTimedSlots with %d = %d: want error", tc.addr, tc.val)
			}
		})
	}
}

func TestDecodeTimedSlotsMissingRegister(t *testing.T) {
	s := timedSlotSnapshot()
	s[0].Regs = s[0].Regs[:RegTimedSlot3Base-RegTimedChargeCurrent]

	_, err := DecodeTimedSlots(s)
	if !errors.Is(err, ErrRegisterOutOfRange) {
		t.Fatalf("DecodeTimedSlots without slot 3 = %v, want ErrRegisterOutOfRange", err)
	}
}

func TestClockString(t *testing.T) {
	for _, tc := range []struct {
		clock Clock
		want  string
	}{
		{Clock{}, "00:00"},
		{Clock{5, 29}, "05:29"},
		{Clock{23, 31}, "23:31"},
	} {
		if got := tc.clock.String(); got != tc.want {
			t.Errorf("Clock%+v.String() = %q, want %q", tc.clock, got, tc.want)
		}
	}
}

func TestTimedWindowIsZero(t *testing.T) {
	for _, tc := range []struct {
		name   string
		window TimedWindow
		want   bool
	}{
		{"unset", TimedWindow{}, true},
		{"starts at midnight", TimedWindow{End: Clock{5, 29}}, false},
		{"ends at midnight", TimedWindow{Start: Clock{23, 31}}, false},
		{"both set", TimedWindow{Start: Clock{14, 2}, End: Clock{14, 56}}, false},
	} {
		if got := tc.window.IsZero(); got != tc.want {
			t.Errorf("%s: IsZero() = %v, want %v", tc.name, got, tc.want)
		}
	}
}
