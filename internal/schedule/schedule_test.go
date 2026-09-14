package schedule_test

import (
	"testing"
	"time"

	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
	"github.com/gsdevme/solis-inverter-manager/internal/schedule"
)

// win builds a window from its four wall-clock fields.
func win(startHour, startMinute, endHour, endMinute uint8) inverter.TimedWindow {
	return inverter.TimedWindow{
		Start: inverter.Clock{Hour: startHour, Minute: startMinute},
		End:   inverter.Clock{Hour: endHour, Minute: endMinute},
	}
}

// touSlots is the owner's live configuration: the Time-of-Use tariff split across
// slots 1 and 2 at midnight, plus an afternoon boost in slot 3.
var touSlots = inverter.TimedSlots{
	{Charge: win(23, 31, 0, 0)},
	{Charge: win(0, 0, 5, 29)},
	{Charge: win(14, 2, 14, 56)},
}

func TestToU(t *testing.T) {
	for _, tc := range []struct {
		name   string
		slots  inverter.TimedSlots
		want   inverter.TimedWindow
		wantOK bool
	}{
		{
			name:   "midnight-joined pair",
			slots:  touSlots,
			want:   win(23, 31, 5, 29),
			wantOK: true,
		},
		{
			name:   "slot 1 only",
			slots:  inverter.TimedSlots{{Charge: win(9, 0, 11, 30)}},
			want:   win(9, 0, 11, 30),
			wantOK: true,
		},
		{
			name:   "unconfigured",
			slots:  inverter.TimedSlots{},
			wantOK: false,
		},
		{
			name: "two windows that do not join at midnight",
			slots: inverter.TimedSlots{
				{Charge: win(9, 0, 11, 30)},
				{Charge: win(13, 0, 14, 0)},
			},
			wantOK: false,
		},
		{
			name: "slot 2 set but slot 1 unset",
			slots: inverter.TimedSlots{
				{},
				{Charge: win(0, 0, 5, 29)},
			},
			wantOK: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := schedule.ToU(tc.slots)
			if ok != tc.wantOK {
				t.Fatalf("ToU ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Errorf("ToU = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestModeString(t *testing.T) {
	for _, tc := range []struct {
		mode schedule.Mode
		want string
	}{
		{schedule.Off, "Off"},
		{schedule.Charge, "Charge"},
		{schedule.Discharge, "Discharge"},
		{schedule.Mode(9), "Mode(9)"},
	} {
		if got := tc.mode.String(); got != tc.want {
			t.Errorf("Mode(%d).String() = %q, want %q", int(tc.mode), got, tc.want)
		}
	}
}

func TestBoostOf(t *testing.T) {
	for _, tc := range []struct {
		name  string
		slots inverter.TimedSlots
		want  schedule.Boost
	}{
		{
			name:  "charge window in slot 3",
			slots: touSlots,
			want:  schedule.Boost{Mode: schedule.Charge, Window: win(14, 2, 14, 56)},
		},
		{
			name:  "discharge window in slot 3",
			slots: inverter.TimedSlots{{}, {}, {Discharge: win(17, 0, 19, 30)}},
			want:  schedule.Boost{Mode: schedule.Discharge, Window: win(17, 0, 19, 30)},
		},
		{
			name:  "charge wins when both are set",
			slots: inverter.TimedSlots{{}, {}, {Charge: win(14, 2, 14, 56), Discharge: win(17, 0, 19, 30)}},
			want:  schedule.Boost{Mode: schedule.Charge, Window: win(14, 2, 14, 56)},
		},
		{
			name:  "slot 3 unset",
			slots: inverter.TimedSlots{{Charge: win(23, 31, 0, 0)}, {Charge: win(0, 0, 5, 29)}},
			want:  schedule.Boost{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := schedule.BoostOf(tc.slots); got != tc.want {
				t.Errorf("BoostOf = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestBoostEndsAt(t *testing.T) {
	zone := time.FixedZone("BST", 3600)
	now := time.Date(2026, time.September, 13, 12, 0, 0, 0, zone)

	for _, tc := range []struct {
		name  string
		boost schedule.Boost
		want  time.Time
	}{
		{
			name:  "ends today",
			boost: schedule.Boost{Mode: schedule.Charge, Window: win(14, 2, 14, 56)},
			want:  time.Date(2026, time.September, 13, 14, 56, 0, 0, zone),
		},
		{
			name:  "end 00:00 means the next midnight",
			boost: schedule.Boost{Mode: schedule.Charge, Window: win(23, 31, 0, 0)},
			want:  time.Date(2026, time.September, 14, 0, 0, 0, 0, zone),
		},
		{
			name:  "off has no end",
			boost: schedule.Boost{},
			want:  time.Time{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.boost.EndsAt(now)
			if !got.Equal(tc.want) {
				t.Fatalf("EndsAt = %s, want %s", got, tc.want)
			}
			if got.Location() != tc.want.Location() {
				t.Errorf("EndsAt location = %s, want %s", got.Location(), tc.want.Location())
			}
		})
	}
}

func TestBoostString(t *testing.T) {
	for _, tc := range []struct {
		boost schedule.Boost
		want  string
	}{
		{schedule.Boost{}, "Off"},
		{schedule.Boost{Mode: schedule.Charge, Window: win(14, 2, 14, 56)}, "Charge until 14:56"},
		{schedule.Boost{Mode: schedule.Discharge, Window: win(17, 0, 19, 30)}, "Discharge until 19:30"},
	} {
		if got := tc.boost.String(); got != tc.want {
			t.Errorf("Boost%+v.String() = %q, want %q", tc.boost, got, tc.want)
		}
	}
}

func TestFormatWindow(t *testing.T) {
	for _, tc := range []struct {
		window inverter.TimedWindow
		want   string
	}{
		{win(23, 31, 5, 29), "23:31–05:29"},
		{inverter.TimedWindow{}, "00:00–00:00"},
	} {
		if got := schedule.FormatWindow(tc.window); got != tc.want {
			t.Errorf("FormatWindow(%+v) = %q, want %q", tc.window, got, tc.want)
		}
	}
}
