package schedule

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
)

const (
	// minutesPerDay is both the length of a day and the minute-of-day value of
	// end-of-day, which a window End of 00:00 stands for.
	minutesPerDay = 24 * 60
	// boostStep is the quarter-hour grid a boost ends on, and the granularity of
	// the selectable durations.
	boostStep = 15
	// touSeparator joins the two clocks of a TOU_WINDOW setting.
	touSeparator = "-"
)

// boostDurations are the selectable boost lengths in minutes, in the order the
// Home Assistant select lists them.
var boostDurations = []int{15, 30, 45, 60}

// ErrCrossesMidnight and ErrOverlapsToU are the two reasons a requested boost is
// refused: the inverter cannot hold a window that crosses midnight, and a boost
// must not fight the Time-of-Use tariff the manager asserts in slots 1 and 2.
var (
	ErrCrossesMidnight = errors.New("boost would cross midnight")
	ErrOverlapsToU     = errors.New("boost overlaps the time-of-use window")
)

// ParseToUWindow parses the TOU_WINDOW setting, "HH:MM-HH:MM" in local time. An
// empty string is not an error: it reports enabled=false, meaning the owner has
// turned Time-of-Use assertion off and slots 1 and 2 are left alone. An end of
// 00:00 means end-of-day and is allowed; a start equal to the end is not, since
// it describes neither a full day nor an empty one.
func ParseToUWindow(s string) (inverter.TimedWindow, bool, error) {
	if s == "" {
		return inverter.TimedWindow{}, false, nil
	}
	startText, endText, found := strings.Cut(s, touSeparator)
	if !found {
		return inverter.TimedWindow{}, false, fmt.Errorf("invalid time-of-use window %q: want HH:MM-HH:MM", s)
	}
	start, err := inverter.ParseClock(startText)
	if err != nil {
		return inverter.TimedWindow{}, false, fmt.Errorf("invalid time-of-use window %q: start: %w", s, err)
	}
	end, err := inverter.ParseClock(endText)
	if err != nil {
		return inverter.TimedWindow{}, false, fmt.Errorf("invalid time-of-use window %q: end: %w", s, err)
	}
	if start == end {
		return inverter.TimedWindow{}, false, fmt.Errorf("invalid time-of-use window %q: start and end are the same", s)
	}
	return inverter.TimedWindow{Start: start, End: end}, true, nil
}

// ToUSlots expands the Time-of-Use window into the first two timed slots. The
// inverter cannot hold a window that crosses midnight, so a tariff that does is
// split into a slot ending at end-of-day and a slot starting at midnight; a
// tariff that already ends at end-of-day needs no second slot. Both slots'
// discharge windows are asserted empty — the tariff only ever charges.
func ToUSlots(tou inverter.TimedWindow) [2]inverter.TimedSlot {
	if !isMidnight(tou.End) && minuteOfDay(tou.Start) < minuteOfDay(tou.End) {
		return [2]inverter.TimedSlot{{Charge: tou}, {}}
	}
	beforeMidnight := inverter.TimedSlot{Charge: inverter.TimedWindow{Start: tou.Start}}
	if isMidnight(tou.End) {
		return [2]inverter.TimedSlot{beforeMidnight, {}}
	}
	afterMidnight := inverter.TimedSlot{Charge: inverter.TimedWindow{End: tou.End}}
	return [2]inverter.TimedSlot{beforeMidnight, afterMidnight}
}

// Desired builds the schedule the inverter should hold: slots 1 and 2 carry the
// Time-of-Use tariff when the manager asserts it (otherwise whatever the
// inverter already holds is left alone), and the boost slot keeps every window
// that reads as a live boost while clearing any other window it holds.
//
// A zero tariff window is never asserted, whatever assertToU says: asserting it
// would clear the owner's own schedule rather than leave it alone, which is the
// opposite of what "Time-of-Use assertion is off" means.
func Desired(tou inverter.TimedWindow, assertToU bool, slots inverter.TimedSlots, now time.Time) inverter.TimedSlots {
	desired := slots
	if assertToU && !tou.IsZero() {
		tariff := ToUSlots(tou)
		copy(desired[:], tariff[:])
	}
	desired[boostSlot] = running(slots[boostSlot], now)
	return desired
}

// running keeps each of the slot's two windows while it is live and zeroes each
// one that is not, judging the directions independently.
//
// Live is the narrow shape a boost has: PlanBoost only ever writes a window
// starting at the current minute and ending on a quarter-hour strictly before
// midnight, so any other set window in the boost slot is a remnant — a partial
// write whose end registers never landed, or a boost from a previous day — and
// cannot be a boost worth keeping. Judging by end alone re-adopted both: an end
// of 00:00 never arrives, and a yesterday window's end reads as still ahead.
//
// Per direction rather than per boost because a boost is written one register at
// a time: a command interrupted by a transport failure can leave the slot holding
// a half-cleared window of the old direction alongside the freshly programmed
// new one. Judging the slot as a whole would read the remnant's window as the
// boost's own and wipe the running window with it, escalating one failed
// register into a destroyed boost. Judged per direction, the next reconcile
// clears only the remnant and so heals the partial write instead.
func running(slot inverter.TimedSlot, now time.Time) inverter.TimedSlot {
	if !live(slot.Charge, now) {
		slot.Charge = inverter.TimedWindow{}
	}
	if !live(slot.Discharge, now) {
		slot.Discharge = inverter.TimedWindow{}
	}
	return slot
}

// live reports whether a window reads as a boost the reconcile should keep: set,
// ending before midnight, and covering now in minute-of-day terms. An unset
// window is never live, and clearing one is a no-op.
func live(w inverter.TimedWindow, now time.Time) bool {
	if w.IsZero() || isMidnight(w.End) {
		return false
	}
	nowMinute := minuteOf(now)
	return minuteOfDay(w.Start) <= nowMinute && nowMinute < minuteOfDay(w.End)
}

// BoostOptions lists the Home Assistant select options in order: Off, then each
// duration to charge, then each duration to discharge. Each call returns a fresh
// slice, so a caller may keep and even reshape its own copy.
func BoostOptions() []string {
	options := make([]string, 0, 1+2*len(boostDurations))
	options = append(options, Off.String())
	for _, mode := range []Mode{Charge, Discharge} {
		for _, minutes := range boostDurations {
			options = append(options, boostOption(mode, minutes))
		}
	}
	return options
}

// ParseBoostOption maps a select command back to a mode and a duration. "Off"
// parses to Off with no duration; anything that is not an option, once
// surrounding space is trimmed, reports ok=false so the caller can log and drop
// it.
func ParseBoostOption(s string) (mode Mode, minutes int, ok bool) {
	s = strings.TrimSpace(s)
	if s == Off.String() {
		return Off, 0, true
	}
	for _, candidate := range []Mode{Charge, Discharge} {
		for _, duration := range boostDurations {
			if s == boostOption(candidate, duration) {
				return candidate, duration, true
			}
		}
	}
	return Off, 0, false
}

// PlanBoost turns a selected option into the window to write to the boost slot.
// The boost starts at now truncated to the minute and ends on the quarter-hour
// grid: the minutes/boostStep-th boundary strictly after now, so the boost runs
// for longer than minutes-boostStep and for at most minutes.
//
// It refuses the boost with ErrCrossesMidnight when the end falls on or after
// midnight, and — when the manager asserts the tariff — with ErrOverlapsToU when
// the boost would run inside the Time-of-Use window. An unset tariff window
// overlaps nothing. A mode of Off, or a duration that is not a positive multiple
// of boostStep, is a caller bug and returns a plain error.
func PlanBoost(mode Mode, minutes int, now time.Time, tou inverter.TimedWindow, assertToU bool) (inverter.TimedSlot, error) {
	if mode != Charge && mode != Discharge {
		return inverter.TimedSlot{}, fmt.Errorf("plan boost: mode %s has no window to plan", mode)
	}
	if minutes <= 0 || minutes%boostStep != 0 {
		return inverter.TimedSlot{}, fmt.Errorf("plan boost: %d minutes is not a positive multiple of %d", minutes, boostStep)
	}

	start := minuteOf(now)
	end := (start/boostStep+1)*boostStep + minutes - boostStep
	if end >= minutesPerDay {
		return inverter.TimedSlot{}, fmt.Errorf("%d min boost from %s: %w", minutes, clockAt(start), ErrCrossesMidnight)
	}

	window := inverter.TimedWindow{Start: clockAt(start), End: clockAt(end)}
	if assertToU && overlaps(tou, start, end) {
		return inverter.TimedSlot{}, fmt.Errorf("boost %s against tariff %s: %w", FormatWindow(window), FormatWindow(tou), ErrOverlapsToU)
	}

	if mode == Discharge {
		return inverter.TimedSlot{Discharge: window}, nil
	}
	return inverter.TimedSlot{Charge: window}, nil
}

// BoostSelectState renders the select's state from the registers: "Off" for an
// empty boost slot, otherwise the option matching the boost's direction and its
// duration rounded up to the quarter-hour grid. A window the options cannot
// express — one an app set by hand, say — returns nil, which the state document
// publishes as JSON null so Home Assistant shows the select as unknown rather
// than misreporting it.
func BoostSelectState(slots inverter.TimedSlots) *string {
	boost := BoostOf(slots)
	if boost.Mode == Off {
		state := Off.String()
		return &state
	}
	minutes := roundUpToStep(endMinuteOfDay(boost.Window.End) - minuteOfDay(boost.Window.Start))
	if !slices.Contains(boostDurations, minutes) {
		return nil
	}
	state := boostOption(boost.Mode, minutes)
	return &state
}

// boostOption renders one select option, e.g. "Charge 30 min".
func boostOption(mode Mode, minutes int) string {
	return fmt.Sprintf("%s %d min", mode, minutes)
}

// overlaps reports whether the half-open minute-of-day range [start, end)
// intersects the window, which may itself cross midnight and then covers the
// union of its evening and morning halves. An unset window covers nothing.
func overlaps(w inverter.TimedWindow, start, end int) bool {
	if w.IsZero() {
		return false
	}
	windowStart, windowEnd := minuteOfDay(w.Start), endMinuteOfDay(w.End)
	if windowStart < windowEnd {
		return intersects(start, end, windowStart, windowEnd)
	}
	return intersects(start, end, windowStart, minutesPerDay) || intersects(start, end, 0, windowEnd)
}

// intersects reports whether two half-open minute ranges share a minute. Ranges
// that merely touch at a boundary do not.
func intersects(aStart, aEnd, bStart, bEnd int) bool {
	return aStart < bEnd && bStart < aEnd
}

// minuteOf converts an instant's wall clock, in its own location, to minutes
// since midnight, dropping the seconds.
func minuteOf(now time.Time) int {
	return now.Hour()*60 + now.Minute()
}

// minuteOfDay converts a clock to minutes since midnight.
func minuteOfDay(c inverter.Clock) int {
	return int(c.Hour)*60 + int(c.Minute)
}

// endMinuteOfDay converts a window's End clock to minutes since midnight,
// resolving 00:00 to end-of-day.
func endMinuteOfDay(c inverter.Clock) int {
	if isMidnight(c) {
		return minutesPerDay
	}
	return minuteOfDay(c)
}

// clockAt converts minutes since midnight back to a wall clock. The caller must
// keep the value inside the day.
func clockAt(minute int) inverter.Clock {
	return inverter.Clock{Hour: uint8(minute / 60), Minute: uint8(minute % 60)}
}

// roundUpToStep rounds a positive minute count up to the quarter-hour grid,
// leaving a zero or negative count alone so it cannot pass for a valid duration.
func roundUpToStep(minutes int) int {
	if minutes <= 0 {
		return minutes
	}
	return (minutes + boostStep - 1) / boostStep * boostStep
}
