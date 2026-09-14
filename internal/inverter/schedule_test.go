package inverter

import (
	"errors"
	"fmt"
	"strings"
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

// TestTimedSlotWriteRegisters pins the eight write registers for slot 1 and
// slot 3 to the confirmed addresses, in the spec's field order. Both windows are
// set here, which is the "charge block then discharge block" case.
func TestTimedSlotWriteRegisters(t *testing.T) {
	slot := TimedSlot{
		Charge:    TimedWindow{Start: Clock{1, 2}, End: Clock{3, 4}},
		Discharge: TimedWindow{Start: Clock{5, 6}, End: Clock{7, 8}},
	}

	for _, tc := range []struct {
		name     string
		i        int
		wantBase int
	}{
		{"slot 1", 0, 43143},
		{"slot 3", 2, 43163},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := TimedSlotWriteRegisters(tc.i, slot)
			want := []Register{
				{Addr: tc.wantBase + 0, Value: 1},
				{Addr: tc.wantBase + 1, Value: 2},
				{Addr: tc.wantBase + 2, Value: 3},
				{Addr: tc.wantBase + 3, Value: 4},
				{Addr: tc.wantBase + 4, Value: 5},
				{Addr: tc.wantBase + 5, Value: 6},
				{Addr: tc.wantBase + 6, Value: 7},
				{Addr: tc.wantBase + 7, Value: 8},
			}
			if len(got) != len(want) {
				t.Fatalf("TimedSlotWriteRegisters(%d, ...) = %+v, want %+v", tc.i, got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("TimedSlotWriteRegisters(%d, ...)[%d] = %+v, want %+v", tc.i, i, got[i], want[i])
				}
			}
		})
	}
}

// TestTimedSlotWriteRegistersClearsUnusedDirectionFirst pins the block ordering
// rule that keeps a direction swap out of the both-windows-set state: the
// direction whose desired window is unset is listed first, so its zeros land
// before the other direction's window does.
func TestTimedSlotWriteRegistersClearsUnusedDirectionFirst(t *testing.T) {
	charge := TimedWindow{Start: Clock{1, 2}, End: Clock{3, 4}}
	discharge := TimedWindow{Start: Clock{5, 6}, End: Clock{7, 8}}

	for _, tc := range []struct {
		name      string
		slot      TimedSlot
		wantAddrs []int
	}{
		{
			name:      "charge only clears the discharge block first",
			slot:      TimedSlot{Charge: charge},
			wantAddrs: []int{43167, 43168, 43169, 43170, 43163, 43164, 43165, 43166},
		},
		{
			name:      "discharge only clears the charge block first",
			slot:      TimedSlot{Discharge: discharge},
			wantAddrs: []int{43163, 43164, 43165, 43166, 43167, 43168, 43169, 43170},
		},
		{
			name:      "both unset keeps the charge block first",
			slot:      TimedSlot{},
			wantAddrs: []int{43163, 43164, 43165, 43166, 43167, 43168, 43169, 43170},
		},
		{
			name:      "both set keeps the charge block first",
			slot:      TimedSlot{Charge: charge, Discharge: discharge},
			wantAddrs: []int{43163, 43164, 43165, 43166, 43167, 43168, 43169, 43170},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := TimedSlotWriteRegisters(2, tc.slot)
			if len(got) != len(tc.wantAddrs) {
				t.Fatalf("TimedSlotWriteRegisters(2, %+v) = %+v, want %d registers", tc.slot, got, len(tc.wantAddrs))
			}
			for i, addr := range tc.wantAddrs {
				if got[i].Addr != addr {
					t.Errorf("TimedSlotWriteRegisters(2, %+v)[%d].Addr = %d, want %d", tc.slot, i, got[i].Addr, addr)
				}
			}
		})
	}
}

// TestTimedSlotWriteRegistersPanicsOutOfRange asserts a bad slot index panics
// rather than silently writing the wrong registers.
func TestTimedSlotWriteRegistersPanicsOutOfRange(t *testing.T) {
	for _, i := range []int{-1, 3} {
		t.Run(fmt.Sprintf("i=%d", i), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("TimedSlotWriteRegisters(%d, ...) did not panic", i)
				}
			}()
			TimedSlotWriteRegisters(i, TimedSlot{})
		})
	}
}

// TestTimedSlotWriteRegistersRoundTrip proves TimedSlotWriteRegisters and
// DecodeTimedSlots are inverse: writing every slot's registers from a
// TimedSlots value and decoding the resulting snapshot reproduces the input.
func TestTimedSlotWriteRegistersRoundTrip(t *testing.T) {
	want := TimedSlots{
		{Charge: TimedWindow{Start: Clock{1, 2}, End: Clock{3, 4}}, Discharge: TimedWindow{Start: Clock{5, 6}, End: Clock{7, 8}}},
		{Charge: TimedWindow{Start: Clock{9, 10}, End: Clock{11, 12}}, Discharge: TimedWindow{Start: Clock{13, 14}, End: Clock{15, 16}}},
		{Charge: TimedWindow{Start: Clock{17, 18}, End: Clock{19, 20}}, Discharge: TimedWindow{Start: Clock{21, 22}, End: Clock{23, 24}}},
	}

	s := timedSlotSnapshot()
	for i, slot := range want {
		for _, reg := range TimedSlotWriteRegisters(i, slot) {
			s[0].Regs[reg.Addr-RegTimedChargeCurrent] = reg.Value
		}
	}

	got, err := DecodeTimedSlots(s)
	if err != nil {
		t.Fatalf("DecodeTimedSlots: %v", err)
	}
	if got != want {
		t.Errorf("round trip =\n%+v\nwant\n%+v", got, want)
	}
}

func TestParseClock(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      string
		want    Clock
		wantErr bool
	}{
		{"midnight", "00:00", Clock{0, 0}, false},
		{"last minute", "23:59", Clock{23, 59}, false},
		{"hour out of range", "24:00", Clock{}, true},
		{"single-digit hour", "7:05", Clock{}, true},
		{"minute out of range", "12:60", Clock{}, true},
		{"empty", "", Clock{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseClock(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseClock(%q) = %v, want error", tc.in, got)
				}
				if !strings.Contains(err.Error(), tc.in) {
					t.Errorf("ParseClock(%q) error = %q, want it to mention the input", tc.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseClock(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseClock(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
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
