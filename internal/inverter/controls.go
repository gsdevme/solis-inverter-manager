package inverter

import "math"

// haChargeAmpsMin and haChargeAmpsMax bound the Home Assistant charge/discharge
// current control at 0–62.5 A, the RHI-3.6K-48ES-5G datasheet battery
// charge/discharge rating. The registers themselves accept up to 100.0 A
// (43117/43118 = 1000) because that is the rating of the larger 4.6K/5K/6K
// models in the same family, so the manager clamps to this model's figure.
const (
	haChargeAmpsMin = 0.0
	haChargeAmpsMax = 62.5
)

// DecodeAmps decodes a ÷10 A current register (e.g. 43141/43142) to amps.
func DecodeAmps(raw uint16) float64 {
	return div10(float64(raw))
}

// EncodeAmps encodes amps to a ÷10 A register word, rounding to the nearest
// tenth. It does not clamp — clamping to the HA control range is the caller's
// concern (see ClampHAChargeAmps).
func EncodeAmps(amps float64) uint16 {
	return uint16(math.Round(amps * 10))
}

// ClampHAChargeAmps clamps a requested current to the Home Assistant control
// range (0–62.5 A, this model's datasheet rating). It is a policy helper for the
// controls/HA layer and is kept out of the EncodeAmps primitive so the encoder
// stays a pure unit conversion.
func ClampHAChargeAmps(amps float64) float64 {
	return min(max(amps, haChargeAmpsMin), haChargeAmpsMax)
}
