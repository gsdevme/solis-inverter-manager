package schedule

import (
	"fmt"
	"time"

	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
)

// boostSlot is the index of the slot reserved for a boost: the last one, since
// slots 1 and 2 carry the Time-of-Use schedule.
const boostSlot = len(inverter.TimedSlots{}) - 1

// windowSeparator joins the two ends of a formatted window (en dash, U+2013).
const windowSeparator = "\u2013"

// isMidnight reports whether c is 00:00. As a window End that means end-of-day;
// as a Start it is a literal midnight, which is how a Time-of-Use pair joins.
func isMidnight(c inverter.Clock) bool {
	return c == inverter.Clock{}
}

// ToU returns the Time-of-Use charge window the owner configured, rejoining the
// midnight-split pair in slots 1 and 2 into one window. It reports false when the
// schedule is not a recognisable Time-of-Use setup: slot 1 unset (an
// unconfigured inverter must not read as "00:00–00:00"), or two charge windows
// that do not meet at midnight.
func ToU(slots inverter.TimedSlots) (inverter.TimedWindow, bool) {
	first, second := slots[0].Charge, slots[1].Charge
	switch {
	case first.IsZero():
		return inverter.TimedWindow{}, false
	case second.IsZero():
		return first, true
	case isMidnight(first.End) && isMidnight(second.Start):
		return inverter.TimedWindow{Start: first.Start, End: second.End}, true
	default:
		return inverter.TimedWindow{}, false
	}
}

// Mode is what a timed slot is set up to do.
type Mode int

const (
	// Off means the slot has neither a charge nor a discharge window.
	Off Mode = iota
	// Charge means the slot charges the battery over its window.
	Charge
	// Discharge means the slot discharges the battery over its window.
	Discharge
)

// String renders the mode as the Home Assistant state string.
func (m Mode) String() string {
	switch m {
	case Off:
		return "Off"
	case Charge:
		return "Charge"
	case Discharge:
		return "Discharge"
	default:
		return fmt.Sprintf("Mode(%d)", int(m))
	}
}

// Boost is the ad-hoc charge or discharge period held in the last timed slot. The
// zero Boost is Off with no window.
type Boost struct {
	Mode   Mode
	Window inverter.TimedWindow
}

// BoostOf reads the boost from the last timed slot. A charge window takes
// precedence over a discharge window, which the inverter should never set
// together in one slot.
func BoostOf(slots inverter.TimedSlots) Boost {
	slot := slots[boostSlot]
	switch {
	case !slot.Charge.IsZero():
		return Boost{Mode: Charge, Window: slot.Charge}
	case !slot.Discharge.IsZero():
		return Boost{Mode: Discharge, Window: slot.Discharge}
	default:
		return Boost{}
	}
}

// EndsAt resolves the boost's end clock against now's date and location. A boost
// that is Off has no end and returns the zero time.
func (b Boost) EndsAt(now time.Time) time.Time {
	if b.Mode == Off {
		return time.Time{}
	}
	return endsAt(b.Window, now)
}

// endsAt resolves a window's End clock against now's date and location. An end of
// 00:00 means end-of-day, so it resolves to the next midnight; the caller decides
// what an unset window means, since a zero window would resolve the same way.
//
// time.Date normalises the result: a DST spring-forward gap resolves to the
// shifted instant, and a fall-back repeat resolves to the earlier occurrence.
// The slot's clock is the inverter's wall clock, resolved here against now's
// location; that only holds while the inverter RTC tracks the host (drift is
// published as rtc_drift, and RTC sync, when enabled, corrects it).
func endsAt(w inverter.TimedWindow, now time.Time) time.Time {
	end := w.End
	day := now.Day()
	if isMidnight(end) {
		day++
	}
	return time.Date(now.Year(), now.Month(), day, int(end.Hour), int(end.Minute), 0, 0, now.Location())
}

// String renders the boost as the Home Assistant state string, either "Off" or
// e.g. "Charge until 14:56".
func (b Boost) String() string {
	if b.Mode == Off {
		return Off.String()
	}
	return fmt.Sprintf("%s until %s", b.Mode, b.Window.End)
}

// FormatWindow renders a window as "23:31–05:29", separated by an en dash.
func FormatWindow(w inverter.TimedWindow) string {
	return w.Start.String() + windowSeparator + w.End.String()
}
