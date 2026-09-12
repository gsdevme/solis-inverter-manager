package controls_test

import (
	"context"
	"sync"
	"testing"

	"github.com/gsdevme/solis-inverter-manager/internal/controls"
	"github.com/gsdevme/solis-inverter-manager/internal/sidecarclient"
)

// registerReader mirrors publisher.RegisterReader (the input-read seam the poll
// goroutine consumes). It is duplicated here rather than imported so this suite
// does not couple to internal/publisher while that package is mid-refactor in
// Task 5; the live type-check against publisher.RegisterReader happens where cmd
// wires the poll path and this wrapper together.
type registerReader interface {
	ReadInput(ctx context.Context, addr, count int) ([]uint16, error)
}

// Compile-time assertions: *Locking satisfies both seams, and the concrete
// sidecar client satisfies the wrapper's inner surface.
var (
	_ registerReader             = (*controls.Locking)(nil)
	_ controls.HoldingReadWriter = (*controls.Locking)(nil)
	_ controls.HoldingReadWriter = (*sidecarclient.Client)(nil)
)

// TestLockingRoutesAllThree confirms every method reaches the inner client.
func TestLockingRoutesAllThree(t *testing.T) {
	f := newFakeRW()
	f.regs[43141] = 7
	l := controls.NewLocking(f)

	if _, err := l.ReadInput(context.Background(), 33022, 2); err != nil {
		t.Fatalf("ReadInput: %v", err)
	}
	if _, err := l.ReadHolding(context.Background(), 43141, 1); err != nil {
		t.Fatalf("ReadHolding: %v", err)
	}
	if err := l.WriteHolding(context.Background(), 43141, 9); err != nil {
		t.Fatalf("WriteHolding: %v", err)
	}

	if len(f.inputCalls) != 1 || len(f.holdCalls) != 1 || len(f.writeCalls) != 1 {
		t.Errorf("calls: input=%d holding=%d write=%d, want 1/1/1", len(f.inputCalls), len(f.holdCalls), len(f.writeCalls))
	}
}

// TestLockingSerializes exercises concurrent access under -race: the shared
// mutex must serialize interleaved reads and writes without a data race.
func TestLockingSerializes(t *testing.T) {
	l := controls.NewLocking(newFakeRW())
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); _, _ = l.ReadInput(context.Background(), 33022, 1) }()
		go func() { defer wg.Done(); _, _ = l.ReadHolding(context.Background(), 43141, 1) }()
		go func() { defer wg.Done(); _ = l.WriteHolding(context.Background(), 43141, 1) }()
	}
	wg.Wait()
}
