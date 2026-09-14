package publisher_test

import (
	"context"
	"testing"

	"github.com/gsdevme/solis-inverter-manager/internal/publisher"
)

// readCall records the arguments of one ReadInput invocation.
type readCall struct {
	addr  int
	count int
}

// fakeReader returns a zero-filled slice of the requested length for each read
// and records the calls. Setting regs[absAddr] seeds a specific register.
type fakeReader struct {
	calls []readCall
	regs  map[int]uint16
}

func (f *fakeReader) ReadInput(_ context.Context, addr, count int) ([]uint16, error) {
	f.calls = append(f.calls, readCall{addr: addr, count: count})
	out := make([]uint16, count)
	for i := range out {
		if v, ok := f.regs[addr+i]; ok {
			out[i] = v
		}
	}
	return out, nil
}

func TestCollectTwoBlocks(t *testing.T) {
	const (
		regBatterySOC = 33139 // in block 2
		wantSOC       = 42
	)
	f := &fakeReader{regs: map[int]uint16{regBatterySOC: wantSOC}}

	tel, err := publisher.Collect(context.Background(), f)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	wantCalls := []readCall{{33022, 100}, {33122, 54}}
	if len(f.calls) != len(wantCalls) {
		t.Fatalf("ReadInput called %d times, want %d: %v", len(f.calls), len(wantCalls), f.calls)
	}
	for i, want := range wantCalls {
		if f.calls[i] != want {
			t.Errorf("call %d = %+v, want %+v", i, f.calls[i], want)
		}
	}

	if tel.Battery.SOCPercent != wantSOC {
		t.Errorf("decoded battery SOC = %v, want %d", tel.Battery.SOCPercent, wantSOC)
	}
}
