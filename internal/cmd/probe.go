package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gsdevme/solis-inverter-manager/internal/controls"
	"github.com/gsdevme/solis-inverter-manager/internal/homeassistant"
	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
	"github.com/gsdevme/solis-inverter-manager/internal/publisher"
	"github.com/gsdevme/solis-inverter-manager/internal/sidecarclient"
)

// The probe is the read-only register workbench (docs/specs/11-probe.md). It
// talks to the sidecar alone — no MQTT, no Home Assistant, no scheduler — and
// reuses the manager's own decoders, so what it prints is what the manager would
// publish. It has no write verb on purpose: every write stays on the guarded
// command path (REQ-HA-10), and the probe exists to answer register questions
// before anything is written.

// probeMaxRead is the widest single Modbus read the probe issues. The live
// Solarman datalogger NAKs reads wider than ~100 registers (findings.md
// ambiguity #10), so a wider span is split into sequential chunks of at most
// this many — sequential because two reads in flight on the one datalogger
// socket time out (same ambiguity).
const probeMaxRead = 100

// probeDefaultSidecar is the sidecar URL the probe falls back to when neither
// --sidecar nor SIDECAR_URL is set; the manager's own default.
const probeDefaultSidecar = "http://127.0.0.1:8081"

// probeReader is the read-only register surface the probe needs.
// *sidecarclient.Client satisfies it; the tests use a fake.
type probeReader interface {
	ReadInput(ctx context.Context, addr, count int) ([]uint16, error)
	ReadHolding(ctx context.Context, addr, count int) ([]uint16, error)
}

// say writes formatted probe output. Output is best-effort by design — a closed
// pipe must not turn a successful read into an error — so the write error is
// deliberately dropped here, once, instead of at every call site.
func say(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format, args...)
}

// prober runs the probe verbs against a reader, writing human-readable output
// to out. now is the seam for the decode and capture timestamps.
type prober struct {
	bus probeReader
	out io.Writer
	now func() time.Time
}

// newProber builds a prober over bus writing to out, with the wall clock.
func newProber(bus probeReader, out io.Writer) *prober {
	return &prober{bus: bus, out: out, now: time.Now}
}

// span is one contiguous register run, as parsed from an "addr:count" argument.
type span struct {
	addr, count int
}

// parseSpan parses "addr:count" (or "addr" alone for one register), bounding
// the address to the Modbus range and the count to at least one.
func parseSpan(text string) (span, error) {
	addrText, countText, hasCount := strings.Cut(strings.TrimSpace(text), ":")
	addr, err := strconv.Atoi(addrText)
	if err != nil || addr < 0 || addr > 65535 {
		return span{}, fmt.Errorf("span %q: address must be 0–65535", text)
	}
	count := 1
	if hasCount {
		count, err = strconv.Atoi(countText)
		if err != nil || count < 1 || addr+count-1 > 65535 {
			return span{}, fmt.Errorf("span %q: count must be >= 1 and stay within 0–65535", text)
		}
	}
	return span{addr: addr, count: count}, nil
}

// read reads one span of the named bank ("input" or "holding"), splitting it
// into sequential chunks of at most probeMaxRead registers.
func (p *prober) read(ctx context.Context, kind string, s span) ([]uint16, error) {
	var fn func(context.Context, int, int) ([]uint16, error)
	switch kind {
	case "input":
		fn = p.bus.ReadInput
	case "holding":
		fn = p.bus.ReadHolding
	default:
		return nil, fmt.Errorf("unknown register bank %q", kind)
	}
	out := make([]uint16, 0, s.count)
	for done := 0; done < s.count; {
		n := min(probeMaxRead, s.count-done)
		regs, err := fn(ctx, s.addr+done, n)
		if err != nil {
			return nil, fmt.Errorf("read %s %d+%d: %w", kind, s.addr+done, n, err)
		}
		if len(regs) < n {
			return nil, fmt.Errorf("read %s %d+%d: short response, got %d registers", kind, s.addr+done, n, len(regs))
		}
		out = append(out, regs[:n]...)
		done += n
	}
	return out, nil
}

// dump reads a span and prints one row per register: address, unsigned value,
// hex, and the signed (S16) reading for the rows where sign matters.
func (p *prober) dump(ctx context.Context, kind string, s span) error {
	regs, err := p.read(ctx, kind, s)
	if err != nil {
		return err
	}
	say(p.out, "%s %d..%d (%d registers)\n", kind, s.addr, s.addr+s.count-1, s.count)
	say(p.out, "%-6s %6s %6s %7s\n", "addr", "u16", "hex", "s16")
	for i, v := range regs {
		say(p.out, "%-6d %6d 0x%04X %7d\n", s.addr+i, v, v, int16(v))
	}
	return nil
}

// decode runs one real poll — the manager's own Collect and ReadSetpoints — and
// prints the state document Home Assistant would receive, pretty-printed. It is
// the "is the inverter reachable and does it decode sanely" check with no broker.
func (p *prober) decode(ctx context.Context) error {
	tel, err := publisher.Collect(ctx, p.bus)
	if err != nil {
		return fmt.Errorf("collect telemetry: %w", err)
	}
	sp, err := controls.ReadSetpoints(ctx, readOnly{p.bus})
	if err != nil {
		return fmt.Errorf("read setpoints: %w", err)
	}
	now := p.now()
	msg, err := homeassistant.Config{}.BuildState(tel, inverter.Drift(tel.Time, now), toHASetpoints(sp, now))
	if err != nil {
		return err
	}
	var pretty json.RawMessage = msg.Payload
	body, err := json.MarshalIndent(pretty, "", "  ")
	if err != nil {
		return err
	}
	say(p.out, "%s\n", body)
	return nil
}

// readOnly adapts the probe's read-only reader to controls.HoldingReadWriter so
// ReadSetpoints can run unchanged. ReadSetpoints never writes; the WriteHolding
// here exists only to satisfy the interface and refuses, which is the probe's
// no-write rule stated in code.
type readOnly struct{ probeReader }

func (readOnly) WriteHolding(context.Context, int, uint16) error {
	return errors.New("probe is read-only: writes stay on the guarded command path")
}

// fixtureBlock and fixture mirror the docs/phase0/fixtures snapshot shape the
// decode tests load (inverter/fixture_test.go) and the sidecar mock seeds from,
// so a capture is a test fixture as soon as it is written.
type fixtureBlock struct {
	Kind   string            `json:"kind"`
	Addr   int               `json:"addr"`
	Count  int               `json:"count"`
	Regs   []uint16          `json:"regs"`
	ByAddr map[string]uint16 `json:"by_addr"`
}

type fixture struct {
	Connection string         `json:"connection"`
	CapturedAt string         `json:"captured_at"`
	Via        string         `json:"via"`
	Blocks     []fixtureBlock `json:"blocks"`
}

// capture reads every span and writes them as one fixture file. Each span is one
// block of the file even when it was read in several chunks on the wire.
func (p *prober) capture(ctx context.Context, path string, input, holding []span) error {
	if len(input)+len(holding) == 0 {
		return errors.New("capture: name at least one --input or --holding span")
	}
	f := fixture{
		Connection: "ok",
		CapturedAt: p.now().UTC().Format(time.RFC3339),
		Via:        "solis-inverter-manager probe capture",
	}
	add := func(kind string, spans []span) error {
		for _, s := range spans {
			regs, err := p.read(ctx, kind, s)
			if err != nil {
				return err
			}
			b := fixtureBlock{Kind: kind, Addr: s.addr, Count: s.count, Regs: regs, ByAddr: make(map[string]uint16, s.count)}
			for i, v := range regs {
				b.ByAddr[strconv.Itoa(s.addr+i)] = v
			}
			f.Blocks = append(f.Blocks, b)
		}
		return nil
	}
	if err := add("input", input); err != nil {
		return err
	}
	if err := add("holding", holding); err != nil {
		return err
	}
	body, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := writeNewFile(path, append(body, '\n')); err != nil {
		return fmt.Errorf("write fixture: %w", err)
	}
	say(p.out, "wrote %s (%d blocks)\n", path, len(f.Blocks))
	return nil
}

// writeNewFile creates path and writes body, refusing to touch a file that
// already exists: docs/phase0/fixtures holds live evidence, and a before/after
// capture pair under the recommended <topic>-<date>.json name must never
// overwrite its "before" half.
func writeNewFile(path string, body []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists; pick a new name", path)
		}
		return err
	}
	if _, err := f.Write(body); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}

// --- cobra wiring ---

var (
	probeSidecar string
	probeTimeout time.Duration
)

var probeCmd = &cobra.Command{
	Use:   "probe",
	Short: "Read and decode inverter registers through the sidecar, with no MQTT or Home Assistant",
	Long: `Read-only register workbench for confirming the register map against the live
inverter. It talks to the sidecar alone (start one with MODE=live python -m sidecar)
and reuses the manager's own decoders. It never writes: writes stay on the guarded
command path. See docs/specs/11-probe.md.`,
}

// newProbeClient resolves the sidecar URL (--sidecar, then SIDECAR_URL, then the
// manager's default) and builds the typed client with the probe timeout.
func newProbeClient(cmd *cobra.Command) *sidecarclient.Client {
	url := probeSidecar
	if !cmd.Flags().Changed("sidecar") {
		url = getEnvDefault("SIDECAR_URL", probeDefaultSidecar)
	}
	return sidecarclient.New(url, sidecarclient.WithTimeout(probeTimeout))
}

// getEnvDefault returns the environment value or def when unset/empty.
func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// probeDumpCmd builds the read-input / read-holding verbs over one bank.
func probeDumpCmd(kind string) *cobra.Command {
	return &cobra.Command{
		Use:   "read-" + kind + " <addr> [count]",
		Short: "Dump raw " + kind + " registers (fc0" + map[string]string{"input": "4", "holding": "3"}[kind] + ")",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			text := args[0]
			if len(args) == 2 {
				text += ":" + args[1]
			}
			s, err := parseSpan(text)
			if err != nil {
				return err
			}
			return newProber(newProbeClient(cmd), cmd.OutOrStdout()).dump(cmd.Context(), kind, s)
		},
	}
}

var probeDecodeCmd = &cobra.Command{
	Use:   "decode",
	Short: "Run one real poll and print the state document Home Assistant would receive",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return newProber(newProbeClient(cmd), cmd.OutOrStdout()).decode(cmd.Context())
	},
}

var (
	captureOut     string
	captureInput   []string
	captureHolding []string
)

var probeCaptureCmd = &cobra.Command{
	Use:   "capture --out <file> [--input addr:count]... [--holding addr:count]...",
	Short: "Write register spans as a docs/phase0/fixtures-shaped JSON fixture",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		parse := func(texts []string) ([]span, error) {
			spans := make([]span, 0, len(texts))
			for _, t := range texts {
				s, err := parseSpan(t)
				if err != nil {
					return nil, err
				}
				spans = append(spans, s)
			}
			return spans, nil
		}
		input, err := parse(captureInput)
		if err != nil {
			return err
		}
		holding, err := parse(captureHolding)
		if err != nil {
			return err
		}
		return newProber(newProbeClient(cmd), cmd.OutOrStdout()).capture(cmd.Context(), captureOut, input, holding)
	},
}

func init() {
	probeCmd.PersistentFlags().StringVar(&probeSidecar, "sidecar", probeDefaultSidecar, "sidecar base URL (default: $SIDECAR_URL, then this value)")
	probeCmd.PersistentFlags().DurationVar(&probeTimeout, "timeout", 15*time.Second, "per-request HTTP timeout")
	probeCaptureCmd.Flags().StringVar(&captureOut, "out", "", "fixture file to write (required)")
	probeCaptureCmd.Flags().StringArrayVar(&captureInput, "input", nil, "input span addr:count (repeatable)")
	probeCaptureCmd.Flags().StringArrayVar(&captureHolding, "holding", nil, "holding span addr:count (repeatable)")
	_ = probeCaptureCmd.MarkFlagRequired("out")
	probeCmd.AddCommand(probeDumpCmd("input"), probeDumpCmd("holding"), probeDecodeCmd, probeCaptureCmd)
	rootCmd.AddCommand(probeCmd)
}
