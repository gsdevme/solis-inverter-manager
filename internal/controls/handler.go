package controls

import (
	"context"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
)

// Command keys parsed from the trailing `.../<key>/set` topic segment.
const (
	keySetChargeCurrent    = "set_charge_current"
	keySetDischargeCurrent = "set_discharge_current"
	keyOptimalIncome       = "optimal_income"
	keyRTCSync             = "rtc_sync"
)

// Handler routes inbound MQTT command messages to guarded inverter writes.
//
// It parses the command key from the topic, validates the payload, maps the key
// to a read-before-write Guard action, and invokes a refresh callback after any
// action that touched the inverter (so state is re-read and republished). It
// never panics on a bad command: unparseable payloads, unknown keys and
// malformed topics are logged and dropped. A kill-switch (enabled=false) drops
// every command without touching the inverter.
type Handler struct {
	rw      HoldingReadWriter
	log     *slog.Logger
	enabled bool
	refresh func(context.Context)
	now     func() time.Time
}

// Option configures a Handler in NewHandler.
type Option func(*Handler)

// WithNow overrides the clock used for rtc_sync (for testability).
func WithNow(now func() time.Time) Option {
	return func(h *Handler) {
		if now != nil {
			h.now = now
		}
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
// every other bit. The payload must be exactly ON or OFF (case-insensitive).
func (h *Handler) setOptimalIncome(ctx context.Context, key string, payload []byte) bool {
	on, ok := parseOnOff(payload)
	if !ok {
		h.log.Warn("controls: rejecting non-ON/OFF payload", "key", key, "payload", string(payload))
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

// syncRTC guards each of the six RTC holding registers; each Guard skips a
// register whose value already matches, so only drifted registers are written.
// A single register failure is logged and the loop continues. This is the manual
// "Sync RTC now" command path; it reports "routed" so refresh fires afterwards.
func (h *Handler) syncRTC(ctx context.Context, key string) bool {
	_, _ = h.syncRTCTo(ctx, key)
	return true
}

// SyncRTC is the scheduler seam for opt-in, threshold-gated RTC auto-sync
// (REQ-HA-13). It writes the six RTC holding registers (43000–43005) to the
// handler's current clock under the read-before-write guard, so only drifted
// registers are actually written. It reports whether any register was written
// (wrote=true means at least one fc06 was issued) and the first error, if any.
// The scheduler invokes this from its poll — already holding apiMu — when RTC
// auto-sync is enabled and measured drift exceeds the threshold.
func (h *Handler) SyncRTC(ctx context.Context) (bool, error) {
	return h.syncRTCTo(ctx, keyRTCSync)
}

// syncRTCTo guards the six RTC registers against the handler clock, logging each
// outcome, and returns whether any write was issued plus the first error seen. It
// backs both the manual command (syncRTC) and the scheduler seam (SyncRTC).
func (h *Handler) syncRTCTo(ctx context.Context, key string) (bool, error) {
	wrote := false
	var firstErr error
	for _, reg := range inverter.RTCWriteRegisters(h.now()) {
		res, err := Guard(ctx, h.rw, reg.Addr, reg.Value)
		h.logGuard(key, reg.Addr, reg.Value, res, err)
		if res.Wrote {
			wrote = true
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return wrote, firstErr
}

// guardOne runs Guard for one register and logs the outcome. The single-register
// command paths (amps, work-mode) use it; syncRTCTo calls Guard directly so it can
// aggregate the per-register results.
func (h *Handler) guardOne(ctx context.Context, key string, addr int, value uint16) {
	res, err := Guard(ctx, h.rw, addr, value)
	h.logGuard(key, addr, value, res, err)
}

// logGuard logs one guarded-write outcome, classifying a re-read mismatch (error)
// apart from a transport failure, a skip (no-op) and a confirmed write.
func (h *Handler) logGuard(key string, addr int, value uint16, res Result, err error) {
	switch {
	case err != nil && res.Wrote:
		h.log.Error("controls: write did not confirm", "key", key, "addr", addr, "desired", value, "reread", res.New, "err", err)
	case err != nil:
		h.log.Error("controls: guarded write failed", "key", key, "addr", addr, "desired", value, "err", err)
	case res.Skipped:
		h.log.Info("controls: write skipped (no-op)", "key", key, "addr", addr, "value", value)
	default:
		h.log.Info("controls: write confirmed", "key", key, "addr", addr, "old", res.Old, "new", res.New)
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

// parseOnOff accepts exactly "ON" or "OFF" (case-insensitive) and reports the
// boolean plus whether the payload was valid.
func parseOnOff(payload []byte) (on, ok bool) {
	switch strings.ToUpper(strings.TrimSpace(string(payload))) {
	case "ON":
		return true, true
	case "OFF":
		return false, true
	default:
		return false, false
	}
}
