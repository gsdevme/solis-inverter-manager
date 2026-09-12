package controls_test

import (
	"context"
	"fmt"
)

// holdCall records one ReadHolding invocation.
type holdCall struct {
	addr  int
	count int
}

// writeCall records one WriteHolding invocation.
type writeCall struct {
	addr  int
	value uint16
}

// inputCall records one ReadInput invocation.
type inputCall struct {
	addr  int
	count int
}

// fakeRW is a programmable holding-register read/write surface that records every
// call. It satisfies controls.HoldingReadWriter and the Locking inner surface
// (ReadInput+ReadHolding+WriteHolding).
//
// regs maps absolute register address to its current value; reads return
// zero-filled slices for unseeded addresses. WriteHolding mutates regs so a
// subsequent re-read observes the written value, unless overrideReread pins the
// re-read result (to exercise a confirm mismatch). readErr / writeErr inject
// failures.
type fakeRW struct {
	regs           map[int]uint16
	holdCalls      []holdCall
	writeCalls     []writeCall
	inputCalls     []inputCall
	readErr        error
	writeErr       error
	overrideReread *uint16
	shortHolding   bool
}

func newFakeRW() *fakeRW {
	return &fakeRW{regs: map[int]uint16{}}
}

func (f *fakeRW) ReadHolding(_ context.Context, addr, count int) ([]uint16, error) {
	f.holdCalls = append(f.holdCalls, holdCall{addr: addr, count: count})
	if f.readErr != nil {
		return nil, f.readErr
	}
	if f.shortHolding {
		return []uint16{}, nil
	}
	if f.overrideReread != nil && count == 1 {
		return []uint16{*f.overrideReread}, nil
	}
	out := make([]uint16, count)
	for i := range out {
		out[i] = f.regs[addr+i]
	}
	return out, nil
}

func (f *fakeRW) WriteHolding(_ context.Context, addr int, value uint16) error {
	f.writeCalls = append(f.writeCalls, writeCall{addr: addr, value: value})
	if f.writeErr != nil {
		return f.writeErr
	}
	f.regs[addr] = value
	return nil
}

func (f *fakeRW) ReadInput(_ context.Context, addr, count int) ([]uint16, error) {
	f.inputCalls = append(f.inputCalls, inputCall{addr: addr, count: count})
	if f.readErr != nil {
		return nil, f.readErr
	}
	out := make([]uint16, count)
	for i := range out {
		out[i] = f.regs[addr+i]
	}
	return out, nil
}

// wroteTo reports whether any WriteHolding hit addr.
func (f *fakeRW) wroteTo(addr int) bool {
	for _, c := range f.writeCalls {
		if c.addr == addr {
			return true
		}
	}
	return false
}

var errBoom = fmt.Errorf("boom")
