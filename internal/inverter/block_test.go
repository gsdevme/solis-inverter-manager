package inverter

import (
	"errors"
	"testing"
)

const floatTolerance = 1e-9

// assertFloat fails unless got is within floatTolerance of want.
func assertFloat(t *testing.T, name string, got, want float64) {
	t.Helper()
	if diff := got - want; diff > floatTolerance || diff < -floatTolerance {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestPrimitivesNonZeroBase(t *testing.T) {
	// A block that starts at an arbitrary base must resolve absolute addresses,
	// never assume register 0.
	regs := []uint16{0xFFFF, 0xFF9C, 0x0000, 0x0064} // at base 100..103
	const base = 100

	t.Run("u16", func(t *testing.T) {
		v, ok := u16(regs, base, 103)
		if !ok || v != 100 {
			t.Fatalf("u16(103) = %d, %v; want 100, true", v, ok)
		}
	})

	t.Run("out of range low", func(t *testing.T) {
		if _, ok := u16(regs, base, 99); ok {
			t.Fatal("u16(99) ok=true, want false")
		}
	})

	t.Run("out of range high", func(t *testing.T) {
		if _, ok := u16(regs, base, 104); ok {
			t.Fatal("u16(104) ok=true, want false")
		}
	})
}

func TestS16TwosComplement(t *testing.T) {
	tests := []struct {
		name string
		raw  uint16
		want int16
	}{
		{"zero", 0, 0},
		{"positive", 155, 155},
		{"negative one", 0xFFFF, -1},
		{"min", 0x8000, -32768},
		{"max", 0x7FFF, 32767},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, ok := s16([]uint16{tc.raw}, 0, 0)
			if !ok || v != tc.want {
				t.Fatalf("s16(%#04x) = %d, %v; want %d, true", tc.raw, v, ok, tc.want)
			}
		})
	}
}

func TestU32MSWFirst(t *testing.T) {
	// MSW at the lower address: reg[addr]<<16 | reg[addr+1].
	regs := []uint16{0x0001, 0x0002}
	v, ok := u32(regs, 0, 0)
	if !ok || v != 0x00010002 {
		t.Fatalf("u32 = %#08x, %v; want 0x00010002, true", v, ok)
	}

	t.Run("second word missing", func(t *testing.T) {
		if _, ok := u32([]uint16{0x0001}, 0, 0); ok {
			t.Fatal("u32 with missing LSW ok=true, want false")
		}
	})
}

func TestS32TwosComplement(t *testing.T) {
	tests := []struct {
		name string
		hi   uint16
		lo   uint16
		want int32
	}{
		{"zero", 0, 0, 0},
		{"positive", 0, 802, 802},
		{"grid import -132", 65535, 65404, -132},
		{"negative one", 0xFFFF, 0xFFFF, -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, ok := s32([]uint16{tc.hi, tc.lo}, 0, 0)
			if !ok || v != tc.want {
				t.Fatalf("s32(%d,%d) = %d, %v; want %d, true", tc.hi, tc.lo, v, ok, tc.want)
			}
		})
	}
}

func TestSnapshotOutOfRange(t *testing.T) {
	s := Snapshot{{Base: 33121, Regs: []uint16{1, 2, 3}}}
	if _, err := s.U16(40000); !errors.Is(err, ErrRegisterOutOfRange) {
		t.Fatalf("U16(40000) err = %v, want ErrRegisterOutOfRange", err)
	}
}
