package inverter

// BMS fault/protection bitfields (input registers 33145 and 33146). They are the
// only register evidence that tells a finished charge — where the BMS simply
// drops its charge current limit to 0 A — from a pack protection event.
//
// Unlike the rest of this package's register map, the bit assignments below come
// from the vendor's hybrid-inverter protocol document rather than from this unit:
// both words read 0 in every capture taken so far, so only the addressing and the
// "no fault" decode are confirmed live. Raw is therefore preserved and published
// alongside the decoded bits, so a real fault can be reconciled against the
// vendor table even if a bit is mislabelled here. See docs/phase0/findings.md
// ambiguity #15.

// BMSFault1 bits (register 33145). Bit 0 is reserved.
const (
	bitBMSOverVoltage          uint16 = 1 << 1
	bitBMSUnderVoltage         uint16 = 1 << 2
	bitBMSOverTemp             uint16 = 1 << 3
	bitBMSUnderTemp            uint16 = 1 << 4
	bitBMSChargeOverTemp       uint16 = 1 << 5
	bitBMSChargeUnderTemp      uint16 = 1 << 6
	bitBMSDischargeOverCurrent uint16 = 1 << 7
)

// BMSFault2 bits (register 33146). Bits 1, 2 and 5–7 are reserved.
const (
	bitBMSChargeOverCurrent uint16 = 1 << 0
	bitBMSInternal          uint16 = 1 << 3
	bitBMSModuleUnbalanced  uint16 = 1 << 4
)

// BMSFault1 is the decoded 33145 fault bitfield. Raw preserves the original
// register, including the reserved bits this map does not model.
type BMSFault1 struct {
	OverVoltage          bool // bit 1
	UnderVoltage         bool // bit 2
	OverTemp             bool // bit 3
	UnderTemp            bool // bit 4
	ChargeOverTemp       bool // bit 5
	ChargeUnderTemp      bool // bit 6
	DischargeOverCurrent bool // bit 7
	Raw                  uint16
}

// DecodeBMSFault1 decodes the 33145 bitfield.
func DecodeBMSFault1(raw uint16) BMSFault1 {
	return BMSFault1{
		OverVoltage:          raw&bitBMSOverVoltage != 0,
		UnderVoltage:         raw&bitBMSUnderVoltage != 0,
		OverTemp:             raw&bitBMSOverTemp != 0,
		UnderTemp:            raw&bitBMSUnderTemp != 0,
		ChargeOverTemp:       raw&bitBMSChargeOverTemp != 0,
		ChargeUnderTemp:      raw&bitBMSChargeUnderTemp != 0,
		DischargeOverCurrent: raw&bitBMSDischargeOverCurrent != 0,
		Raw:                  raw,
	}
}

// Any reports whether the word carries any fault at all, reserved bits included,
// so an unmodelled bit still reads as "something is wrong".
func (f BMSFault1) Any() bool { return f.Raw != 0 }

// BMSFault2 is the decoded 33146 fault bitfield. Raw preserves the original
// register, including the reserved bits this map does not model.
type BMSFault2 struct {
	ChargeOverCurrent bool // bit 0
	BMSInternal       bool // bit 3
	ModuleUnbalanced  bool // bit 4
	Raw               uint16
}

// DecodeBMSFault2 decodes the 33146 bitfield.
func DecodeBMSFault2(raw uint16) BMSFault2 {
	return BMSFault2{
		ChargeOverCurrent: raw&bitBMSChargeOverCurrent != 0,
		BMSInternal:       raw&bitBMSInternal != 0,
		ModuleUnbalanced:  raw&bitBMSModuleUnbalanced != 0,
		Raw:               raw,
	}
}

// Any reports whether the word carries any fault at all, reserved bits included.
func (f BMSFault2) Any() bool { return f.Raw != 0 }
