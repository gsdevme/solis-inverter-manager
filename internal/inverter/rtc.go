package inverter

import (
	"fmt"
	"time"
)

// rtcEpochYear is added to the year register (which stores the year since 2000).
const rtcEpochYear = 2000

// DecodeRTC decodes the six-register RTC block (y/mo/d/h/mi/s) starting at
// yearAddr — RegRTCRead on the input bank or RegRTCSet on the holding bank — into
// a naive local datetime. The inverter clock carries no timezone, so the result
// is interpreted in time.Local.
func DecodeRTC(s Snapshot, yearAddr int) (time.Time, error) {
	b, ok := s.block(yearAddr)
	if !ok {
		return time.Time{}, fmt.Errorf("rtc %d: %w", yearAddr, ErrRegisterOutOfRange)
	}
	return decodeRTC(b.Regs, b.Base, yearAddr)
}

// decodeRTC decodes the six consecutive RTC registers from a raw slice keyed at
// base. It works from any base so a block need not start at register 0.
func decodeRTC(regs []uint16, base, yearAddr int) (time.Time, error) {
	var f [rtcRegisterCount]int
	for i := range f {
		v, ok := regAt(regs, base, yearAddr+i)
		if !ok {
			return time.Time{}, fmt.Errorf("rtc %d: %w", yearAddr+i, ErrRegisterOutOfRange)
		}
		f[i] = int(v)
	}
	return time.Date(rtcEpochYear+f[0], time.Month(f[1]), f[2], f[3], f[4], f[5], 0, time.Local), nil
}

// EncodeRTC encodes a time into the six RTC registers [y-2000, mo, d, h, mi, s]
// for writing the RegRTCSet block. The time's own fields are used verbatim
// (naive local datetime); callers pass the value in the inverter's local zone.
func EncodeRTC(t time.Time) [rtcRegisterCount]uint16 {
	return [rtcRegisterCount]uint16{
		uint16(t.Year() - rtcEpochYear),
		uint16(t.Month()),
		uint16(t.Day()),
		uint16(t.Hour()),
		uint16(t.Minute()),
		uint16(t.Second()),
	}
}

// Drift returns how far the inverter clock (rtc) is ahead of a reference clock
// (now). A positive result means the inverter is running fast.
func Drift(rtc, now time.Time) time.Duration {
	return rtc.Sub(now)
}
