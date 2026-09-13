package inverter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// writeProbe mirrors docs/phase0/fixtures/write-probe-stage-a.json: the target
// registers with their baseline/probe values and the ordered read/write steps.
type writeProbe struct {
	Targets map[string]struct {
		Baseline uint16 `json:"baseline"`
		Probe    uint16 `json:"probe"`
	} `json:"targets"`
	Steps []struct {
		Op         string `json:"op"`
		Addr       int    `json:"addr"`
		Value      uint16 `json:"value"`
		Checkpoint *int   `json:"checkpoint_s"`
		Restore    bool   `json:"restore_check"`
	} `json:"steps"`
}

// regSOCCandidate is the unconfirmed force-charge/backup SOC register probed in
// Stage A; it has no named constant because its meaning is not confirmed.
const regSOCCandidate = 43024

// TestStageAWriteProbe pins the Stage A (#27) write->read-back->restore results:
// timed H/M registers in slot 1 and slot 3 accept fc06 and hold the probe value
// at every checkpoint, 43024 acks the write but keeps its baseline, and every
// register reads back its baseline after restore.
func TestStageAWriteProbe(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "phase0", "fixtures", "write-probe-stage-a.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var p writeProbe
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	slot3ChargeStartMinute := RegTimedSlot3Base + (RegTimedChargeStartMinute - RegTimedChargeCurrent)
	held := map[int]bool{
		RegTimedChargeStartMinute: true,
		slot3ChargeStartMinute:    true,
		regSOCCandidate:           false,
	}
	for addr, wantHeld := range held {
		tg, ok := p.Targets[strconv.Itoa(addr)]
		if !ok {
			t.Fatalf("%d missing from fixture targets", addr)
		}
		want := tg.Baseline
		if wantHeld {
			want = tg.Probe
		}
		checkpoints, restored := 0, false
		for _, s := range p.Steps {
			if s.Op != "read" || s.Addr != addr {
				continue
			}
			switch {
			case s.Checkpoint != nil:
				checkpoints++
				if s.Value != want {
					t.Errorf("%d at t+%ds = %d, want %d (held=%v)", addr, *s.Checkpoint, s.Value, want, wantHeld)
				}
			case s.Restore:
				restored = true
				if s.Value != tg.Baseline {
					t.Errorf("%d after restore = %d, want baseline %d", addr, s.Value, tg.Baseline)
				}
			}
		}
		if checkpoints != 3 || !restored {
			t.Errorf("%d: %d checkpoint reads (want 3), restored=%v", addr, checkpoints, restored)
		}
	}
}
