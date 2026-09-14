package cmd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
)

// recordingReconciler records the slots it was asked to reconcile and returns a
// canned result.
type recordingReconciler struct {
	calls []inverter.TimedSlots
	wrote bool
	err   error
}

func (r *recordingReconciler) Reconcile(_ context.Context, slots inverter.TimedSlots) (bool, error) {
	r.calls = append(r.calls, slots)
	return r.wrote, r.err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestFreshnessGateSkipsReconcileAfterFailedSetpointsRead is the guardrail: a
// poll whose setpoints came from the last-known cache must not reconcile, or the
// manager could clear a window it never read this cycle.
func TestFreshnessGateSkipsReconcileAfterFailedSetpointsRead(t *testing.T) {
	var fresh setpointFreshness
	inner := &recordingReconciler{wrote: true}
	gated := fresh.gate(inner, discardLogger())

	fresh.markStale()
	wrote, err := gated.Reconcile(context.Background(), inverter.TimedSlots{})

	if wrote || err != nil {
		t.Errorf("Reconcile = (%v, %v), want (false, nil) on a stale cycle", wrote, err)
	}
	if len(inner.calls) != 0 {
		t.Errorf("inner reconciler ran %d times, want 0 on a stale cycle", len(inner.calls))
	}
}

// TestFreshnessGatePassesThroughFreshCycles: once a read gets its setpoints from
// the inverter again, the reconcile runs and its result is passed straight back.
func TestFreshnessGatePassesThroughFreshCycles(t *testing.T) {
	var fresh setpointFreshness
	boom := errors.New("boom")
	inner := &recordingReconciler{wrote: true, err: boom}
	gated := fresh.gate(inner, discardLogger())
	slots := inverter.TimedSlots{{Charge: inverter.TimedWindow{Start: inverter.Clock{Hour: 14}}}}

	fresh.markStale()
	fresh.markFresh()
	wrote, err := gated.Reconcile(context.Background(), slots)

	if !wrote || !errors.Is(err, boom) {
		t.Errorf("Reconcile = (%v, %v), want (true, boom) on a fresh cycle", wrote, err)
	}
	if len(inner.calls) != 1 || inner.calls[0] != slots {
		t.Errorf("inner reconciler calls = %v, want exactly one with %v", inner.calls, slots)
	}
}

// TestFreshnessGateZeroValueIsFresh: the first poll reconciles, because the flag
// only ever reports staleness a read has actually observed.
func TestFreshnessGateZeroValueIsFresh(t *testing.T) {
	var fresh setpointFreshness
	inner := &recordingReconciler{}
	gated := fresh.gate(inner, discardLogger())

	if _, err := gated.Reconcile(context.Background(), inverter.TimedSlots{}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(inner.calls) != 1 {
		t.Errorf("inner reconciler ran %d times, want 1 for the zero value", len(inner.calls))
	}
}
