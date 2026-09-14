package controls_test

import (
	"context"
	"testing"

	"github.com/gsdevme/solis-inverter-manager/internal/controls"
	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
)

// touSlots is the schedule the asserted 23:30-05:30 tariff expands to: slot 1
// charges from 23:30 to end-of-day, slot 2 resumes at midnight until 05:30, and
// the boost slot is clear.
func touSlots() inverter.TimedSlots {
	return inverter.TimedSlots{
		{Charge: inverter.TimedWindow{Start: clock(23, 30)}},
		{Charge: inverter.TimedWindow{End: clock(5, 30)}},
		{},
	}
}

// seedSlots writes a schedule into the fake's registers so a reconcile reads
// back exactly the slots it is handed.
func seedSlots(f *fakeRW, slots inverter.TimedSlots) {
	for i, slot := range slots {
		for _, reg := range inverter.TimedSlotWriteRegisters(i, slot) {
			f.regs[reg.Addr] = reg.Value
		}
	}
}

// TestReconcileDisabledDoesNothing: the CONTROLS_ENABLED kill-switch silences the
// reconcile exactly as it silences commands.
func TestReconcileDisabledDoesNothing(t *testing.T) {
	f := newFakeRW()
	h := controls.NewHandler(f, quietLogger(), false, nil, at(14, 7), controls.WithToU(touWindow, true))

	wrote, err := h.Reconcile(context.Background(), inverter.TimedSlots{})

	if wrote || err != nil {
		t.Errorf("Reconcile = (%v, %v), want (false, nil) when disabled", wrote, err)
	}
	if len(f.holdCalls) != 0 || len(f.writeCalls) != 0 {
		t.Errorf("holdCalls = %v, writeCalls = %v, want ZERO of each when disabled", f.holdCalls, f.writeCalls)
	}
}

// TestReconcileInSteadyStateIssuesNoCalls: when the inverter already holds the
// desired schedule the reconcile costs no Modbus frames at all — not even the
// guard's compare reads. This is the per-poll steady state.
func TestReconcileInSteadyStateIssuesNoCalls(t *testing.T) {
	slots := touSlots()
	f := newFakeRW()
	seedSlots(f, slots)
	h := controls.NewHandler(f, quietLogger(), true, nil, at(14, 7), controls.WithToU(touWindow, true))

	wrote, err := h.Reconcile(context.Background(), slots)

	if wrote || err != nil {
		t.Errorf("Reconcile = (%v, %v), want (false, nil) in steady state", wrote, err)
	}
	if len(f.holdCalls) != 0 || len(f.writeCalls) != 0 {
		t.Errorf("holdCalls = %v, writeCalls = %v, want ZERO of each in steady state", f.holdCalls, f.writeCalls)
	}
}

// TestReconcileRewritesDriftedToUMinute: an app-side edit to one register of the
// tariff is reverted with exactly one guarded write, leaving every matching
// register untouched.
func TestReconcileRewritesDriftedToUMinute(t *testing.T) {
	slots := touSlots()
	slots[1].Charge.End = clock(5, 31)
	f := newFakeRW()
	seedSlots(f, slots)
	h := controls.NewHandler(f, quietLogger(), true, nil, at(14, 7), controls.WithToU(touWindow, true))

	wrote, err := h.Reconcile(context.Background(), slots)

	if !wrote || err != nil {
		t.Errorf("Reconcile = (%v, %v), want (true, nil) after drift", wrote, err)
	}
	wantWrites(t, f, []writeCall{{43156, 30}})
}

// TestReconcileClearsExpiredBoost: a boost whose window has passed is zeroed in
// the spec's register order so it cannot fire again tomorrow.
func TestReconcileClearsExpiredBoost(t *testing.T) {
	slots := touSlots()
	slots[2] = inverter.TimedSlot{Charge: inverter.TimedWindow{Start: clock(13, 5), End: clock(13, 45)}}
	f := newFakeRW()
	seedSlots(f, slots)
	h := controls.NewHandler(f, quietLogger(), true, nil, at(14, 7), controls.WithToU(touWindow, true))

	wrote, err := h.Reconcile(context.Background(), slots)

	if !wrote || err != nil {
		t.Errorf("Reconcile = (%v, %v), want (true, nil) after expiry", wrote, err)
	}
	wantWrites(t, f, []writeCall{{43163, 0}, {43164, 0}, {43165, 0}, {43166, 0}})
}

// TestReconcileLeavesRunningBoostAlone: a boost still inside its window is
// adopted, not reverted, so a restart mid-boost keeps it running.
func TestReconcileLeavesRunningBoostAlone(t *testing.T) {
	slots := touSlots()
	slots[2] = inverter.TimedSlot{Charge: inverter.TimedWindow{Start: clock(14, 0), End: clock(14, 30)}}
	f := newFakeRW()
	seedSlots(f, slots)
	h := controls.NewHandler(f, quietLogger(), true, nil, at(14, 7), controls.WithToU(touWindow, true))

	wrote, err := h.Reconcile(context.Background(), slots)

	if wrote || err != nil {
		t.Errorf("Reconcile = (%v, %v), want (false, nil) while the boost runs", wrote, err)
	}
	if len(f.holdCalls) != 0 || len(f.writeCalls) != 0 {
		t.Errorf("holdCalls = %v, writeCalls = %v, want ZERO of each", f.holdCalls, f.writeCalls)
	}
}

// TestReconcileWithoutToUAssertionLeavesSlotsOneAndTwo: with TOU_WINDOW unset the
// manager does not own the tariff, so whatever the inverter holds in slots 1 and
// 2 is left alone.
func TestReconcileWithoutToUAssertionLeavesSlotsOneAndTwo(t *testing.T) {
	slots := inverter.TimedSlots{
		{Charge: inverter.TimedWindow{Start: clock(1, 0), End: clock(2, 0)}},
		{Discharge: inverter.TimedWindow{Start: clock(9, 0), End: clock(10, 0)}},
		{},
	}
	f := newFakeRW()
	seedSlots(f, slots)
	h := controls.NewHandler(f, quietLogger(), true, nil, at(14, 7), controls.WithToU(inverter.TimedWindow{}, false))

	wrote, err := h.Reconcile(context.Background(), slots)

	if wrote || err != nil {
		t.Errorf("Reconcile = (%v, %v), want (false, nil) with assertion off", wrote, err)
	}
	if len(f.holdCalls) != 0 || len(f.writeCalls) != 0 {
		t.Errorf("holdCalls = %v, writeCalls = %v, want ZERO of each with assertion off", f.holdCalls, f.writeCalls)
	}
}

// staleCharge is the half-cleared charge window the live defect left behind: the
// end minute of the old boost survived a failed clear, so the window reads
// 00:00-00:56. runningDischarge is the boost programmed over it in the same
// command, still inside its window.
var (
	staleCharge      = inverter.TimedWindow{End: clock(0, 56)}
	runningDischarge = inverter.TimedWindow{Start: clock(8, 58), End: clock(9, 0)}
)

// partiallyClearedSlots is the schedule the inverter held after that command:
// the tariff, plus slot 3 carrying both the stale charge remnant and the live
// discharge boost.
func partiallyClearedSlots() inverter.TimedSlots {
	slots := touSlots()
	slots[2] = inverter.TimedSlot{Charge: staleCharge, Discharge: runningDischarge}
	return slots
}

// TestReconcileHealsAPartiallyClearedSlotWithoutWipingTheBoost: slot 3 holding an
// ended charge remnant alongside a running discharge boost is expired per
// direction, so the reconcile clears only the remnant's surviving register and
// leaves the boost running.
func TestReconcileHealsAPartiallyClearedSlotWithoutWipingTheBoost(t *testing.T) {
	slots := partiallyClearedSlots()
	f := newFakeRW()
	seedSlots(f, slots)
	h := controls.NewHandler(f, quietLogger(), true, nil, at(8, 59), controls.WithToU(touWindow, true))

	wrote, err := h.Reconcile(context.Background(), slots)

	if !wrote || err != nil {
		t.Errorf("Reconcile = (%v, %v), want (true, nil) while healing the slot", wrote, err)
	}
	wantWrites(t, f, []writeCall{{43166, 0}})
}

// TestReconcileClearsBothDirectionsOnceBothHaveEnded: with neither window still
// running the whole slot is cleared, charge block first.
func TestReconcileClearsBothDirectionsOnceBothHaveEnded(t *testing.T) {
	slots := partiallyClearedSlots()
	f := newFakeRW()
	seedSlots(f, slots)
	h := controls.NewHandler(f, quietLogger(), true, nil, at(9, 30), controls.WithToU(touWindow, true))

	wrote, err := h.Reconcile(context.Background(), slots)

	if !wrote || err != nil {
		t.Errorf("Reconcile = (%v, %v), want (true, nil) after both windows ended", wrote, err)
	}
	wantWrites(t, f, []writeCall{{43166, 0}, {43167, 0}, {43168, 0}, {43169, 0}})
}

// expiredBoostSlots is the tariff plus a slot-3 charge window that ended before
// the tests' 14:07 clock.
func expiredBoostSlots() inverter.TimedSlots {
	slots := touSlots()
	slots[2] = inverter.TimedSlot{Charge: inverter.TimedWindow{Start: clock(13, 5), End: clock(13, 45)}}
	return slots
}

// TestReconcileRetriesARegisterLostToATransportFailure: a register whose guard
// never reached the inverter is retried once after the ordered pass, so a one-off
// timeout does not leave the schedule half written — and the retry's success
// clears the error the first attempt reported.
func TestReconcileRetriesARegisterLostToATransportFailure(t *testing.T) {
	slots := expiredBoostSlots()
	f := newFakeRW()
	seedSlots(f, slots)
	f.failReadOnce = map[int]error{43164: errBoom}
	h := controls.NewHandler(f, quietLogger(), true, nil, at(14, 7), controls.WithToU(touWindow, true))

	wrote, err := h.Reconcile(context.Background(), slots)

	if !wrote || err != nil {
		t.Errorf("Reconcile = (%v, %v), want (true, nil) once the retry succeeds", wrote, err)
	}
	wantWrites(t, f, []writeCall{{43163, 0}, {43165, 0}, {43166, 0}, {43164, 0}})
}

// TestReconcileReportsARegisterThatFailsTwice: the retry is a single extra
// attempt, so a register failing persistently still surfaces its error while
// every other register of the sequence is written.
func TestReconcileReportsARegisterThatFailsTwice(t *testing.T) {
	slots := expiredBoostSlots()
	f := newFakeRW()
	seedSlots(f, slots)
	f.failRead = map[int]error{43164: errBoom}
	h := controls.NewHandler(f, quietLogger(), true, nil, at(14, 7), controls.WithToU(touWindow, true))

	wrote, err := h.Reconcile(context.Background(), slots)

	if !wrote || err == nil {
		t.Errorf("Reconcile = (%v, %v), want (true, an error) when a register fails twice", wrote, err)
	}
	wantWrites(t, f, []writeCall{{43163, 0}, {43165, 0}, {43166, 0}})
	if got := f.readsOf(43164); got != 2 {
		t.Errorf("reads of 43164 = %d, want 2 (the attempt and one retry)", got)
	}
}
