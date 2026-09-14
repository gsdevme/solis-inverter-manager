package schedule_test

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
	"github.com/gsdevme/solis-inverter-manager/internal/schedule"
)

// zone is a fixed offset so the tests never depend on the host's TZ database.
var zone = time.FixedZone("BST", 3600)

// at builds an instant on the tests' reference day from a wall clock.
func at(hour, minute, second int) time.Time {
	return time.Date(2026, time.September, 13, hour, minute, second, 0, zone)
}

// cl builds a wall clock from its hour and minute.
func cl(hour, minute uint8) inverter.Clock {
	return inverter.Clock{Hour: hour, Minute: minute}
}

// charge builds a slot holding only a charge window.
func charge(w inverter.TimedWindow) inverter.TimedSlot {
	return inverter.TimedSlot{Charge: w}
}

func TestParseToUWindow(t *testing.T) {
	for _, tc := range []struct {
		name        string
		in          string
		want        inverter.TimedWindow
		wantEnabled bool
		wantErr     bool
	}{
		{name: "empty disables assertion", in: ""},
		{name: "crosses midnight", in: "23:30-05:30", want: win(23, 30, 5, 30), wantEnabled: true},
		{name: "within one day", in: "01:00-04:00", want: win(1, 0, 4, 0), wantEnabled: true},
		{name: "ends at end of day", in: "22:00-00:00", want: win(22, 0, 0, 0), wantEnabled: true},
		{name: "starts at midnight", in: "00:00-05:30", want: win(0, 0, 5, 30), wantEnabled: true},
		{name: "no separator", in: "23:30", wantErr: true},
		{name: "three parts", in: "23:30-05:30-07:00", wantErr: true},
		{name: "start out of range", in: "25:00-05:30", wantErr: true},
		{name: "end malformed", in: "23:30-5:30", wantErr: true},
		{name: "start equals end", in: "05:30-05:30", wantErr: true},
		{name: "empty halves", in: "-", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, enabled, err := schedule.ParseToUWindow(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseToUWindow(%q) error = nil, want an error", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseToUWindow(%q) error = %v", tc.in, err)
			}
			if enabled != tc.wantEnabled {
				t.Errorf("ParseToUWindow(%q) enabled = %v, want %v", tc.in, enabled, tc.wantEnabled)
			}
			if got != tc.want {
				t.Errorf("ParseToUWindow(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestToUSlots(t *testing.T) {
	for _, tc := range []struct {
		name string
		tou  inverter.TimedWindow
		want [2]inverter.TimedSlot
	}{
		{
			name: "crosses midnight splits at 00:00",
			tou:  win(23, 30, 5, 30),
			want: [2]inverter.TimedSlot{charge(win(23, 30, 0, 0)), charge(win(0, 0, 5, 30))},
		},
		{
			name: "within one day uses slot 1 only",
			tou:  win(1, 0, 4, 0),
			want: [2]inverter.TimedSlot{charge(win(1, 0, 4, 0)), {}},
		},
		{
			name: "ends at end of day needs no second slot",
			tou:  win(22, 0, 0, 0),
			want: [2]inverter.TimedSlot{charge(win(22, 0, 0, 0)), {}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := schedule.ToUSlots(tc.tou); got != tc.want {
				t.Errorf("ToUSlots(%+v) = %+v, want %+v", tc.tou, got, tc.want)
			}
		})
	}
}

// tariffOnly is the Time-of-Use pair with no boost in slot 3.
var tariffOnly = inverter.TimedSlots{charge(win(23, 31, 0, 0)), charge(win(0, 0, 5, 29))}

// withSlot3 places a slot-3 window pair under the owner's tariff slots, so a row
// exercising expiry reads as "the tariff, plus this".
func withSlot3(slot inverter.TimedSlot) inverter.TimedSlots {
	slots := tariffOnly
	slots[2] = slot
	return slots
}

// staleCharge and runningDischarge are the windows the live defect left in slot
// 3: a charge whose end minute was never cleared, alongside the boost that had
// just been programmed over it.
var (
	staleCharge      = win(0, 0, 0, 56)
	runningDischarge = win(8, 58, 9, 0)
)

func TestDesired(t *testing.T) {
	tariff := win(23, 30, 5, 30)
	bothRunning := inverter.TimedSlot{Charge: win(8, 0, 9, 30), Discharge: win(10, 0, 11, 0)}
	bothStale := inverter.TimedSlot{Charge: staleCharge, Discharge: runningDischarge}

	for _, tc := range []struct {
		name      string
		tou       inverter.TimedWindow
		assertToU bool
		slots     inverter.TimedSlots
		now       time.Time
		want      inverter.TimedSlots
	}{
		{
			name:      "asserts the tariff and keeps a live boost",
			tou:       tariff,
			assertToU: true,
			slots:     touSlots,
			now:       at(14, 30, 0),
			want: inverter.TimedSlots{
				charge(win(23, 30, 0, 0)),
				charge(win(0, 0, 5, 30)),
				charge(win(14, 2, 14, 56)),
			},
		},
		{
			name:      "clears an expired boost",
			tou:       tariff,
			assertToU: true,
			slots:     touSlots,
			now:       at(15, 0, 0),
			want: inverter.TimedSlots{
				charge(win(23, 30, 0, 0)),
				charge(win(0, 0, 5, 30)),
				{},
			},
		},
		{
			name:  "leaves the tariff slots alone when assertion is off",
			tou:   tariff,
			slots: touSlots,
			now:   at(14, 30, 0),
			want:  touSlots,
		},
		{
			name:  "clears an expired boost with assertion off",
			tou:   tariff,
			slots: touSlots,
			now:   at(15, 0, 0),
			want:  withSlot3(inverter.TimedSlot{}),
		},
		{
			// Belt and braces: ParseToUWindow never reports enabled for an empty
			// setting, but asserting a zero window would clear the owner's tariff
			// rather than leave it alone, so Desired refuses it outright.
			name:      "never asserts a zero tariff window",
			assertToU: true,
			slots:     touSlots,
			now:       at(14, 30, 0),
			want:      touSlots,
		},
		{
			name:  "keeps a boost before its end",
			slots: touSlots,
			now:   at(14, 55, 0),
			want:  touSlots,
		},
		{
			name:  "clears a boost at its end minute",
			slots: touSlots,
			now:   at(14, 56, 0),
			want:  withSlot3(inverter.TimedSlot{}),
		},
		{
			name:  "keeps an end-of-day boost before midnight",
			slots: withSlot3(charge(win(23, 31, 0, 0))),
			now:   at(23, 59, 0),
			want:  withSlot3(charge(win(23, 31, 0, 0))),
		},
		{
			name:  "an empty boost slot stays empty",
			slots: tariffOnly,
			now:   at(15, 0, 0),
			want:  tariffOnly,
		},
		{
			name:  "keeps a running discharge and clears a stale charge",
			slots: withSlot3(bothStale),
			now:   at(8, 59, 0),
			want:  withSlot3(inverter.TimedSlot{Discharge: runningDischarge}),
		},
		{
			name:  "keeps a running charge and clears a stale discharge",
			slots: withSlot3(inverter.TimedSlot{Charge: runningDischarge, Discharge: staleCharge}),
			now:   at(8, 59, 0),
			want:  withSlot3(inverter.TimedSlot{Charge: runningDischarge}),
		},
		{
			name:  "clears the slot once both windows have ended",
			slots: withSlot3(bothStale),
			now:   at(9, 30, 0),
			want:  withSlot3(inverter.TimedSlot{}),
		},
		{
			name:  "keeps both windows while both run",
			slots: withSlot3(bothRunning),
			now:   at(8, 59, 0),
			want:  withSlot3(bothRunning),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := schedule.Desired(tc.tou, tc.assertToU, tc.slots, tc.now)
			if got != tc.want {
				t.Errorf("Desired = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestBoostOptions(t *testing.T) {
	want := []string{
		"Off",
		"Charge 15 min", "Charge 30 min", "Charge 45 min", "Charge 60 min",
		"Discharge 15 min", "Discharge 30 min", "Discharge 45 min", "Discharge 60 min",
	}
	got := schedule.BoostOptions()
	if len(got) != len(want) {
		t.Fatalf("BoostOptions() = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("BoostOptions()[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	got[0] = "mutated"
	if schedule.BoostOptions()[0] != want[0] {
		t.Error("BoostOptions() shares backing storage between calls")
	}
}

func TestParseBoostOption(t *testing.T) {
	for _, option := range schedule.BoostOptions() {
		mode, minutes, ok := schedule.ParseBoostOption(option)
		if !ok {
			t.Fatalf("ParseBoostOption(%q) ok = false, want true", option)
		}
		if option == "Off" {
			if mode != schedule.Off || minutes != 0 {
				t.Errorf("ParseBoostOption(%q) = %v, %d, want Off, 0", option, mode, minutes)
			}
			continue
		}
		if want := mode.String() + " " + strconv.Itoa(minutes) + " min"; want != option {
			t.Errorf("ParseBoostOption(%q) = %v, %d, which renders as %q", option, mode, minutes, want)
		}
	}

	for _, in := range []string{"  Charge 30 min  ", "Off"} {
		if _, _, ok := schedule.ParseBoostOption(in); !ok {
			t.Errorf("ParseBoostOption(%q) ok = false, want true", in)
		}
	}

	for _, in := range []string{"", "charge 30 min", "Charge 20 min", "Charge", "Boost 30 min"} {
		if _, _, ok := schedule.ParseBoostOption(in); ok {
			t.Errorf("ParseBoostOption(%q) ok = true, want false", in)
		}
	}
}

func TestPlanBoostQuarterHourSnap(t *testing.T) {
	for _, tc := range []struct {
		minute int
		ends   [4]inverter.Clock // for 15, 30, 45 and 60 minutes
	}{
		{minute: 0, ends: [4]inverter.Clock{cl(14, 15), cl(14, 30), cl(14, 45), cl(15, 0)}},
		{minute: 7, ends: [4]inverter.Clock{cl(14, 15), cl(14, 30), cl(14, 45), cl(15, 0)}},
		{minute: 15, ends: [4]inverter.Clock{cl(14, 30), cl(14, 45), cl(15, 0), cl(15, 15)}},
		{minute: 29, ends: [4]inverter.Clock{cl(14, 30), cl(14, 45), cl(15, 0), cl(15, 15)}},
		{minute: 44, ends: [4]inverter.Clock{cl(14, 45), cl(15, 0), cl(15, 15), cl(15, 30)}},
		{minute: 59, ends: [4]inverter.Clock{cl(15, 0), cl(15, 15), cl(15, 30), cl(15, 45)}},
	} {
		for i, minutes := range []int{15, 30, 45, 60} {
			now := at(14, tc.minute, 33)
			want := inverter.TimedSlot{Charge: inverter.TimedWindow{
				Start: inverter.Clock{Hour: 14, Minute: uint8(tc.minute)},
				End:   tc.ends[i],
			}}
			t.Run(now.Format("15:04")+"+"+strconv.Itoa(minutes), func(t *testing.T) {
				got, err := schedule.PlanBoost(schedule.Charge, minutes, now, inverter.TimedWindow{}, false)
				if err != nil {
					t.Fatalf("PlanBoost error = %v", err)
				}
				if got != want {
					t.Errorf("PlanBoost = %+v, want %+v", got, want)
				}
			})
		}
	}
}

func TestPlanBoostDischarge(t *testing.T) {
	got, err := schedule.PlanBoost(schedule.Discharge, 30, at(17, 7, 0), inverter.TimedWindow{}, false)
	if err != nil {
		t.Fatalf("PlanBoost error = %v", err)
	}
	want := inverter.TimedSlot{Discharge: win(17, 7, 17, 30)}
	if got != want {
		t.Errorf("PlanBoost = %+v, want %+v", got, want)
	}
}

func TestPlanBoostRejections(t *testing.T) {
	tou := win(23, 30, 5, 30)

	for _, tc := range []struct {
		name      string
		mode      schedule.Mode
		minutes   int
		now       time.Time
		tou       inverter.TimedWindow
		assertToU bool
		wantErr   error
	}{
		{
			name:    "end crosses into tomorrow",
			mode:    schedule.Charge,
			minutes: 15,
			now:     at(23, 50, 0),
			wantErr: schedule.ErrCrossesMidnight,
		},
		{
			name:    "end exactly at midnight",
			mode:    schedule.Charge,
			minutes: 15,
			now:     at(23, 45, 0),
			wantErr: schedule.ErrCrossesMidnight,
		},
		{
			name:      "runs into the tariff window",
			mode:      schedule.Charge,
			minutes:   30,
			now:       at(23, 20, 0),
			tou:       tou,
			assertToU: true,
			wantErr:   schedule.ErrOverlapsToU,
		},
		{
			name:      "ends inside the tariff window's morning half",
			mode:      schedule.Discharge,
			minutes:   15,
			now:       at(5, 20, 0),
			tou:       tou,
			assertToU: true,
			wantErr:   schedule.ErrOverlapsToU,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := schedule.PlanBoost(tc.mode, tc.minutes, tc.now, tc.tou, tc.assertToU)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("PlanBoost error = %v, want %v", err, tc.wantErr)
			}
			if got != (inverter.TimedSlot{}) {
				t.Errorf("PlanBoost = %+v, want the zero slot on rejection", got)
			}
		})
	}
}

func TestPlanBoostToUBoundaries(t *testing.T) {
	tou := win(23, 30, 5, 30)

	for _, tc := range []struct {
		name      string
		minutes   int
		now       time.Time
		tou       inverter.TimedWindow
		assertToU bool
		want      inverter.TimedSlot
	}{
		{
			name:      "starting as the tariff window ends does not overlap",
			minutes:   15,
			now:       at(5, 30, 0),
			tou:       tou,
			assertToU: true,
			want:      inverter.TimedSlot{Charge: win(5, 30, 5, 45)},
		},
		{
			name:    "overlap is not checked when assertion is off",
			minutes: 30,
			now:     at(23, 20, 0),
			tou:     tou,
			want:    inverter.TimedSlot{Charge: win(23, 20, 23, 45)},
		},
		{
			name:      "an unset tariff window never overlaps",
			minutes:   15,
			now:       at(5, 20, 0),
			assertToU: true,
			want:      inverter.TimedSlot{Charge: win(5, 20, 5, 30)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := schedule.PlanBoost(schedule.Charge, tc.minutes, tc.now, tc.tou, tc.assertToU)
			if err != nil {
				t.Fatalf("PlanBoost error = %v", err)
			}
			if got != tc.want {
				t.Errorf("PlanBoost = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestPlanBoostProgrammingErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    schedule.Mode
		minutes int
	}{
		{name: "mode off", mode: schedule.Off, minutes: 15},
		{name: "zero minutes", mode: schedule.Charge},
		{name: "negative minutes", mode: schedule.Charge, minutes: -15},
		{name: "not a quarter hour", mode: schedule.Charge, minutes: 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := schedule.PlanBoost(tc.mode, tc.minutes, at(14, 7, 0), inverter.TimedWindow{}, false)
			if err == nil {
				t.Fatal("PlanBoost error = nil, want an error")
			}
			if errors.Is(err, schedule.ErrCrossesMidnight) || errors.Is(err, schedule.ErrOverlapsToU) {
				t.Errorf("PlanBoost error = %v, want a plain error, not a rejection sentinel", err)
			}
		})
	}
}

func TestBoostSelectState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		slots inverter.TimedSlots
		want  *string
	}{
		{name: "empty slot 3 is off", slots: tariffOnly, want: ptr("Off")},
		{name: "23 minutes rounds up to 30", slots: inverter.TimedSlots{{}, {}, charge(win(14, 7, 14, 30))}, want: ptr("Charge 30 min")},
		{name: "54 minutes rounds up to 60", slots: inverter.TimedSlots{{}, {}, charge(win(14, 2, 14, 56))}, want: ptr("Charge 60 min")},
		{name: "an exact quarter hour maps directly", slots: inverter.TimedSlots{{}, {}, charge(win(14, 15, 14, 30))}, want: ptr("Charge 15 min")},
		{
			name:  "a discharge window maps to the discharge option",
			slots: inverter.TimedSlots{{}, {}, {Discharge: win(17, 7, 17, 30)}},
			want:  ptr("Discharge 30 min"),
		},
		{name: "an app-set 90 minute window is unmappable", slots: inverter.TimedSlots{{}, {}, charge(win(14, 0, 15, 30))}},
		{
			name:  "a window running to end of day measures to 24:00",
			slots: inverter.TimedSlots{{}, {}, charge(win(23, 31, 0, 0))},
			want:  ptr("Charge 30 min"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := schedule.BoostSelectState(tc.slots)
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("BoostSelectState = %q, want nil", *got)
			case tc.want != nil && got == nil:
				t.Fatalf("BoostSelectState = nil, want %q", *tc.want)
			case tc.want != nil && *got != *tc.want:
				t.Errorf("BoostSelectState = %q, want %q", *got, *tc.want)
			}
		})
	}
}

// ptr takes the address of a string literal for the *string expectations.
func ptr(s string) *string { return &s }
