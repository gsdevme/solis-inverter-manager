package controls_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/gsdevme/solis-inverter-manager/internal/controls"
	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
)

// quietLogger discards handler log output so tests stay silent.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// counter is a refresh callback that records how many times it fired.
type counter struct{ n int }

func (c *counter) refresh(context.Context) { c.n++ }

// captureLogger logs to buf so a test can assert on the text of a warning.
func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, nil))
}

// touWindow is the tariff the control tests configure: 23:30-05:30, a window
// that crosses midnight and so splits across slots 1 and 2.
var touWindow = inverter.TimedWindow{Start: clock(23, 30), End: clock(5, 30)}

// clock builds a wall clock, keeping the slot literals in the tests readable.
func clock(hour, minute uint8) inverter.Clock {
	return inverter.Clock{Hour: hour, Minute: minute}
}

// at returns a fixed-clock option for the given time of day on the test date.
func at(hour, minute int) controls.Option {
	return controls.WithNow(func() time.Time {
		return time.Date(2026, 9, 12, hour, minute, 0, 0, time.UTC)
	})
}

// wantWrites asserts the fake saw exactly these WriteHolding calls, in order.
func wantWrites(t *testing.T, f *fakeRW, want []writeCall) {
	t.Helper()
	if len(f.writeCalls) != len(want) {
		t.Fatalf("writeCalls = %v, want exactly %v", f.writeCalls, want)
	}
	for i, w := range want {
		if f.writeCalls[i] != w {
			t.Errorf("writeCalls[%d] = %v, want %v", i, f.writeCalls[i], w)
		}
	}
}

// TestSetChargeCurrentInRangeWrites: an in-range set_charge_current encodes
// EncodeAmps(ClampHAChargeAmps(v)) and guards the write, and a routed write
// fires the refresh callback. DoD 4 + DoD 12 (positive).
func TestSetChargeCurrentInRangeWrites(t *testing.T) {
	f := newFakeRW()
	f.regs[43141] = 0
	var c counter
	h := controls.NewHandler(f, quietLogger(), true, c.refresh)

	h.OnMessage(context.Background(), "solis/cmd/set_charge_current/set", []byte("30"))

	if len(f.writeCalls) != 1 || f.writeCalls[0] != (writeCall{43141, 300}) {
		t.Errorf("writeCalls = %v, want one write of 43141=300", f.writeCalls)
	}
	if c.n != 1 {
		t.Errorf("refresh fired %d times, want 1 after a routed write", c.n)
	}
}

// TestSetDischargeCurrentRoutesTo43142 confirms discharge maps to register 43142
// with the same encode path. DoD 4 (routing).
func TestSetDischargeCurrentRoutesTo43142(t *testing.T) {
	f := newFakeRW()
	f.regs[43142] = 0
	var c counter
	h := controls.NewHandler(f, quietLogger(), true, c.refresh)

	h.OnMessage(context.Background(), "solis/cmd/set_discharge_current/set", []byte("15.5"))

	if len(f.writeCalls) != 1 || f.writeCalls[0] != (writeCall{43142, 155}) {
		t.Errorf("writeCalls = %v, want one write of 43142=155", f.writeCalls)
	}
}

// TestInvalidAmpsRejected: unparseable/NaN/empty payloads are dropped with ZERO
// writes, no panic, and no refresh. DoD 5 + DoD 12 (negative).
func TestInvalidAmpsRejected(t *testing.T) {
	for _, payload := range []string{"abc", "NaN", "", "Inf"} {
		t.Run(payload, func(t *testing.T) {
			f := newFakeRW()
			var c counter
			h := controls.NewHandler(f, quietLogger(), true, c.refresh)

			h.OnMessage(context.Background(), "solis/cmd/set_charge_current/set", []byte(payload))

			if len(f.writeCalls) != 0 {
				t.Errorf("payload %q: writeCalls = %v, want ZERO", payload, f.writeCalls)
			}
			if c.n != 0 {
				t.Errorf("payload %q: refresh fired %d times, want 0 on reject", payload, c.n)
			}
		})
	}
}

// TestOutOfRangeAmpsClamped: a finite-but-out-of-range value is CLAMPED to the
// HA maximum (60 A -> 600), not rejected (Ruling R6). DoD 6.
func TestOutOfRangeAmpsClamped(t *testing.T) {
	f := newFakeRW()
	f.regs[43141] = 0
	var c counter
	h := controls.NewHandler(f, quietLogger(), true, c.refresh)

	h.OnMessage(context.Background(), "solis/cmd/set_charge_current/set", []byte("200"))

	if len(f.writeCalls) != 1 || f.writeCalls[0] != (writeCall{43141, 600}) {
		t.Errorf("writeCalls = %v, want one clamped write of 43141=600", f.writeCalls)
	}
	if c.n != 1 {
		t.Errorf("refresh fired %d times, want 1 (clamp is a routed write)", c.n)
	}
}

// TestOptimalIncomeFlipsOnlyBit1 flips bit 1 of the 43110 work-mode word while
// preserving every other bit (read-modify-write). The select speaks the Solis
// app's vocabulary, Run/Stop. DoD 7 (positive).
func TestOptimalIncomeFlipsOnlyBit1(t *testing.T) {
	tests := []struct {
		name    string
		current uint16
		payload string
		want    uint16
	}{
		{"run 33->35", 33, "Run", 35},
		{"stop 35->33", 35, "Stop", 33},
		// Bit 8 (256) set alongside the known flags must survive the flip: a
		// Stop word 289 (33|256) set to Run becomes 291 (35|256), not a bare 35.
		{"preserves extra bits", 289, "run", 291},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeRW()
			f.regs[43110] = tc.current
			var c counter
			h := controls.NewHandler(f, quietLogger(), true, c.refresh)

			h.OnMessage(context.Background(), "solis/cmd/optimal_income/set", []byte(tc.payload))

			if len(f.writeCalls) != 1 || f.writeCalls[0] != (writeCall{43110, tc.want}) {
				t.Errorf("writeCalls = %v, want one write of 43110=%d", f.writeCalls, tc.want)
			}
			if c.n != 1 {
				t.Errorf("refresh fired %d times, want 1", c.n)
			}
		})
	}
}

// TestOptimalIncomeRejectsBadPayload: anything but Run/Stop is dropped with ZERO
// writes and no refresh — including the switch's old ON payload, which the
// select no longer speaks. DoD 7 (negative).
func TestOptimalIncomeRejectsBadPayload(t *testing.T) {
	for _, payload := range []string{"banana", "ON", ""} {
		t.Run(payload, func(t *testing.T) {
			f := newFakeRW()
			f.regs[43110] = 33
			var c counter
			h := controls.NewHandler(f, quietLogger(), true, c.refresh)

			h.OnMessage(context.Background(), "solis/cmd/optimal_income/set", []byte(payload))

			if len(f.writeCalls) != 0 {
				t.Errorf("payload %q: writeCalls = %v, want ZERO for a non-Run/Stop payload", payload, f.writeCalls)
			}
			if c.n != 0 {
				t.Errorf("payload %q: refresh fired %d times, want 0 on reject", payload, c.n)
			}
		})
	}
}

// TestRTCSyncWritesOnlyDrifted loops the six RTC holding registers (43000..43005)
// and, because each is guarded, writes only the ones whose value differs from the
// clock. Seeding two registers to the expected value proves they are skipped.
// DoD 8.
func TestRTCSyncWritesOnlyDrifted(t *testing.T) {
	// Fixed clock -> [year-2000=26, mo=9, d=12, h=20, mi=34, s=56].
	now := time.Date(2026, 9, 12, 20, 34, 56, 0, time.Local)
	f := newFakeRW()
	f.regs[43000] = 26 // year already correct -> skipped
	f.regs[43001] = 9  // month already correct -> skipped
	// 43002..43005 left at zero -> all differ -> written.

	var c counter
	h := controls.NewHandler(f, quietLogger(), true, c.refresh, controls.WithNow(func() time.Time { return now }))

	h.OnMessage(context.Background(), "solis/cmd/rtc_sync/set", nil)

	if f.wroteTo(43000) {
		t.Error("wrote 43000 (year), want skipped (already equal)")
	}
	if f.wroteTo(43001) {
		t.Error("wrote 43001 (month), want skipped (already equal)")
	}
	want := []writeCall{{43002, 12}, {43003, 20}, {43004, 34}, {43005, 56}}
	if len(f.writeCalls) != len(want) {
		t.Fatalf("writeCalls = %v, want exactly the four drifted regs %v", f.writeCalls, want)
	}
	for i, w := range want {
		if f.writeCalls[i] != w {
			t.Errorf("writeCalls[%d] = %v, want %v", i, f.writeCalls[i], w)
		}
	}
	if c.n != 1 {
		t.Errorf("refresh fired %d times, want 1 after rtc_sync", c.n)
	}
}

// TestMalformedAndUnknownDropped: topics without a trailing /set, an empty key,
// too-short topics, and unknown command keys are all dropped with ZERO writes,
// no panic, and no refresh. DoD 11 + DoD 12 (negative).
func TestMalformedAndUnknownDropped(t *testing.T) {
	for _, topic := range []string{
		"solis/cmd/set_charge_current", // no trailing /set
		"solis/foo/bar",                // last segment is not "set"
		"solis//set",                   // empty key before /set
		"",                             // too short to route
		"solis/cmd/bogus/set",          // well-formed but unknown key
	} {
		t.Run(topic, func(t *testing.T) {
			f := newFakeRW()
			var c counter
			h := controls.NewHandler(f, quietLogger(), true, c.refresh)

			h.OnMessage(context.Background(), topic, []byte("30"))

			if len(f.writeCalls) != 0 {
				t.Errorf("topic %q: writeCalls = %v, want ZERO", topic, f.writeCalls)
			}
			if c.n != 0 {
				t.Errorf("topic %q: refresh fired %d times, want 0", topic, c.n)
			}
		})
	}
}

// TestDisabledDropsEveryCommand: the CONTROLS_ENABLED kill-switch drops an
// otherwise-valid command without touching the inverter or refreshing.
func TestDisabledDropsEveryCommand(t *testing.T) {
	f := newFakeRW()
	f.regs[43141] = 0
	var c counter
	h := controls.NewHandler(f, quietLogger(), false, c.refresh)

	h.OnMessage(context.Background(), "solis/cmd/set_charge_current/set", []byte("30"))

	if len(f.writeCalls) != 0 {
		t.Errorf("writeCalls = %v, want ZERO when disabled", f.writeCalls)
	}
	if c.n != 0 {
		t.Errorf("refresh fired %d times, want 0 when disabled", c.n)
	}
}

// TestNilRefreshDoesNotPanic: a routed write with no refresh callback registered
// must still write and must not panic.
func TestNilRefreshDoesNotPanic(t *testing.T) {
	f := newFakeRW()
	f.regs[43141] = 0
	h := controls.NewHandler(f, quietLogger(), true, nil)

	h.OnMessage(context.Background(), "solis/cmd/set_charge_current/set", []byte("30"))

	if len(f.writeCalls) != 1 {
		t.Errorf("writeCalls = %v, want one write even with a nil refresh", f.writeCalls)
	}
}

// TestBoostChargeWritesSlotThreeInOrder: "Charge 30 min" at 14:07 snaps the end
// to the 14:30 quarter-hour boundary and writes slot 3's charge window one
// register at a time in start-hour, start-minute, end-hour, end-minute order.
// The discharge half is asserted empty, which costs no write because the slot is
// already clear.
func TestBoostChargeWritesSlotThreeInOrder(t *testing.T) {
	f := newFakeRW()
	f.regs[43110] = inverter.WorkModeTimedOn
	var c counter
	h := controls.NewHandler(f, quietLogger(), true, c.refresh, at(14, 7), controls.WithToU(touWindow, true))

	h.OnMessage(context.Background(), "solis/cmd/boost_select/set", []byte("Charge 30 min"))

	wantWrites(t, f, []writeCall{{43163, 14}, {43164, 7}, {43165, 14}, {43166, 30}})
	if c.n != 1 {
		t.Errorf("refresh fired %d times, want 1 after a boost write", c.n)
	}
}

// TestBoostDischargeWritesSlotThreeDischargeWindow: the discharge option writes
// the other four registers of slot 3 and leaves the charge half clear.
func TestBoostDischargeWritesSlotThreeDischargeWindow(t *testing.T) {
	f := newFakeRW()
	f.regs[43110] = inverter.WorkModeTimedOn
	var c counter
	h := controls.NewHandler(f, quietLogger(), true, c.refresh, at(14, 7), controls.WithToU(touWindow, true))

	h.OnMessage(context.Background(), "solis/cmd/boost_select/set", []byte("Discharge 15 min"))

	wantWrites(t, f, []writeCall{{43167, 14}, {43168, 7}, {43169, 14}, {43170, 15}})
}

// TestBoostRetriesARegisterLostToATransportFailure: the live defect. A Discharge
// boost replacing a charge window loses the guard's read of 43166 to a one-off
// sidecar timeout; the register is retried immediately, in place, so the old
// window is fully cleared before the new one is programmed and the sequence runs
// on to its end. The retried line is tagged retry=true so an operator can tell it
// apart from the first-pass line for the same address; the first-pass lines carry
// no such tag.
func TestBoostRetriesARegisterLostToATransportFailure(t *testing.T) {
	var buf bytes.Buffer
	f := newFakeRW()
	f.regs[43110] = inverter.WorkModeTimedOn
	f.regs[43163], f.regs[43164], f.regs[43165], f.regs[43166] = 14, 2, 14, 56
	f.failReadOnce = map[int]error{43166: errBoom}
	h := controls.NewHandler(f, captureLogger(&buf), true, nil, at(8, 58), controls.WithToU(touWindow, true))

	h.OnMessage(context.Background(), "solis/cmd/boost_select/set", []byte("Discharge 15 min"))

	wantWrites(t, f, []writeCall{
		{43163, 0}, {43164, 0}, {43165, 0}, {43166, 0},
		{43167, 8}, {43168, 58}, {43169, 9},
	})
	if f.regs[43166] != 0 {
		t.Errorf("43166 = %d, want 0 once the retry lands", f.regs[43166])
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	var retried, first int
	for _, line := range lines {
		if !strings.Contains(line, "addr=43166") {
			continue
		}
		if strings.Contains(line, "retry=true") {
			retried++
		} else {
			first++
		}
	}
	if retried != 1 {
		t.Errorf("addr=43166 lines with retry=true = %d, want 1", retried)
	}
	if first != 1 {
		t.Errorf("addr=43166 lines without retry=true = %d, want 1 (the first pass)", first)
	}
}

// TestBoostAbortsWhenClearingTheOldDirectionFails: a register still failing after
// its retry stops the whole command. The discharge window slot 3 is holding
// cannot be cleared, so no charge register is written either: the slot keeps the
// one window it already had — which expires by itself — rather than gaining a
// second one the inverter has never been probed holding.
func TestBoostAbortsWhenClearingTheOldDirectionFails(t *testing.T) {
	f := newFakeRW()
	f.regs[43110] = inverter.WorkModeTimedOn
	f.regs[43167], f.regs[43168], f.regs[43169], f.regs[43170] = 8, 58, 9, 9
	f.failRead = map[int]error{43167: errBoom}
	h := controls.NewHandler(f, quietLogger(), true, nil, at(14, 7), controls.WithToU(touWindow, true))

	h.OnMessage(context.Background(), "solis/cmd/boost_select/set", []byte("Charge 30 min"))

	wantWrites(t, f, nil)
	if got := f.readsOf(43167); got != 2 {
		t.Errorf("reads of 43167 = %d, want 2 (the attempt and one retry)", got)
	}
	got := [4]uint16{f.regs[43167], f.regs[43168], f.regs[43169], f.regs[43170]}
	if want := [4]uint16{8, 58, 9, 9}; got != want {
		t.Errorf("discharge registers = %v, want %v (the old window left intact)", got, want)
	}
}

// TestBoostAbortsAfterAnUnconfirmedWrite: a write whose re-read does not confirm
// is not retried — the inverter took it — but it does abort the sequence, so the
// registers after it are left alone for the next poll to reason about.
func TestBoostAbortsAfterAnUnconfirmedWrite(t *testing.T) {
	f := newFakeRW()
	f.regs[43110] = inverter.WorkModeTimedOn
	f.overrideReread = map[int]uint16{43164: 99}
	h := controls.NewHandler(f, quietLogger(), true, nil, at(14, 7), controls.WithToU(touWindow, true))

	h.OnMessage(context.Background(), "solis/cmd/boost_select/set", []byte("Charge 30 min"))

	wantWrites(t, f, []writeCall{{43163, 14}, {43164, 7}})
	if got := f.readsOf(43164); got != 2 {
		t.Errorf("reads of 43164 = %d, want 2 (its own read and re-read, with no retry)", got)
	}
}

// TestBoostSwapsDirectionClearingTheOldWindowFirst: a Charge boost replacing a
// running Discharge boost must never leave slot 3 holding both windows, a state
// the inverter has never been probed in. The unused direction is cleared first,
// so the four discharge zeros land before the charge window is programmed.
func TestBoostSwapsDirectionClearingTheOldWindowFirst(t *testing.T) {
	f := newFakeRW()
	f.regs[43110] = inverter.WorkModeTimedOn
	f.regs[43167], f.regs[43168], f.regs[43169], f.regs[43170] = 13, 5, 14, 45
	var c counter
	h := controls.NewHandler(f, quietLogger(), true, c.refresh, at(14, 7), controls.WithToU(touWindow, true))

	h.OnMessage(context.Background(), "solis/cmd/boost_select/set", []byte("Charge 30 min"))

	wantWrites(t, f, []writeCall{
		{43167, 0}, {43168, 0}, {43169, 0}, {43170, 0},
		{43163, 14}, {43164, 7}, {43165, 14}, {43166, 30},
	})
	if c.n != 1 {
		t.Errorf("refresh fired %d times, want 1 after a boost write", c.n)
	}
}

// TestBoostOffClearsSlotThree: selecting Off zeroes the running window in the
// same register order; the already-clear discharge half is skipped.
func TestBoostOffClearsSlotThree(t *testing.T) {
	f := newFakeRW()
	f.regs[43163], f.regs[43164], f.regs[43165], f.regs[43166] = 14, 7, 14, 30
	var c counter
	h := controls.NewHandler(f, quietLogger(), true, c.refresh, at(14, 7), controls.WithToU(touWindow, true))

	h.OnMessage(context.Background(), "solis/cmd/boost_select/set", []byte("Off"))

	wantWrites(t, f, []writeCall{{43163, 0}, {43164, 0}, {43165, 0}, {43166, 0}})
	if c.n != 1 {
		t.Errorf("refresh fired %d times, want 1 after a clear", c.n)
	}
}

// TestBoostRejectedWhenOptimalIncomeStop: the manager never enables the timed
// schedule implicitly, so a boost requested while 43110 bit 1 is Stop is refused
// with a warning and no write. It still refreshes, which republishes the derived
// state and snaps the select back to Off.
func TestBoostRejectedWhenOptimalIncomeStop(t *testing.T) {
	var buf bytes.Buffer
	f := newFakeRW()
	f.regs[43110] = 33 // bit 1 clear: Optimal Income Stop
	var c counter
	h := controls.NewHandler(f, captureLogger(&buf), true, c.refresh, at(14, 7), controls.WithToU(touWindow, true))

	h.OnMessage(context.Background(), "solis/cmd/boost_select/set", []byte("Charge 30 min"))

	if len(f.writeCalls) != 0 {
		t.Errorf("writeCalls = %v, want ZERO while Optimal Income is Stop", f.writeCalls)
	}
	if !strings.Contains(buf.String(), "boost rejected: optimal income is Stop") {
		t.Errorf("log = %q, want the Stop rejection warning", buf.String())
	}
	if c.n != 1 {
		t.Errorf("refresh fired %d times, want 1 so the select snaps back to Off", c.n)
	}
}

// TestBoostRejectedByPlan covers the two window rejections: a boost whose end
// would land on or after midnight, and one that would run inside the asserted
// tariff window. Both warn and write nothing, and both refresh.
func TestBoostRejectedByPlan(t *testing.T) {
	tests := []struct {
		name          string
		hour, minute  int
		option        string
		wantLogReason string
	}{
		{"crosses midnight", 23, 50, "Charge 60 min", "boost would cross midnight"},
		{"overlaps tariff", 23, 31, "Charge 15 min", "boost overlaps the time-of-use window"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			f := newFakeRW()
			f.regs[43110] = inverter.WorkModeTimedOn
			var c counter
			h := controls.NewHandler(f, captureLogger(&buf), true, c.refresh, at(tc.hour, tc.minute), controls.WithToU(touWindow, true))

			h.OnMessage(context.Background(), "solis/cmd/boost_select/set", []byte(tc.option))

			if len(f.writeCalls) != 0 {
				t.Errorf("writeCalls = %v, want ZERO for a rejected boost", f.writeCalls)
			}
			if !strings.Contains(buf.String(), tc.wantLogReason) {
				t.Errorf("log = %q, want the reason %q", buf.String(), tc.wantLogReason)
			}
			if c.n != 1 {
				t.Errorf("refresh fired %d times, want 1 so the select snaps back to Off", c.n)
			}
		})
	}
}

// TestBoostInvalidOptionDropped: a payload that is not one of the select's
// options is dropped like any other bad command — no read, no write, no refresh.
func TestBoostInvalidOptionDropped(t *testing.T) {
	for _, payload := range []string{"Charge 20 min", "banana", ""} {
		t.Run(payload, func(t *testing.T) {
			var buf bytes.Buffer
			f := newFakeRW()
			f.regs[43110] = inverter.WorkModeTimedOn
			var c counter
			h := controls.NewHandler(f, captureLogger(&buf), true, c.refresh, at(14, 7), controls.WithToU(touWindow, true))

			h.OnMessage(context.Background(), "solis/cmd/boost_select/set", []byte(payload))

			if len(f.writeCalls) != 0 || len(f.holdCalls) != 0 {
				t.Errorf("holdCalls = %v, writeCalls = %v, want ZERO of each", f.holdCalls, f.writeCalls)
			}
			if !strings.Contains(buf.String(), "invalid boost option") {
				t.Errorf("log = %q, want the invalid-option warning", buf.String())
			}
			if c.n != 0 {
				t.Errorf("refresh fired %d times, want 0 on reject", c.n)
			}
		})
	}
}
