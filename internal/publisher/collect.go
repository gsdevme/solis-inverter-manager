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

// Input register bank addressing. The sidecar caps a single read at 125 registers
// (see docs/specs/01-sidecar-contract.md), so the telemetry bank (33022–33175) is
// read as two blocks. Snapshot resolves every register by absolute address across
// the blocks, so the two reads need no merging or reordering.
const (
	block1Base  = 33022
	block1Count = 125 // covers 33022–33146
	block2Base  = 33147
	block2Count = 29 // covers 33147–33175
)

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
