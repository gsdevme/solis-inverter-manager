package inverter

import (
	"errors"
	"fmt"
)

// ErrRegisterOutOfRange reports that a requested absolute register address falls
// outside the block(s) available to a decode. Callers can branch on it with
// errors.Is.
var ErrRegisterOutOfRange = errors.New("register address out of range")

// Block is a raw register block as returned by the sidecar: Regs[0] holds the
// register at absolute address Base, Regs[1] at Base+1, and so on. A Block need
// not start at register 0 — every read is resolved through Base — so a decoder
// consuming a Block never assumes a fixed origin.
type Block struct {
	// Base is the absolute Modbus address of Regs[0].
	Base int
	// Regs are the consecutive register words read from Base.
	Regs []uint16
}

// index maps an absolute address to a slice index, reporting ok=false when the
// address is outside the block.
func (b Block) index(addr int) (int, bool) {
	i := addr - b.Base
	if i < 0 || i >= len(b.Regs) {
		return 0, false
	}
	return i, true
}

// contains reports whether addr lies within the block.
func (b Block) contains(addr int) bool {
	_, ok := b.index(addr)
	return ok
}

// regAt returns the register at absolute address addr within regs, whose first
// element is at address base. ok is false when addr falls outside the slice. This
// is the single bounds-checked locator every primitive builds on.
func regAt(regs []uint16, base, addr int) (uint16, bool) {
	i := addr - base
	if i < 0 || i >= len(regs) {
		return 0, false
	}
	return regs[i], true
}

// u16 reads an unsigned 16-bit register.
func u16(regs []uint16, base, addr int) (uint16, bool) {
	return regAt(regs, base, addr)
}

// s16 reads a signed 16-bit register (two's complement).
func s16(regs []uint16, base, addr int) (int16, bool) {
	v, ok := regAt(regs, base, addr)
	return int16(v), ok
}

// u32 reads an unsigned 32-bit value from two consecutive registers, MSW at the
// lower address (big-endian words): raw = reg[addr]<<16 | reg[addr+1].
func u32(regs []uint16, base, addr int) (uint32, bool) {
	hi, ok := regAt(regs, base, addr)
	if !ok {
		return 0, false
	}
	lo, ok := regAt(regs, base, addr+1)
	if !ok {
		return 0, false
	}
	return uint32(hi)<<16 | uint32(lo), true
}

// s32 reads a signed 32-bit value (two's complement) from two consecutive
// registers, MSW at the lower address.
func s32(regs []uint16, base, addr int) (int32, bool) {
	v, ok := u32(regs, base, addr)
	return int32(v), ok
}

// U16 returns the unsigned 16-bit register at addr, or ErrRegisterOutOfRange.
func (b Block) U16(addr int) (uint16, error) {
	v, ok := u16(b.Regs, b.Base, addr)
	if !ok {
		return 0, fmt.Errorf("u16 %d: %w", addr, ErrRegisterOutOfRange)
	}
	return v, nil
}

// S16 returns the signed 16-bit register at addr, or ErrRegisterOutOfRange.
func (b Block) S16(addr int) (int16, error) {
	v, ok := s16(b.Regs, b.Base, addr)
	if !ok {
		return 0, fmt.Errorf("s16 %d: %w", addr, ErrRegisterOutOfRange)
	}
	return v, nil
}

// U32 returns the unsigned 32-bit value at addr (MSW-first), or
// ErrRegisterOutOfRange if either word is missing.
func (b Block) U32(addr int) (uint32, error) {
	v, ok := u32(b.Regs, b.Base, addr)
	if !ok {
		return 0, fmt.Errorf("u32 %d: %w", addr, ErrRegisterOutOfRange)
	}
	return v, nil
}

// S32 returns the signed 32-bit value at addr (MSW-first), or
// ErrRegisterOutOfRange if either word is missing.
func (b Block) S32(addr int) (int32, error) {
	v, ok := s32(b.Regs, b.Base, addr)
	if !ok {
		return 0, fmt.Errorf("s32 %d: %w", addr, ErrRegisterOutOfRange)
	}
	return v, nil
}

// Snapshot is a set of register blocks captured in one poll. A telemetry decode
// spans several non-contiguous blocks, so reads are resolved by absolute address
// across every block.
type Snapshot []Block

// block returns the block containing addr.
func (s Snapshot) block(addr int) (Block, bool) {
	for _, b := range s {
		if b.contains(addr) {
			return b, true
		}
	}
	return Block{}, false
}

// U16 reads the unsigned 16-bit register at addr from whichever block contains it.
func (s Snapshot) U16(addr int) (uint16, error) {
	b, ok := s.block(addr)
	if !ok {
		return 0, fmt.Errorf("u16 %d: %w", addr, ErrRegisterOutOfRange)
	}
	return b.U16(addr)
}

// S16 reads the signed 16-bit register at addr from whichever block contains it.
func (s Snapshot) S16(addr int) (int16, error) {
	b, ok := s.block(addr)
	if !ok {
		return 0, fmt.Errorf("s16 %d: %w", addr, ErrRegisterOutOfRange)
	}
	return b.S16(addr)
}

// U32 reads the unsigned 32-bit value at addr (both words must lie in one block).
func (s Snapshot) U32(addr int) (uint32, error) {
	b, ok := s.block(addr)
	if !ok {
		return 0, fmt.Errorf("u32 %d: %w", addr, ErrRegisterOutOfRange)
	}
	return b.U32(addr)
}

// S32 reads the signed 32-bit value at addr (both words must lie in one block).
func (s Snapshot) S32(addr int) (int32, error) {
	b, ok := s.block(addr)
	if !ok {
		return 0, fmt.Errorf("s32 %d: %w", addr, ErrRegisterOutOfRange)
	}
	return b.S32(addr)
}
