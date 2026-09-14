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
// single-register read of that address to a fixed value (to exercise a confirm
// mismatch on one register of a sequence). readErr / writeErr inject
// failures across every address; failReadOnce and failRead inject a read failure
// at one address, the first consumed by the first read of that address (the
// one-off sidecar timeout the live defect hit) and the second standing for every
// read of it.
type fakeRW struct {
	regs           map[int]uint16
	holdCalls      []holdCall
	writeCalls     []writeCall
	inputCalls     []inputCall
	readErr        error
	writeErr       error
	failReadOnce   map[int]error
	failRead       map[int]error
	overrideReread map[int]uint16
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
	if err, ok := f.failReadOnce[addr]; ok {
		delete(f.failReadOnce, addr)
		return nil, err
	}
	if err, ok := f.failRead[addr]; ok {
		return nil, err
	}
	if f.shortHolding {
		return []uint16{}, nil
	}
	if v, ok := f.overrideReread[addr]; ok && count == 1 {
		return []uint16{v}, nil
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

// readsOf counts the ReadHolding calls that targeted addr exactly.
func (f *fakeRW) readsOf(addr int) int {
	n := 0
	for _, c := range f.holdCalls {
		if c.addr == addr {
			n++
		}
	}
	return n
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
