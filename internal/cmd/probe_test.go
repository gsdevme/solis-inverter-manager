package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recordingBus wraps fakeBus, recording every read in order.
type recordingBus struct {
	*fakeBus
	calls []holdCallRec
}

type holdCallRec struct {
	kind        string
	addr, count int
}

func (b *recordingBus) record(kind string, addr, count int) {
	b.calls = append(b.calls, holdCallRec{kind, addr, count})
}

func (b *recordingBus) ReadInput(ctx context.Context, addr, count int) ([]uint16, error) {
	b.record("input", addr, count)
	return b.fakeBus.ReadInput(ctx, addr, count)
}

func (b *recordingBus) ReadHolding(ctx context.Context, addr, count int) ([]uint16, error) {
	b.record("holding", addr, count)
	return b.fakeBus.ReadHolding(ctx, addr, count)
}

func newRecordingBus() *recordingBus {
	return &recordingBus{fakeBus: &fakeBus{input: map[int]uint16{}, holding: map[int]uint16{}}}
}

func testProber(bus probeReader, out *bytes.Buffer) *prober {
	p := newProber(bus, out)
	p.now = func() time.Time { return time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC) }
	return p
}

func TestParseSpan(t *testing.T) {
	good := map[string]span{"43110": {43110, 1}, "43141:2": {43141, 2}, " 33022:100 ": {33022, 100}}
	for text, want := range good {
		got, err := parseSpan(text)
		if err != nil || got != want {
			t.Errorf("parseSpan(%q) = %+v, %v; want %+v", text, got, err, want)
		}
	}
	for _, text := range []string{"", "abc", "-1", "70000", "43110:0", "43110:x", "65535:2"} {
		if _, err := parseSpan(text); err == nil {
			t.Errorf("parseSpan(%q) accepted, want an error", text)
		}
	}
}

// TestProbeReadChunksAtTheDataloggerCap pins findings ambiguity #10: a span wider
// than 100 registers is split into sequential reads of at most 100, in address
// order, and reassembled in order.
func TestProbeReadChunksAtTheDataloggerCap(t *testing.T) {
	bus := newRecordingBus()
	bus.holding[43000] = 26
	bus.holding[43100] = 7
	bus.holding[43249] = 9
	p := testProber(bus, &bytes.Buffer{})

	regs, err := p.read(context.Background(), "holding", span{43000, 250})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := []holdCallRec{{"holding", 43000, 100}, {"holding", 43100, 100}, {"holding", 43200, 50}}
	if len(bus.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", bus.calls, want)
	}
	for i := range want {
		if bus.calls[i] != want[i] {
			t.Errorf("call %d = %v, want %v", i, bus.calls[i], want[i])
		}
	}
	if len(regs) != 250 || regs[0] != 26 || regs[100] != 7 || regs[249] != 9 {
		t.Errorf("reassembled block wrong: len %d, [0]=%d [100]=%d [249]=%d", len(regs), regs[0], regs[100], regs[249])
	}
}

func TestProbeDumpPrintsOneRowPerRegister(t *testing.T) {
	bus := newRecordingBus()
	bus.holding[43141] = 0
	bus.holding[43142] = 50
	var out bytes.Buffer

	if err := testProber(bus, &out).dump(context.Background(), "holding", span{43141, 2}); err != nil {
		t.Fatalf("dump: %v", err)
	}
	for _, want := range []string{"holding 43141..43142 (2 registers)", "43141 ", "43142 ", "0x0032"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
	if bus.calls[0] != (holdCallRec{"holding", 43141, 2}) {
		t.Errorf("read = %v, want one holding read of 43141+2", bus.calls)
	}
}

// TestProbeDecodePrintsTheStateDocument: decode is the manager's own poll —
// Collect plus ReadSetpoints through BuildState — so the output is the exact
// Home Assistant state document, read with the manager's frames.
func TestProbeDecodePrintsTheStateDocument(t *testing.T) {
	bus := newRecordingBus()
	bus.input[33139] = 57   // battery SOC
	bus.holding[43110] = 35 // work mode: optimal income Run
	var out bytes.Buffer

	if err := testProber(bus, &out).decode(context.Background()); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	for key, want := range map[string]any{"battery_soc": float64(57), "optimal_income": "Run"} {
		if doc[key] != want {
			t.Errorf("%s = %v, want %v", key, doc[key], want)
		}
	}
	// The exact frames the manager issues: two input blocks and one setpoint block.
	want := []holdCallRec{{"input", 33022, 100}, {"input", 33122, 93}, {"holding", 43110, 61}}
	if len(bus.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", bus.calls, want)
	}
	for i := range want {
		if bus.calls[i] != want[i] {
			t.Errorf("call %d = %v, want %v", i, bus.calls[i], want[i])
		}
	}
}

// TestProbeCaptureWritesALoadableFixture: a capture has the exact shape of the
// docs/phase0/fixtures snapshots — kind/addr/count/regs/by_addr blocks — so it
// drops straight into the decode tests and the sidecar mock seed.
func TestProbeCaptureWritesALoadableFixture(t *testing.T) {
	bus := newRecordingBus()
	bus.holding[43110] = 35
	bus.holding[43111] = 7
	bus.input[33139] = 57
	path := filepath.Join(t.TempDir(), "probe.json")
	var out bytes.Buffer

	err := testProber(bus, &out).capture(context.Background(), path, []span{{33139, 1}}, []span{{43110, 2}})
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	if f.Connection != "ok" || f.CapturedAt != "2026-10-31T09:00:00Z" || !strings.Contains(f.Via, "probe capture") {
		t.Errorf("header = %+v", f)
	}
	if len(f.Blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(f.Blocks))
	}
	in, hold := f.Blocks[0], f.Blocks[1]
	if in.Kind != "input" || in.Addr != 33139 || in.Count != 1 || in.Regs[0] != 57 || in.ByAddr["33139"] != 57 {
		t.Errorf("input block = %+v", in)
	}
	if hold.Kind != "holding" || hold.Addr != 43110 || hold.Count != 2 || hold.Regs[1] != 7 || hold.ByAddr["43110"] != 35 || hold.ByAddr["43111"] != 7 {
		t.Errorf("holding block = %+v", hold)
	}
	if !strings.Contains(out.String(), "wrote "+path) {
		t.Errorf("output = %q", out.String())
	}
}

// TestProbeCaptureRefusesToOverwrite: an existing fixture is live evidence, so a
// second capture to the same path fails and leaves the file untouched.
func TestProbeCaptureRefusesToOverwrite(t *testing.T) {
	bus := newRecordingBus()
	bus.holding[43110] = 35
	path := filepath.Join(t.TempDir(), "probe.json")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := testProber(bus, &out).capture(context.Background(), path, nil, []span{{43110, 1}})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("capture over an existing file: err=%v, want already-exists", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "before\n" {
		t.Fatalf("existing fixture was modified: %q", got)
	}
}

func TestProbeCaptureRefusesNoSpans(t *testing.T) {
	p := testProber(newRecordingBus(), &bytes.Buffer{})
	if err := p.capture(context.Background(), filepath.Join(t.TempDir(), "x.json"), nil, nil); err == nil {
		t.Fatal("capture with no spans should error")
	}
}

// TestProbeCommandTreeIsWired pins the CLI surface the skill documents.
func TestProbeCommandTreeIsWired(t *testing.T) {
	want := map[string]bool{"read-input": false, "read-holding": false, "decode": false, "capture": false}
	for _, c := range probeCmd.Commands() {
		want[c.Name()] = true
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("probe has no %q subcommand", name)
		}
	}
	found := false
	for _, c := range rootCmd.Commands() {
		if c.Name() == "probe" {
			found = true
		}
	}
	if !found {
		t.Error("root command has no probe subcommand")
	}
}
