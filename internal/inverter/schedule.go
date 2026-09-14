package inverter

import (
	"fmt"
	"strconv"
)

// maxHour and maxMinute bound a valid time-of-day register pair.
const (
	maxHour   = 23
	maxMinute = 59
)

// Clock is a time of day as stored in a timed-slot hour/minute register pair.
// The inverter carries no date or timezone here, so a Clock is a wall-clock
// value only.
type Clock struct {
	Hour   uint8
	Minute uint8
}

// String renders the clock as HH:MM.
func (c Clock) String() string {
	return fmt.Sprintf("%02d:%02d", c.Hour, c.Minute)
}

// TimedWindow is one charge or discharge period of a timed slot. A window never
// crosses midnight, so an End of 00:00 on a configured window means end-of-day.
type TimedWindow struct {
	Start Clock
	End   Clock
}

// IsZero reports whether the window is unset, meaning Start and End are both
// 00:00. A window that merely starts at midnight (slot 2 of a Time-of-Use pair)
// has a non-zero End and is not zero.
func (w TimedWindow) IsZero() bool {
	return w == TimedWindow{}
}

// TimedSlot is one timed-charge slot: a charge window and a discharge window.
// The charge and discharge currents are global to the inverter, not per slot.
type TimedSlot struct {
	Charge    TimedWindow
	Discharge TimedWindow
}

// TimedSlots are the inverter's three timed-charge slots in register order.
type TimedSlots [3]TimedSlot

// timedWindowOffsets locates one window's four hour/minute registers relative to
// a slot base. The offsets are derived from the confirmed slot-1 addresses so
// slots 2 and 3 follow the register map rather than hand-typed numbers.
type timedWindowOffsets struct {
	startHour   int
	startMinute int
	endHour     int
	endMinute   int
}

var (
	timedChargeOffsets = timedWindowOffsets{
		startHour:   RegTimedChargeStartHour - RegTimedChargeCurrent,
		startMinute: RegTimedChargeStartMinute - RegTimedChargeCurrent,
		endHour:     RegTimedChargeEndHour - RegTimedChargeCurrent,
		endMinute:   RegTimedChargeEndMinute - RegTimedChargeCurrent,
	}
	timedDischargeOffsets = timedWindowOffsets{
		startHour:   RegTimedDischargeStartHour - RegTimedChargeCurrent,
		startMinute: RegTimedDischargeStartMin - RegTimedChargeCurrent,
		endHour:     RegTimedDischargeEndHour - RegTimedChargeCurrent,
		endMinute:   RegTimedDischargeEndMin - RegTimedChargeCurrent,
	}
)

// timedSlotBase returns the base address of slot i (0-based), which is slot 1's
// base plus i strides — RegTimedSlot2Base and RegTimedSlot3Base for i = 1 and 2.
func timedSlotBase(i int) int {
	return RegTimedChargeCurrent + i*RegTimedSlotStride
}

// DecodeTimedSlots decodes the three timed slots from a holding-register
// snapshot. It fails with ErrRegisterOutOfRange when the snapshot does not cover
// every slot, and with a field error when an hour or minute register is out of
// range — an inverter that has never had a slot configured reports 0, which
// decodes to an unset window rather than an error.
func DecodeTimedSlots(s Snapshot) (TimedSlots, error) {
	var slots TimedSlots
	for i := range slots {
		base := timedSlotBase(i)
		charge, err := decodeTimedWindow(s, base, timedChargeOffsets)
		if err != nil {
			return TimedSlots{}, fmt.Errorf("timed slot %d charge: %w", i+1, err)
		}
		discharge, err := decodeTimedWindow(s, base, timedDischargeOffsets)
		if err != nil {
			return TimedSlots{}, fmt.Errorf("timed slot %d discharge: %w", i+1, err)
		}
		slots[i] = TimedSlot{Charge: charge, Discharge: discharge}
	}
	return slots, nil
}

// decodeTimedWindow decodes the start and end clocks of one window.
func decodeTimedWindow(s Snapshot, base int, off timedWindowOffsets) (TimedWindow, error) {
	start, err := decodeClock(s, base+off.startHour, base+off.startMinute)
	if err != nil {
		return TimedWindow{}, fmt.Errorf("start: %w", err)
	}
	end, err := decodeClock(s, base+off.endHour, base+off.endMinute)
	if err != nil {
		return TimedWindow{}, fmt.Errorf("end: %w", err)
	}
	return TimedWindow{Start: start, End: end}, nil
}

// decodeClock reads an hour/minute register pair and rejects field values the
// inverter should never report.
func decodeClock(s Snapshot, hourAddr, minuteAddr int) (Clock, error) {
	hour, err := s.U16(hourAddr)
	if err != nil {
		return Clock{}, err
	}
	minute, err := s.U16(minuteAddr)
	if err != nil {
		return Clock{}, err
	}
	if hour > maxHour || minute > maxMinute {
		return Clock{}, fmt.Errorf("clock %d:%d (registers %d/%d) out of range", hour, minute, hourAddr, minuteAddr)
	}
	return Clock{Hour: uint8(hour), Minute: uint8(minute)}, nil
}

// ParseClock parses a clock in strict HH:MM form: exactly two digits for the
// hour and two for the minute, bounded by maxHour and maxMinute. The returned
// error names the offending input.
func ParseClock(s string) (Clock, error) {
	if len(s) != 5 || s[2] != ':' {
		return Clock{}, fmt.Errorf("invalid clock %q: want HH:MM", s)
	}
	hour, err := strconv.ParseUint(s[:2], 10, 8)
	if err != nil {
		return Clock{}, fmt.Errorf("invalid clock %q: %w", s, err)
	}
	minute, err := strconv.ParseUint(s[3:], 10, 8)
	if err != nil {
		return Clock{}, fmt.Errorf("invalid clock %q: %w", s, err)
	}
	if hour > maxHour || minute > maxMinute {
		return Clock{}, fmt.Errorf("invalid clock %q: out of range", s)
	}
	return Clock{Hour: uint8(hour), Minute: uint8(minute)}, nil
}

// Register is one holding-register write: address and value.
type Register struct {
	Addr  int
	Value uint16
}

// TimedSlotWriteRegisters expands one timed slot's eight hour/minute registers
// into address/value pairs. The leading current registers
// (RegTimedChargeCurrent and RegTimedDischargeCurrent) are global to the
// inverter, not part of a slot, and are never included here.
//
// Order is load-bearing, because the sidecar is fc06-only and a window is set
// one word at a time: a direction whose desired window is unset is listed
// before a direction whose window is set, so a slot swapping direction clears
// the old window before programming the new one and never transits the
// both-windows-set state the inverter has not been probed in. When both
// directions are unset, or both are set, the charge block comes first. Within a
// direction the order is always start hour, start minute, end hour, end minute,
// so the transient is a superset of the target window rather than an inverted
// one.
//
// TimedSlotWriteRegisters panics if i is outside 0..2: an out-of-range slot
// index is a programming error, like a bad array index.
func TimedSlotWriteRegisters(i int, slot TimedSlot) []Register {
	if i < 0 || i > 2 {
		panic(fmt.Sprintf("inverter: TimedSlotWriteRegisters: slot index %d out of range [0,2]", i))
	}
	base := timedSlotBase(i)
	charge := timedWindowRegisters(base, timedChargeOffsets, slot.Charge)
	discharge := timedWindowRegisters(base, timedDischargeOffsets, slot.Discharge)
	if !slot.Charge.IsZero() && slot.Discharge.IsZero() {
		return append(discharge, charge...)
	}
	return append(charge, discharge...)
}

// timedWindowRegisters expands one direction's four hour/minute registers in the
// spec's field order: start hour, start minute, end hour, end minute.
func timedWindowRegisters(base int, off timedWindowOffsets, w TimedWindow) []Register {
	return []Register{
		{Addr: base + off.startHour, Value: uint16(w.Start.Hour)},
		{Addr: base + off.startMinute, Value: uint16(w.Start.Minute)},
		{Addr: base + off.endHour, Value: uint16(w.End.Hour)},
		{Addr: base + off.endMinute, Value: uint16(w.End.Minute)},
	}
}
