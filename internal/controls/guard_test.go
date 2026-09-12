package controls_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gsdevme/solis-inverter-manager/internal/controls"
)

// TestGuardSkipsWhenEqual is the single most important test: when the current
// register already equals desired, Guard must NOT issue any fc06 write
// (flash-wear avoidance) and must report Skipped. DoD test 2.
func TestGuardSkipsWhenEqual(t *testing.T) {
	f := newFakeRW()
	f.regs[43141] = 300

	res, err := controls.Guard(context.Background(), f, 43141, 300)
	if err != nil {
		t.Fatalf("Guard: %v", err)
	}
	if len(f.writeCalls) != 0 {
		t.Fatalf("expected ZERO WriteHolding calls, got %d: %v", len(f.writeCalls), f.writeCalls)
	}
	if !res.Skipped || res.Wrote {
		t.Errorf("result = %+v, want Skipped=true Wrote=false", res)
	}
	if res.Old != 300 || res.New != 300 {
		t.Errorf("Old/New = %d/%d, want 300/300", res.Old, res.New)
	}
}

// TestGuardWritesWhenDiffers covers a write whose re-read confirms. DoD test 1.
func TestGuardWritesWhenDiffers(t *testing.T) {
	f := newFakeRW()
	f.regs[43141] = 100

	res, err := controls.Guard(context.Background(), f, 43141, 300)
	if err != nil {
		t.Fatalf("Guard: %v", err)
	}
	if !res.Wrote || !res.Confirmed || res.Skipped {
		t.Errorf("result = %+v, want Wrote=true Confirmed=true Skipped=false", res)
	}
	if res.Old != 100 || res.New != 300 {
		t.Errorf("Old/New = %d/%d, want 100/300", res.Old, res.New)
	}
	if len(f.writeCalls) != 1 || f.writeCalls[0] != (writeCall{43141, 300}) {
		t.Errorf("writeCalls = %v, want one write of 43141=300", f.writeCalls)
	}
}

// TestGuardRereadMismatch covers a write whose re-read does not confirm: a
// non-fatal error is returned with Confirmed=false. DoD test 3.
func TestGuardRereadMismatch(t *testing.T) {
	f := newFakeRW()
	f.regs[43141] = 100
	stuck := uint16(150)
	f.overrideReread = &stuck

	res, err := controls.Guard(context.Background(), f, 43141, 300)
	if err == nil {
		t.Fatal("expected non-nil mismatch error")
	}
	if res.Confirmed {
		t.Errorf("Confirmed = true, want false on mismatch")
	}
	if !res.Wrote {
		t.Errorf("Wrote = false, want true (fc06 was issued)")
	}
	if res.New != 150 {
		t.Errorf("New = %d, want 150 (the re-read)", res.New)
	}
}

// TestGuardReadError propagates a wrapped read failure and issues no write.
func TestGuardReadError(t *testing.T) {
	f := newFakeRW()
	f.readErr = errBoom

	_, err := controls.Guard(context.Background(), f, 43141, 300)
	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want wraps errBoom", err)
	}
	if len(f.writeCalls) != 0 {
		t.Errorf("expected no writes on read failure, got %v", f.writeCalls)
	}
}

// TestGuardWriteError propagates a wrapped write failure.
func TestGuardWriteError(t *testing.T) {
	f := newFakeRW()
	f.regs[43141] = 100
	f.writeErr = errBoom

	res, err := controls.Guard(context.Background(), f, 43141, 300)
	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want wraps errBoom", err)
	}
	if res.Wrote {
		t.Errorf("Wrote = true, want false when the write itself failed")
	}
}
