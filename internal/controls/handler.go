package controls

import (
	"context"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
	"github.com/gsdevme/solis-inverter-manager/internal/schedule"
)

// Command keys parsed from the trailing `.../<key>/set` topic segment.
const (
	keySetChargeCurrent    = "set_charge_current"
	keySetDischargeCurrent = "set_discharge_current"
	keyOptimalIncome       = "optimal_income"
	keyBoostSelect         = "boost_select"
	keyRTCSync             = "rtc_sync"
)

// keyReconcile labels the reconcile's guarded writes in the log. Unlike the
// command keys it never arrives on a topic: the scheduler drives the reconcile.
const keyReconcile = "reconcile"

// boostSlot is the index of the timed slot reserved for an ad-hoc boost. Slots 1
// and 2 carry the Time-of-Use tariff, so only the last slot is free.
const boostSlot = len(inverter.TimedSlots{}) - 1

// Handler routes inbound MQTT command messages to guarded inverter writes.
//
// It parses the command key from the topic, validates the payload, maps the key
// to a read-before-write Guard action, and invokes a refresh callback after any
// action that touched the inverter (so state is re-read and republished). It
// never panics on a bad command: unparseable payloads, unknown keys and
// malformed topics are logged and dropped. A kill-switch (enabled=false) drops
// every command without touching the inverter.
type Handler struct {
	rw        HoldingReadWriter
	log       *slog.Logger
	enabled   bool
	refresh   func(context.Context)
	now       func() time.Time
	tou       inverter.TimedWindow
	assertToU bool
}

// Option configures a Handler in NewHandler.
type Option func(*Handler)

// WithNow overrides the clock used for rtc_sync, boost planning and reconcile
// expiry (for testability).
func WithNow(now func() time.Time) Option {
	return func(h *Handler) {
		if now != nil {
			h.now = now
		}
	}
}

// WithToU configures the Time-of-Use tariff the manager asserts in slots 1 and 2
// and refuses to let a boost overlap. assert must be the enabled flag
// schedule.ParseToUWindow reports: a zero window is never asserted, since that
// would clear the owner's schedule rather than leave it alone.
func WithToU(window inverter.TimedWindow, assert bool) Option {
	return func(h *Handler) {
		h.tou = window
		h.assertToU = assert
	}
}

// NewHandler builds a command handler. rw is the (Locking-wrapped) sidecar,
// enabled is the CONTROLS_ENABLED kill-switch, and refresh — when non-nil — is
// invoked after any routed action so Task 5 can re-read setpoints and publish
// state. A nil logger falls back to slog.Default.
func NewHandler(rw HoldingReadWriter, log *slog.Logger, enabled bool, refresh func(context.Context), opts ...Option) *Handler {
	if log == nil {
		log = slog.Default()
	}
	h := &Handler{
		rw:      rw,
		log:     log,
		enabled: enabled,
		refresh: refresh,
		now:     time.Now,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Apply is the scheduler's Commander seam: it routes one inbound command exactly
// like OnMessage. The scheduler calls it while holding apiMu, so a whole guarded
// write + re-read + refresh is atomic against a concurrent poll. It is an alias for
// OnMessage kept so the scheduler depends on a small interface rather than the
// concrete handler.
func (h *Handler) Apply(ctx context.Context, topic string, payload []byte) {
	h.OnMessage(ctx, topic, payload)
}

// OnMessage handles one inbound command message. It is defensive end-to-end: a
// top-level recover guarantees a malformed command can never crash the message
// pump.
func (h *Handler) OnMessage(ctx context.Context, topic string, payload []byte) {
	defer func() {
		if r := recover(); r != nil {
			h.log.Error("controls: recovered from panic in OnMessage", "topic", topic, "panic", r)
		}
	}()

	key, ok := commandKey(topic)
	if !ok {
		h.log.Warn("controls: dropping message with unroutable topic", "topic", topic)
		return
	}

	if !h.enabled {
		h.log.Info("controls: disabled, dropping command", "key", key, "topic", topic)
		return
	}

	if h.route(ctx, key, payload) && h.refresh != nil {
		h.refresh(ctx)
	}
}

// route dispatches a validated command. It returns true when the inverter was
// touched (a write was attempted, even if every register was skipped) so the
// caller knows to refresh; it returns false for rejected or unknown commands.
func (h *Handler) route(ctx context.Context, key string, payload []byte) bool {
	switch key {
	case keySetChargeCurrent:
		return h.setCurrent(ctx, key, inverter.RegTimedChargeCurrent, payload)
	case keySetDischargeCurrent:
		return h.setCurrent(ctx, key, inverter.RegTimedDischargeCurrent, payload)
	case keyOptimalIncome:
		return h.setOptimalIncome(ctx, key, payload)
	case keyBoostSelect:
		return h.setBoost(ctx, key, payload)
	case keyRTCSync:
		return h.syncRTC(ctx, key)
	default:
		h.log.Warn("controls: unknown command key", "key", key)
		return false
	}
}

// setCurrent parses an amps payload, clamps it to the HA range (finite-but-out-
// of-range is clamped, not rejected — ruling R6), encodes it and guards the
// write. Only NaN/±Inf/unparseable payloads are rejected.
func (h *Handler) setCurrent(ctx context.Context, key string, addr int, payload []byte) bool {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(string(payload)), 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		h.log.Warn("controls: rejecting invalid amps payload", "key", key, "payload", string(payload))
		return false
	}
	value := inverter.EncodeAmps(inverter.ClampHAChargeAmps(parsed))
	h.guardOne(ctx, key, addr, value)
	return true
}

// setOptimalIncome flips only bit 1 of the 43110 work-mode bitfield, preserving
// every other bit. The payload must be exactly Run or Stop (case-insensitive),
// the select's two options.
func (h *Handler) setOptimalIncome(ctx context.Context, key string, payload []byte) bool {
	on, ok := parseRunStop(payload)
	if !ok {
		h.log.Warn("controls: rejecting non-Run/Stop payload", "key", key, "payload", string(payload))
		return false
	}
	cur, err := readOne(ctx, h.rw, inverter.RegWorkMode)
	if err != nil {
		// Return "routed" (true) even though no write happened: the command was
		// valid and touched the inverter (one read), so refresh should still fire
		// — matching "refresh after any routed action".
		h.log.Error("controls: work-mode read failed", "key", key, "err", err)
		return true
	}
	desired := inverter.DecodeWorkMode(cur).WithTimed(on).Encode()
	h.guardOne(ctx, key, inverter.RegWorkMode, desired)
	return true
}

// setBoost programs the boost slot from one of the select's options. It never
// changes the work mode implicitly, so a boost requested while Optimal Income is
// Stop is refused, as is one schedule.PlanBoost will not plan (a window crossing
// midnight, or one fighting the asserted tariff). Every refusal still reports
// "routed" so the refresh republishes the derived state and the select snaps back
// to Off; only a payload that is not an option at all is dropped outright.
func (h *Handler) setBoost(ctx context.Context, key string, payload []byte) bool {
	mode, minutes, ok := schedule.ParseBoostOption(string(payload))
	if !ok {
		h.log.Warn("controls: invalid boost option", "key", key, "payload", string(payload))
		return false
	}
	if mode == schedule.Off {
		_, _ = h.writeRegisters(ctx, key, inverter.TimedSlotWriteRegisters(boostSlot, inverter.TimedSlot{}))
		return true
	}

	cur, err := readOne(ctx, h.rw, inverter.RegWorkMode)
	if err != nil {
		h.log.Error("controls: work-mode read failed", "key", key, "err", err)
		return true
	}
	if !inverter.DecodeWorkMode(cur).Timed {
		h.log.Warn("controls: boost rejected: optimal income is Stop", "key", key, "option", string(payload))
		return true
	}

	slot, err := schedule.PlanBoost(mode, minutes, h.now(), h.tou, h.assertToU)
	if err != nil {
		h.log.Warn("controls: boost rejected", "key", key, "option", string(payload), "reason", err)
		return true
	}
	_, _ = h.writeRegisters(ctx, key, inverter.TimedSlotWriteRegisters(boostSlot, slot))
	return true
}

// syncRTC guards each of the six RTC holding registers; each Guard skips a
// register whose value already matches, so only drifted registers are written.
// A register that fails its retry stops the sequence, as does a write the re-read
// did not confirm; the error is logged and dropped. This is the manual "Sync RTC
// now" command path; it reports "routed" so refresh fires afterwards.
func (h *Handler) syncRTC(ctx context.Context, key string) bool {
	_, _ = h.syncRTCTo(ctx, key)
	return true
}

// SyncRTC is the scheduler seam for opt-in, threshold-gated RTC auto-sync
// (REQ-HA-13). It writes the six RTC holding registers (43000–43005) to the
// handler's current clock under the read-before-write guard, so only drifted
// registers are actually written. It reports whether any register was written
// (wrote=true means at least one fc06 was issued) and the error that stopped the
// sequence, if any.
// The scheduler invokes this from its poll — already holding apiMu — when RTC
// auto-sync is enabled and measured drift exceeds the threshold.
func (h *Handler) SyncRTC(ctx context.Context) (bool, error) {
	return h.syncRTCTo(ctx, keyRTCSync)
}

// syncRTCTo guards the six RTC registers against the handler clock, logging each
// outcome, and returns whether any write was issued plus the error that stopped
// it. It backs both the manual command (syncRTC) and the scheduler seam
// (SyncRTC).
func (h *Handler) syncRTCTo(ctx context.Context, key string) (bool, error) {
	regs := inverter.RTCWriteRegisters(h.now())
	return h.writeRegisters(ctx, key, regs[:])
}

// writeRegisters guards each register in order under the given log key and
// reports whether any write was issued plus the error that stopped the sequence
// (nil when every register was guarded successfully).
//
// A register whose guard failed without a confirmed write (res.Wrote == false) is
// retried once, immediately, before the next register is attempted. Such a
// failure does not prove the fc06 never reached the inverter — the write can land
// and only the reply be lost — but the retry is safe either way: it re-enters the
// full read-before-write guard, which re-reads first and skips a register that
// already holds the desired value, so retrying after a landed write costs no
// extra flash wear. No sleep separates the two attempts: the caller holds the API
// mutex for the whole command, so nothing else reaches the inverter in between.
//
// A register still failing after its retry aborts the sequence and no later
// register is attempted; so does a register written but not confirmed by the
// re-read, which is not retried at all because the inverter did take the write.
// Stopping leaves the slot in the last shape the manager fully wrote — an intact
// old window, which expires by itself, or a completed clear, which stays clear —
// which the next reconcile can reason about, whereas writing on past the failure
// can compose a window out of two commands' registers that the inverter was never
// meant to hold. Stopping costs little: scheduleDiff hands the reconcile only the
// registers that differ, so the sequence the next poll retries spends reads, not
// writes.
func (h *Handler) writeRegisters(ctx context.Context, key string, regs []inverter.Register) (bool, error) {
	wrote := false

	for _, reg := range regs {
		res, err := Guard(ctx, h.rw, reg.Addr, reg.Value)
		h.logGuard(key, reg.Addr, reg.Value, res, err)
		wrote = wrote || res.Wrote

		if err != nil && !res.Wrote {
			res, err = Guard(ctx, h.rw, reg.Addr, reg.Value)
			h.logGuard(key, reg.Addr, reg.Value, res, err, "retry", true)
			wrote = wrote || res.Wrote
		}
		if err != nil {
			return wrote, err
		}
	}
	return wrote, nil
}

// guardOne runs Guard for one register and logs the outcome. The single-register
// command paths (amps, work-mode) use it; multi-register paths go through
// writeRegisters, which retries once in place and aborts on the first register
// still failing.
func (h *Handler) guardOne(ctx context.Context, key string, addr int, value uint16) {
	res, err := Guard(ctx, h.rw, addr, value)
	h.logGuard(key, addr, value, res, err)
}

// logGuard logs one guarded-write outcome, classifying a re-read mismatch (error)
// apart from a transport failure, a skip (no-op) and a confirmed write. extra is
// appended to every line as additional slog attributes; writeRegisters' retried
// attempt passes "retry", true so an operator can tell a register's retried line
// apart from its first line. The first attempt calls with no extra, so its lines
// are unchanged.
func (h *Handler) logGuard(key string, addr int, value uint16, res Result, err error, extra ...any) {
	switch {
	case err != nil && res.Wrote:
		h.log.Error("controls: write did not confirm", append([]any{"key", key, "addr", addr, "desired", value, "reread", res.New, "err", err}, extra...)...)
	case err != nil:
		h.log.Error("controls: guarded write failed", append([]any{"key", key, "addr", addr, "desired", value, "err", err}, extra...)...)
	case res.Skipped:
		h.log.Info("controls: write skipped (no-op)", append([]any{"key", key, "addr", addr, "value", value}, extra...)...)
	default:
		h.log.Info("controls: write confirmed", append([]any{"key", key, "addr", addr, "old", res.Old, "new", res.New}, extra...)...)
	}
}

// commandKey extracts the command key from a `.../<key>/set` topic. It returns
// ok=false when the final segment is not "set" or no key precedes it.
func commandKey(topic string) (string, bool) {
	segs := strings.Split(topic, "/")
	if len(segs) < 2 {
		return "", false
	}
	if segs[len(segs)-1] != "set" {
		return "", false
	}
	key := segs[len(segs)-2]
	if key == "" {
		return "", false
	}
	return key, true
}

// parseRunStop accepts exactly "Run" or "Stop" (case-insensitive), the two
// options of the Optimal Income select, and reports the boolean plus whether the
// payload was valid.
func parseRunStop(payload []byte) (on, ok bool) {
	switch strings.ToUpper(strings.TrimSpace(string(payload))) {
	case "RUN":
		return true, true
	case "STOP":
		return false, true
	default:
		return false, false
	}
}
