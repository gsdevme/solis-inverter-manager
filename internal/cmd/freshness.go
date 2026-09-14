package cmd

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
	"github.com/gsdevme/solis-inverter-manager/internal/scheduler"
)

// setpointFreshness records whether the most recent state read got its writable-
// control setpoints from the inverter or fell back to the scheduler's last-known
// cache. Its zero value means fresh, so the very first poll is not treated as
// stale. It is safe for concurrent use: the poll goroutine and the command-
// refresh path both go through it.
type setpointFreshness struct {
	stale atomic.Bool
}

// markFresh records that this read decoded setpoints straight from the inverter.
func (f *setpointFreshness) markFresh() { f.stale.Store(false) }

// markStale records that this read reused the last-known setpoints because the
// setpoints sub-read failed.
func (f *setpointFreshness) markStale() { f.stale.Store(true) }

// gate wraps a reconciler so it acts only on slots the current cycle actually
// read. readState reuses the cached setpoints when the setpoints sub-read fails,
// so the slots a poll hands the reconcile can be a whole poll interval old;
// diffing against them would let the manager clear a window it never saw — a
// boost the owner set from the Solis app between the two polls, say. A stale
// cycle is skipped in full, exactly like a cycle whose publish failed, and the
// next poll that reads setpoints successfully re-asserts the schedule.
func (f *setpointFreshness) gate(inner scheduler.Reconciler, log *slog.Logger) scheduler.Reconciler {
	return gatedReconciler{inner: inner, fresh: f, log: log}
}

// gatedReconciler is the scheduler.Reconciler returned by setpointFreshness.gate.
type gatedReconciler struct {
	inner scheduler.Reconciler
	fresh *setpointFreshness
	log   *slog.Logger
}

// Reconcile skips the cycle when the setpoints behind slots were reused from
// cache, and otherwise delegates unchanged.
func (r gatedReconciler) Reconcile(ctx context.Context, slots inverter.TimedSlots) (bool, error) {
	if r.fresh.stale.Load() {
		r.log.DebugContext(ctx, "skipping schedule reconcile: setpoints were reused from cache this poll")
		return false, nil
	}
	return r.inner.Reconcile(ctx, slots)
}
