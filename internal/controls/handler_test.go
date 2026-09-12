package controls_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/gsdevme/solis-inverter-manager/internal/controls"
)

// quietLogger discards handler log output so tests stay silent.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// counter is a refresh callback that records how many times it fired.
type counter struct{ n int }

func (c *counter) refresh(context.Context) { c.n++ }

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
// preserving every other bit (read-modify-write). DoD 7 (positive).
func TestOptimalIncomeFlipsOnlyBit1(t *testing.T) {
	tests := []struct {
		name    string
		current uint16
		payload string
		want    uint16
	}{
		{"on 33->35", 33, "ON", 35},
		{"off 35->33", 35, "OFF", 33},
		// Bit 8 (256) set alongside the known flags must survive the flip: an
		// OFF word 289 (33|256) turned ON becomes 291 (35|256), not a bare 35.
		{"preserves extra bits", 289, "on", 291},
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

// TestOptimalIncomeRejectsBadPayload: anything but ON/OFF is dropped with ZERO
// writes and no refresh. DoD 7 (negative).
func TestOptimalIncomeRejectsBadPayload(t *testing.T) {
	f := newFakeRW()
	f.regs[43110] = 33
	var c counter
	h := controls.NewHandler(f, quietLogger(), true, c.refresh)

	h.OnMessage(context.Background(), "solis/cmd/optimal_income/set", []byte("banana"))

	if len(f.writeCalls) != 0 {
		t.Errorf("writeCalls = %v, want ZERO for a non-ON/OFF payload", f.writeCalls)
	}
	if c.n != 0 {
		t.Errorf("refresh fired %d times, want 0 on reject", c.n)
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
