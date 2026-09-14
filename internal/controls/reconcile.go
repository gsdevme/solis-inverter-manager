package controls

import (
	"context"

	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
	"github.com/gsdevme/solis-inverter-manager/internal/schedule"
)

// Reconcile makes the inverter's three timed slots match the schedule the
// manager wants it to hold (REQ-HA-17): the configured Time-of-Use tariff in
// slots 1 and 2 when the owner asserts it, and the boost slot cleared once its
// window has passed. The caller passes the slots just read by the poll, so the
// reconcile needs no read of its own, and it reports whether anything was
// written plus the first error seen — a failure is non-fatal and the next poll
// retries.
//
// Only the registers that differ are handed to the guard. That pre-filter is
// what makes the steady state cost zero Modbus frames: the guard would skip an
// equal register anyway, but only after spending a read on it. Each register the
// reconcile does write still goes through the full read-compare-write-confirm
// guard (REQ-HA-10).
func (h *Handler) Reconcile(ctx context.Context, slots inverter.TimedSlots) (bool, error) {
	if !h.enabled {
		return false, nil
	}
	desired := schedule.Desired(h.tou, h.assertToU, slots, h.now())
	diff := scheduleDiff(desired, slots)
	if len(diff) == 0 {
		return false, nil
	}
	return h.writeRegisters(ctx, keyReconcile, diff)
}

// scheduleDiff lists the registers whose desired value differs from the value
// the inverter currently holds, in slot order and, within a slot, in the write
// order inverter.TimedSlotWriteRegisters chose for the desired slot. Current
// values are matched by address, not by position: the two expansions may order
// their blocks differently when the slot is swapping direction.
func scheduleDiff(desired, current inverter.TimedSlots) []inverter.Register {
	var diff []inverter.Register
	for i := range desired {
		have := make(map[int]uint16, 8)
		for _, reg := range inverter.TimedSlotWriteRegisters(i, current[i]) {
			have[reg.Addr] = reg.Value
		}
		for _, reg := range inverter.TimedSlotWriteRegisters(i, desired[i]) {
			if reg.Value != have[reg.Addr] {
				diff = append(diff, reg)
			}
		}
	}
	return diff
}
