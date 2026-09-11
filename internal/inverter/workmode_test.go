package inverter

import "testing"

func TestDecodeWorkMode35(t *testing.T) {
	w := DecodeWorkMode(35)
	if !w.SelfUse || !w.Timed || !w.AllowGridCharge {
		t.Fatalf("DecodeWorkMode(35) = %+v; want all three flags set", w)
	}
	if w.Raw != 35 {
		t.Errorf("Raw = %d, want 35", w.Raw)
	}
}

func TestWorkModeToggleTimed(t *testing.T) {
	on := DecodeWorkMode(WorkModeTimedOn) // 35
	off := on.WithTimed(false)
	if off.Timed {
		t.Error("WithTimed(false).Timed = true, want false")
	}
	if off.Encode() != WorkModeTimedOff { // 33
		t.Errorf("timed off encodes to %d, want %d", off.Encode(), WorkModeTimedOff)
	}
	// Only bit 1 changed; self-use and grid-charge preserved.
	if !off.SelfUse || !off.AllowGridCharge {
		t.Errorf("toggling timed disturbed other bits: %+v", off)
	}

	back := off.WithTimed(true)
	if back.Encode() != WorkModeTimedOn {
		t.Errorf("timed on encodes to %d, want %d", back.Encode(), WorkModeTimedOn)
	}
}

func TestWorkModePreservesUnknownBits(t *testing.T) {
	// Bit 7 (128) is outside the modelled flags; toggling timed must not clear it.
	const raw = 35 | 1<<7 // 163
	w := DecodeWorkMode(raw)
	off := w.WithTimed(false)
	if off.Raw != raw&^bitTimed {
		t.Errorf("WithTimed cleared unknown bits: got %d, want %d", off.Raw, raw&^bitTimed)
	}
}

func TestWorkModeEncodeRoundTrip(t *testing.T) {
	for _, raw := range []uint16{0, 33, 35, 32, 1, 163, 0xFFFF} {
		if got := DecodeWorkMode(raw).Encode(); got != raw {
			t.Errorf("Encode(Decode(%d)) = %d, want %d", raw, got, raw)
		}
	}
}
