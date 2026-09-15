package controls

import (
	"context"
	"fmt"

	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
)

// setpointBlockCount is the number of holding registers read from RegWorkMode
// (43110) through 43170: the work-mode word, the inverter's configured current
// ceiling (43117/43118), both timed-current setpoints (43141/43142), and the
// three timed charge/discharge slots. 61 registers stays within the sidecar's
// 125-register single-read cap.
const setpointBlockCount = 61

// Setpoints is the decoded control state the HA controls mirror: the timed
// charge/discharge currents, the "optimal income" (timed) work-mode flag, and
// the three timed charge/discharge slots.
type Setpoints struct {
	// SetChargeCurrent is register 43141 decoded to amps.
	SetChargeCurrent float64
	// SetDischargeCurrent is register 43142 decoded to amps.
	SetDischargeCurrent float64
	// MaxChargeCurrent and MaxDischargeCurrent are registers 43117/43118: the
	// inverter's own configured ceiling, read-only here. They bound the timed
	// setpoints above and are distinct from the BMS-advertised limits, which are
	// input registers carried on Telemetry.
	MaxChargeCurrent    float64
	MaxDischargeCurrent float64
	// OptimalIncome is bit 1 (timed) of the 43110 work-mode bitfield.
	OptimalIncome bool
	// Slots are the three timed charge/discharge slots decoded from the
	// 43141-43170 slot block.
	Slots inverter.TimedSlots
}

// ReadSetpoints reads the control state in one holding-register block
// (RegWorkMode..the end of timed slot 3) and decodes it. It reuses the
// inverter decoders so all register semantics stay in one place.
func ReadSetpoints(ctx context.Context, rw HoldingReadWriter) (Setpoints, error) {
	regs, err := rw.ReadHolding(ctx, inverter.RegWorkMode, setpointBlockCount)
	if err != nil {
		return Setpoints{}, fmt.Errorf("read setpoints: %w", err)
	}
	if len(regs) < setpointBlockCount {
		return Setpoints{}, fmt.Errorf("read setpoints: short block, got %d registers want %d", len(regs), setpointBlockCount)
	}

	chargeIdx := inverter.RegTimedChargeCurrent - inverter.RegWorkMode
	dischargeIdx := inverter.RegTimedDischargeCurrent - inverter.RegWorkMode
	maxChargeIdx := inverter.RegMaxChargeCurrent - inverter.RegWorkMode
	maxDischargeIdx := inverter.RegMaxDischargeCurrent - inverter.RegWorkMode
	slots, err := inverter.DecodeTimedSlots(inverter.Snapshot{{Base: inverter.RegWorkMode, Regs: regs}})
	if err != nil {
		return Setpoints{}, fmt.Errorf("read setpoints: decode slots: %w", err)
	}
	return Setpoints{
		SetChargeCurrent:    inverter.DecodeAmps(regs[chargeIdx]),
		SetDischargeCurrent: inverter.DecodeAmps(regs[dischargeIdx]),
		MaxChargeCurrent:    inverter.DecodeAmps(regs[maxChargeIdx]),
		MaxDischargeCurrent: inverter.DecodeAmps(regs[maxDischargeIdx]),
		OptimalIncome:       inverter.DecodeWorkMode(regs[0]).Timed,
		Slots:               slots,
	}, nil
}
