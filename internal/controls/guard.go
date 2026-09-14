package controls

import (
	"context"
	"fmt"
)

// HoldingReadWriter is the holding-register read/write surface the guard needs.
// *sidecarclient.Client satisfies it.
type HoldingReadWriter interface {
	ReadHolding(ctx context.Context, addr, count int) ([]uint16, error)
	WriteHolding(ctx context.Context, addr int, value uint16) error
}

// Result reports what Guard did to one holding register.
type Result struct {
	// Skipped is true when current already equalled desired and no fc06 was issued.
	Skipped bool
	// Wrote is true when a fc06 write was issued.
	Wrote bool
	// Confirmed is true when the post-write re-read equalled desired.
	Confirmed bool
	// Old is the register value read before any write.
	Old uint16
	// New is the register value after the operation (the re-read on a write, or
	// Old on a skip).
	New uint16
}

// Guard enforces read-before-write on a single holding register: it reads the
// current value, issues a fc06 write ONLY when it differs from desired, then
// re-reads to confirm. A no-op (current == desired) is SKIPPED — no fc06 is
// issued — because the holding bank is flash-backed and needless writes wear
// flash (REQ-HA-10). A re-read that does not equal desired is a non-fatal error
// (returned for the caller to log); the write still happened.
func Guard(ctx context.Context, rw HoldingReadWriter, addr int, desired uint16) (Result, error) {
	current, err := readOne(ctx, rw, addr)
	if err != nil {
		return Result{}, fmt.Errorf("guard %d: read current: %w", addr, err)
	}

	if current == desired {
		return Result{Skipped: true, Old: current, New: current}, nil
	}

	if err := rw.WriteHolding(ctx, addr, desired); err != nil {
		return Result{Old: current}, fmt.Errorf("guard %d: write %d: %w", addr, desired, err)
	}

	reread, err := readOne(ctx, rw, addr)
	if err != nil {
		return Result{Wrote: true, Old: current}, fmt.Errorf("guard %d: confirm read: %w", addr, err)
	}

	res := Result{Wrote: true, Old: current, New: reread}
	if reread != desired {
		return res, fmt.Errorf("guard %d: re-read %d does not confirm desired %d", addr, reread, desired)
	}
	res.Confirmed = true
	return res, nil
}

// readOne reads exactly one holding register at addr, treating a short or empty
// slice as an error rather than panicking on the index.
func readOne(ctx context.Context, rw HoldingReadWriter, addr int) (uint16, error) {
	regs, err := rw.ReadHolding(ctx, addr, 1)
	if err != nil {
		return 0, err
	}
	if len(regs) < 1 {
		return 0, fmt.Errorf("holding %d: empty read response", addr)
	}
	return regs[0], nil
}
