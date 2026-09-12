package controls

import (
	"context"
	"sync"
)

// sidecar is the register surface the Locking wrapper serializes. It unions the
// input-read path (used by publisher.Collect) with the holding read/write path
// (used by the command handler) so both goroutines share one client safely.
// *sidecarclient.Client satisfies it.
type sidecar interface {
	ReadInput(ctx context.Context, addr, count int) ([]uint16, error)
	ReadHolding(ctx context.Context, addr, count int) ([]uint16, error)
	WriteHolding(ctx context.Context, addr int, value uint16) error
}

// Locking wraps a sidecar client so the poll goroutine (ReadInput via
// publisher.Collect) and the command handler (ReadHolding/WriteHolding via
// Guard) serialize every register call on one mutex. A single underlying socket
// cannot service concurrent Modbus transactions, so all three methods take the
// SAME lock.
//
// Locking satisfies both publisher.RegisterReader (ReadInput) and
// HoldingReadWriter (ReadHolding/WriteHolding).
type Locking struct {
	mu    sync.Mutex
	inner sidecar
}

// NewLocking wraps inner so its register calls are mutually exclusive.
func NewLocking(inner sidecar) *Locking {
	return &Locking{inner: inner}
}

// ReadInput serializes an input-register read.
func (l *Locking) ReadInput(ctx context.Context, addr, count int) ([]uint16, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inner.ReadInput(ctx, addr, count)
}

// ReadHolding serializes a holding-register read.
func (l *Locking) ReadHolding(ctx context.Context, addr, count int) ([]uint16, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inner.ReadHolding(ctx, addr, count)
}

// WriteHolding serializes a holding-register write.
func (l *Locking) WriteHolding(ctx context.Context, addr int, value uint16) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inner.WriteHolding(ctx, addr, value)
}
