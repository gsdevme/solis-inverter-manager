package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gsdevme/solis-inverter-manager/internal/homeassistant"
	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
)

// fakeReader returns a telemetry snapshot, failing the first failFirstN reads with
// a transient error. rtcTime, when set, is folded into the returned telemetry so
// the RTC-drift tests can control measured drift.
type fakeReader struct {
	mu         sync.Mutex
	calls      int
	failFirstN int
	soc        float64
	rtcTime    time.Time
}

func (r *fakeReader) Read(context.Context) (inverter.Telemetry, homeassistant.Setpoints, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.calls <= r.failFirstN {
		return inverter.Telemetry{}, homeassistant.Setpoints{}, errors.New("transient")
	}
	tel := inverter.Telemetry{Time: r.rtcTime}
	tel.Battery.SOCPercent = r.soc
	return tel, homeassistant.Setpoints{SetChargeCurrent: r.soc}, nil
}

type fakePublisher struct {
	mu    sync.Mutex
	count int
	last  inverter.Telemetry
}

func (p *fakePublisher) PublishState(_ context.Context, tel inverter.Telemetry, _ homeassistant.Setpoints) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count++
	p.last = tel
	return nil
}

func (p *fakePublisher) publishCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.count
}

type fakeHealth struct {
	mu               sync.Mutex
	success, failure int
}

func (h *fakeHealth) MarkSuccess() { h.mu.Lock(); h.success++; h.mu.Unlock() }
func (h *fakeHealth) MarkFailure() { h.mu.Lock(); h.failure++; h.mu.Unlock() }
func (h *fakeHealth) counts() (int, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.success, h.failure
}

// fakeRTC records SyncRTC calls and returns a configurable (wrote, err).
type fakeRTC struct {
	mu    sync.Mutex
	calls int
	wrote bool
	err   error
}

func (r *fakeRTC) SyncRTC(context.Context) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return r.wrote, r.err
}

func (r *fakeRTC) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func baseConfig() Config {
	return Config{
		PollInterval: time.Minute,
		MaxRetries:   3,
		Now:          time.Now,
		After:        time.After,
	}
}

func TestImmediatePollPublishes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &fakeReader{soc: 62}
		p := &fakePublisher{}
		h := &fakeHealth{}
		s := New(r, p, h, nil, nil, baseConfig())

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { s.Run(ctx); close(done) }()

		synctest.Wait()
		if p.publishCount() != 1 {
			t.Fatalf("publish count = %d, want 1 (immediate poll)", p.publishCount())
		}
		if succ, _ := h.counts(); succ != 1 {
			t.Fatalf("health success = %d, want 1", succ)
		}
		cancel()
		<-done
	})
}

func TestTransientRetrySucceeds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &fakeReader{soc: 62, failFirstN: 2} // first 2 reads fail, then succeed
		p := &fakePublisher{}
		h := &fakeHealth{}
		s := New(r, p, h, nil, nil, baseConfig())

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { s.Run(ctx); close(done) }()

		// Immediate poll fails twice, backing off 1s then 2s; advance the fake clock
		// past the backoff so the third attempt succeeds within the same poll.
		time.Sleep(10 * time.Second)
		synctest.Wait()

		if p.publishCount() != 1 {
			t.Fatalf("publish count = %d, want 1 after retries", p.publishCount())
		}
		succ, fail := h.counts()
		if fail != 0 {
			t.Fatalf("health failure = %d, want 0 (retries absorbed the transient errors)", fail)
		}
		if succ != 1 {
			t.Fatalf("health success = %d, want 1", succ)
		}
		cancel()
		<-done
	})
}

func TestPersistentFailureMarksFailure(t *testing.T) {
	// A poll whose reads always fail exhausts its retries and reports one failure,
	// with no publish. MaxRetries=0 keeps it backoff-free and deterministic.
	r := &fakeReader{failFirstN: 1_000_000}
	p := &fakePublisher{}
	h := &fakeHealth{}
	cfg := baseConfig()
	cfg.MaxRetries = 0
	s := New(r, p, h, nil, nil, cfg)

	s.PollNow(context.Background())

	if p.publishCount() != 0 {
		t.Fatalf("publish count = %d, want 0 on persistent failure", p.publishCount())
	}
	succ, fail := h.counts()
	if fail != 1 || succ != 0 {
		t.Fatalf("health = (success %d, failure %d), want (0, 1)", succ, fail)
	}
	// A failing poll must not populate the last-good cache.
	if _, _, have := s.LastState(); have {
		t.Fatalf("LastState have = true after only failures, want false")
	}
}

func TestRetainedCacheViaLastState(t *testing.T) {
	r := &fakeReader{soc: 55}
	p := &fakePublisher{}
	h := &fakeHealth{}
	cfg := baseConfig()
	cfg.MaxRetries = 0
	s := New(r, p, h, nil, nil, cfg)

	// A successful poll fills the cache.
	s.PollNow(context.Background())
	tel, sp, have := s.LastState()
	if !have || tel.Battery.SOCPercent != 55 || sp.SetChargeCurrent != 55 {
		t.Fatalf("LastState = (%v, %v, %v), want cached SOC 55", tel.Battery.SOCPercent, sp.SetChargeCurrent, have)
	}

	// A subsequent failing poll must NOT blank the retained cache.
	r.mu.Lock()
	r.failFirstN = 1_000_000
	r.mu.Unlock()
	s.PollNow(context.Background())
	tel, _, have = s.LastState()
	if !have || tel.Battery.SOCPercent != 55 {
		t.Fatalf("LastState after failure = (%v, %v), want the retained SOC 55", tel.Battery.SOCPercent, have)
	}
}

// rtcSchedulerFor builds a scheduler whose measured drift is `drift` (inverter
// clock ahead of Now by drift), with RTC auto-sync configured per the args.
func rtcSchedulerFor(t *testing.T, enabled bool, threshold, drift time.Duration) (*Scheduler, *fakeRTC) {
	t.Helper()
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	r := &fakeReader{soc: 40, rtcTime: now.Add(drift)}
	rtc := &fakeRTC{wrote: true}
	cfg := baseConfig()
	cfg.MaxRetries = 0
	cfg.RTCSyncEnabled = enabled
	cfg.RTCDriftThreshold = threshold
	cfg.Now = func() time.Time { return now }
	s := New(r, &fakePublisher{}, &fakeHealth{}, rtc, nil, cfg)
	return s, rtc
}

func TestRTCAutoSyncFiresWhenDriftExceedsThreshold(t *testing.T) {
	s, rtc := rtcSchedulerFor(t, true, 60*time.Second, 5*time.Minute)
	s.PollNow(context.Background())
	if rtc.callCount() != 1 {
		t.Fatalf("SyncRTC calls = %d, want 1 (enabled, drift > threshold)", rtc.callCount())
	}
}

func TestRTCAutoSyncFiresOnNegativeDrift(t *testing.T) {
	// Drift is compared on absolute value: an inverter clock behind Now must sync too.
	s, rtc := rtcSchedulerFor(t, true, 60*time.Second, -5*time.Minute)
	s.PollNow(context.Background())
	if rtc.callCount() != 1 {
		t.Fatalf("SyncRTC calls = %d, want 1 (enabled, |drift| > threshold)", rtc.callCount())
	}
}

func TestRTCAutoSyncSkippedUnderThreshold(t *testing.T) {
	s, rtc := rtcSchedulerFor(t, true, 60*time.Second, 30*time.Second)
	s.PollNow(context.Background())
	if rtc.callCount() != 0 {
		t.Fatalf("SyncRTC calls = %d, want 0 (drift < threshold)", rtc.callCount())
	}
}

func TestRTCAutoSyncSkippedWhenDisabled(t *testing.T) {
	s, rtc := rtcSchedulerFor(t, false, 60*time.Second, 5*time.Minute)
	s.PollNow(context.Background())
	if rtc.callCount() != 0 {
		t.Fatalf("SyncRTC calls = %d, want 0 (disabled)", rtc.callCount())
	}
}

// fakeCommander records the last routed command.
type fakeCommander struct {
	mu    sync.Mutex
	calls int
	topic string
}

func (c *fakeCommander) Apply(_ context.Context, topic string, _ []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.topic = topic
}

func TestApplyCommandRoutes(t *testing.T) {
	c := &fakeCommander{}
	s := New(&fakeReader{}, &fakePublisher{}, &fakeHealth{}, nil, c, baseConfig())
	s.ApplyCommand(context.Background(), "solis/cmd/set_charge_current/set", []byte("5"))

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.calls != 1 || c.topic != "solis/cmd/set_charge_current/set" {
		t.Fatalf("commander calls=%d topic=%q, want 1 call on the command topic", c.calls, c.topic)
	}
}

func TestApplyCommandNilCommanderIsNoop(t *testing.T) {
	s := New(&fakeReader{}, &fakePublisher{}, &fakeHealth{}, nil, nil, baseConfig())
	// Must not panic when controls are disabled (nil commander).
	s.ApplyCommand(context.Background(), "solis/cmd/set_charge_current/set", []byte("5"))
}
