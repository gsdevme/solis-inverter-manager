package publisher

import (
	"context"
	"fmt"

	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
)

// RegisterReader reads a run of input registers starting at an absolute Modbus
// address. *sidecarclient.Client satisfies this; Collect depends only on the
// interface so the read path is testable without a sidecar.
type RegisterReader interface {
	ReadInput(ctx context.Context, addr, count int) ([]uint16, error)
}

// Input register bank addressing. The live Solarman datalogger NAKs any single
// read wider than ~100 registers with illegal_address (probed: 33022+100 OK,
// 33022+110 NAK) — a stricter bound than the sidecar's 125-register wire cap
// (docs/specs/01-sidecar-contract.md). So the telemetry bank (33022–33214) is read
// as two blocks, both ≤ maxReadRegisters, split at 33121|33122 so no multi-word
// value (the 6-word RTC block and the U32/S32 pairs) straddles the boundary. The
// second block runs to 33214 to take in the SOC-threshold mirrors, still inside
// the single-read cap and still one round trip.
// Snapshot resolves every register by absolute address across the blocks, so the
// two reads need no merging or reordering.
const (
	maxReadRegisters = 100 // live datalogger single-read cap (see comment above)

	block1Base  = 33022
	block1Count = maxReadRegisters // 100, covers 33022–33121
	block2Base  = 33122
	block2Count = 93 // covers 33122–33214
)

// Compile-time guard: widening block 2 past the datalogger's single-read cap
// underflows this unsigned conversion and fails the build.
const _ = uint(maxReadRegisters - block2Count)

// Collect reads the two input register blocks and decodes them into Telemetry.
// It is the reusable read→decode unit the Phase 6 scheduler calls unchanged.
func Collect(ctx context.Context, r RegisterReader) (inverter.Telemetry, error) {
	regs1, err := r.ReadInput(ctx, block1Base, block1Count)
	if err != nil {
		return inverter.Telemetry{}, fmt.Errorf("read input block %d+%d: %w", block1Base, block1Count, err)
	}
	regs2, err := r.ReadInput(ctx, block2Base, block2Count)
	if err != nil {
		return inverter.Telemetry{}, fmt.Errorf("read input block %d+%d: %w", block2Base, block2Count, err)
	}
	snapshot := inverter.Snapshot{
		{Base: block1Base, Regs: regs1},
		{Base: block2Base, Regs: regs2},
	}
	return inverter.DecodeTelemetry(snapshot)
}
