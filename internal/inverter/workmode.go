package inverter

// Work-mode bitfield bits (register 43110, mirrored at input 33132), confirmed in
// docs/phase0/findings.md.
const (
	bitSelfUse         uint16 = 1 << 0 // 1
	bitTimed           uint16 = 1 << 1 // 2
	bitAllowGridCharge uint16 = 1 << 5 // 32
)

// Named work-mode values used by the controls layer.
const (
	// WorkModeTimedOn is self-use + grid-charge + timed ("optimal income ON").
	WorkModeTimedOn uint16 = bitSelfUse | bitTimed | bitAllowGridCharge // 35
	// WorkModeTimedOff is self-use + grid-charge with timed off ("optimal income OFF").
	WorkModeTimedOff uint16 = bitSelfUse | bitAllowGridCharge // 33
)

// WorkMode is the decoded energy-storage work-mode bitfield. Raw preserves the
// original register — including bits outside the three known flags — so encoding
// after a single-flag change never disturbs bits this map does not model.
type WorkMode struct {
	SelfUse         bool // bit 0
	Timed           bool // bit 1 ("optimal income")
	AllowGridCharge bool // bit 5 (1 = allow)
	Raw             uint16
}

// DecodeWorkMode decodes the 43110/33132 bitfield.
func DecodeWorkMode(raw uint16) WorkMode {
	return WorkMode{
		SelfUse:         raw&bitSelfUse != 0,
		Timed:           raw&bitTimed != 0,
		AllowGridCharge: raw&bitAllowGridCharge != 0,
		Raw:             raw,
	}
}

// Encode renders the work mode back to a register word, overlaying the three
// known flags onto the preserved Raw bits. For a value obtained from
// DecodeWorkMode this returns Raw unchanged (a lossless round trip).
func (w WorkMode) Encode() uint16 {
	raw := w.Raw
	raw = setBit(raw, bitSelfUse, w.SelfUse)
	raw = setBit(raw, bitTimed, w.Timed)
	raw = setBit(raw, bitAllowGridCharge, w.AllowGridCharge)
	return raw
}

// WithTimed returns a copy with only bit 1 changed, preserving every other bit
// (read-modify-write). This is how the "optimal income" switch flips 35 ↔ 33
// without assuming the whole field.
func (w WorkMode) WithTimed(on bool) WorkMode {
	return DecodeWorkMode(setBit(w.Raw, bitTimed, on))
}

// WithAllowGridCharge returns a copy with only bit 5 changed.
func (w WorkMode) WithAllowGridCharge(on bool) WorkMode {
	return DecodeWorkMode(setBit(w.Raw, bitAllowGridCharge, on))
}

// WithSelfUse returns a copy with only bit 0 changed.
func (w WorkMode) WithSelfUse(on bool) WorkMode {
	return DecodeWorkMode(setBit(w.Raw, bitSelfUse, on))
}

// setBit sets mask on raw when on, clears it otherwise.
func setBit(raw, mask uint16, on bool) uint16 {
	if on {
		return raw | mask
	}
	return raw &^ mask
}
