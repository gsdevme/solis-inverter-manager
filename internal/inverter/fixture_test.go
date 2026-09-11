package inverter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// fixtureBlock mirrors one block in a docs/phase0/fixtures snapshot.
type fixtureBlock struct {
	Kind   string            `json:"kind"`
	Addr   int               `json:"addr"`
	Count  int               `json:"count"`
	Regs   []uint16          `json:"regs"`
	ByAddr map[string]uint16 `json:"by_addr"`
}

// fixture mirrors the top-level {connection, blocks} snapshot shape.
type fixture struct {
	Connection string         `json:"connection"`
	Blocks     []fixtureBlock `json:"blocks"`
}

// loadFixture reads a fixture from docs/phase0/fixtures relative to the package
// directory (the test working directory).
func loadFixture(t *testing.T, name string) fixture {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "phase0", "fixtures", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	return f
}

// snapshot converts the fixture blocks into a decode Snapshot.
func (f fixture) snapshot() Snapshot {
	s := make(Snapshot, 0, len(f.Blocks))
	for _, b := range f.Blocks {
		s = append(s, Block{Base: b.Addr, Regs: b.Regs})
	}
	return s
}

// raw returns the raw register word at an absolute address, sourced from the
// fixture's by_addr map — the arithmetic ground truth for expected values.
func (f fixture) raw(t *testing.T, addr int) uint16 {
	t.Helper()
	key := strconv.Itoa(addr)
	for _, b := range f.Blocks {
		if v, ok := b.ByAddr[key]; ok {
			return v
		}
	}
	t.Fatalf("register %d not present in fixture", addr)
	return 0
}
