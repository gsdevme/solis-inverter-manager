package controls

import (
	"context"
	"fmt"

	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
)

// setpointBlockCount is the number of holding registers read from RegWorkMode
// (43110) to cover the work-mode word and both timed-current setpoints
// (43141/43142). It stays within the sidecar's 125-register single-read cap.
const setpointBlockCount = 33

// Setpoints is the decoded control state the HA controls mirror: the timed
// charge/discharge currents and the "optimal income" (timed) work-mode flag.
type Setpoints struct {
	// SetChargeCurrent is register 43141 decoded to amps.
	SetChargeCurrent float64
	// SetDischargeCurrent is register 43142 decoded to amps.
	SetDischargeCurrent float64
	// OptimalIncome is bit 1 (timed) of the 43110 work-mode bitfield.
	OptimalIncome bool
}

// ReadSetpoints reads the control state in one holding-register block
// (RegWorkMode..RegTimedDischargeCurrent) and decodes it. It reuses the
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
	return Setpoints{
		SetChargeCurrent:    inverter.DecodeAmps(regs[chargeIdx]),
		SetDischargeCurrent: inverter.DecodeAmps(regs[dischargeIdx]),
		OptimalIncome:       inverter.DecodeWorkMode(regs[0]).Timed,
	}, nil
}
