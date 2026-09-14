package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"

	"github.com/gsdevme/solis-inverter-manager/internal/config"
	"github.com/gsdevme/solis-inverter-manager/internal/controls"
	"github.com/gsdevme/solis-inverter-manager/internal/homeassistant"
	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
	"github.com/gsdevme/solis-inverter-manager/internal/mqtt"
	"github.com/gsdevme/solis-inverter-manager/internal/publisher"
	"github.com/gsdevme/solis-inverter-manager/internal/schedule"
	"github.com/gsdevme/solis-inverter-manager/internal/scheduler"
	"github.com/gsdevme/solis-inverter-manager/internal/server"
	"github.com/gsdevme/solis-inverter-manager/internal/sidecarclient"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the poll -> sidecar -> MQTT manager",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runServe(cmd.Context())
	},
}

// readerFunc adapts a closure to scheduler.StateReader so serve can hand the
// existing readState closure to the scheduler without a wrapper struct.
type readerFunc func(context.Context) (inverter.Telemetry, homeassistant.Setpoints, error)

func (f readerFunc) Read(ctx context.Context) (inverter.Telemetry, homeassistant.Setpoints, error) {
	return f(ctx)
}

// toHASetpoints maps the controls-package setpoints (read from the holding bank)
// onto the homeassistant DTO folded into the shared state document, keeping the
// two packages decoupled at the serve seam. The boost's end instant is resolved
// here against now, because the homeassistant package is pure and must not read
// the wall clock.
func toHASetpoints(sp controls.Setpoints, now time.Time) homeassistant.Setpoints {
	return homeassistant.Setpoints{
		SetChargeCurrent:    sp.SetChargeCurrent,
		SetDischargeCurrent: sp.SetDischargeCurrent,
		OptimalIncome:       sp.OptimalIncome,
		Slots:               sp.Slots,
		BoostEndsAt:         schedule.BoostOf(sp.Slots).EndsAt(now),
	}
}

// touWindowAttr renders the Time-of-Use window for the no-broker telemetry log,
// using the same derivation as the state document and reading "unset" where that
// publishes null.
func touWindowAttr(slots inverter.TimedSlots) string {
	window, ok := schedule.ToU(slots)
	if !ok {
		return "unset"
	}
	return schedule.FormatWindow(window)
}

// boostSelectAttr renders the boost select's state for the no-broker telemetry
// log, using the same derivation as the state document and reading "null" where
// that publishes JSON null (a window the select's options cannot express).
func boostSelectAttr(slots inverter.TimedSlots) string {
	state := schedule.BoostSelectState(slots)
	if state == nil {
		return "null"
	}
	return *state
}

// sidecarProbeInterval is how often the startup wait re-probes the sidecar's
// /health endpoint while it is still coming up.
const sidecarProbeInterval = 500 * time.Millisecond

// waitForSidecar blocks until the sidecar's HTTP listener answers, bounded by
// timeout, so the manager does not announce itself and poll while its sibling
// container is still starting (the first poll would otherwise fail with a
// connection refused). A timeout of zero disables the wait.
//
// A sidecar that never answers is not fatal — startup continues, and the
// scheduler's retries plus the /readyz gate cover a sidecar that is still down.
// A cancelled parent context (SIGTERM during startup) falls through silently to
// the existing shutdown path rather than logging a warning.
func waitForSidecar(ctx context.Context, client *sidecarclient.Client, timeout time.Duration, logger *slog.Logger) {
	if timeout <= 0 {
		return
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started := time.Now()
	if err := client.WaitUntilServing(waitCtx, sidecarProbeInterval); err != nil {
		if ctx.Err() == nil {
			logger.Warn("sidecar not serving after startup timeout, continuing", "err", err, "timeout", timeout)
		}
		return
	}
	logger.Info("sidecar serving", "elapsed", time.Since(started))
}

func runServe(ctx context.Context) error {
	// A cancellable child of the incoming context so the shutdown path can stop the
	// scheduler itself — notably on the healthErr branch, where the parent ctx is
	// not cancelled but teardown still needs to drain the scheduler goroutine.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	logger := newLogger(cfg.LogLevel, cfg.LogFormat)
	logger.Info("starting", "config", cfg)
	if cfg.Mode == "mock" {
		logger.Warn("running in MOCK mode; not talking to a real inverter or sidecar")
	}

	// Status/health server listens immediately so probes work during init.
	// Readiness starts false; the scheduler drives it via MarkSuccess/MarkFailure.
	status := server.New(server.Config{
		FailureThreshold: cfg.FailureThreshold,
		PollInterval:     cfg.PollInterval,
	})
	healthSrv := &http.Server{Addr: cfg.HealthAddr, Handler: status.Handler()}
	healthErr := make(chan error, 1)
	go func() {
		if err := healthSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			healthErr <- err
			return
		}
		healthErr <- nil
	}()
	logger.Info("health server listening", "addr", cfg.HealthAddr)

	client := sidecarclient.New(cfg.SidecarURL)
	waitForSidecar(ctx, client, cfg.SidecarStartupTimeout, logger)
	// wrapper serializes every register call (input reads via publisher.Collect,
	// holding read/writes via the command handler) on one mutex, because the
	// sidecar's single socket cannot service concurrent Modbus transactions. The
	// scheduler's apiMu serializes whole poll/command cycles on top of it.
	wrapper := controls.NewLocking(client)
	haCfg := homeassistant.Config{
		DiscoveryPrefix: cfg.HADiscoveryPrefix,
		TopicPrefix:     cfg.MQTTTopicPrefix,
		Serial:          cfg.InverterSerial,
		ObjectIDPrefix:  cfg.HAObjectIDPrefix,
		ControlsEnabled: cfg.ControlsEnabled,
	}

	// now is the single clock shared by the scheduler, the controls handler (RTC
	// sync) and the publisher (RTC drift), so tests can inject one fake clock
	// across all three.
	now := time.Now

	// svcPtr holds the publisher. Both modes store one — an MQTT-backed Service
	// once Connect returns, or a Discard-backed Service when no broker is
	// configured — so the publish step is identical either way. It is stored by the
	// main goroutine and loaded by the reconnect hook, statePublisher and the
	// command refresh, so the hand-off is synchronized through an atomic pointer
	// (the transport goroutine may fire OnConnectionUp as soon as the connection
	// comes up, which is the only window in which it is still nil).
	var (
		mc     *mqtt.Client
		svcPtr atomic.Pointer[publisher.Service]
		sched  *scheduler.Scheduler
	)

	// fresh records whether each read decoded setpoints from the inverter or fell
	// back to the cache, so the schedule reconcile can skip a cycle that never saw
	// the live slots.
	var fresh setpointFreshness

	// readState is the scheduler's StateReader: it reads telemetry and the
	// writable-control setpoints through the serialized wrapper. Telemetry and
	// setpoints are read in separate holding frames, so a setpoints-read failure is
	// non-fatal and MUST NOT blank the published control state: on failure we reuse
	// the last-known setpoints from the scheduler's cache (truth over optimism)
	// rather than folding zeros (0 A / OFF) into the state doc. It returns an error
	// only when telemetry itself could not be read, so the scheduler backs off and
	// retries. On the very first poll there is no last-known value, so zero is the
	// acceptable fallback.
	readState := func(ctx context.Context) (inverter.Telemetry, homeassistant.Setpoints, error) {
		tel, err := publisher.Collect(ctx, wrapper)
		if err != nil {
			return inverter.Telemetry{}, homeassistant.Setpoints{}, err
		}
		_, haSp, _ := sched.LastState()
		if sp, err := controls.ReadSetpoints(ctx, wrapper); err != nil {
			logger.Warn("read setpoints failed; reusing last-known setpoints", "err", err)
			fresh.markStale()
		} else {
			haSp = toHASetpoints(sp, now())
			fresh.markFresh()
		}
		return tel, haSp, nil
	}

	// statePub fans every freshly read state out to Home Assistant and the status
	// page; see state.go. Telemetry is logged only without a broker, where the log
	// is the sole window onto the pipeline.
	statePub := &statePublisher{
		svc:          svcPtr.Load,
		status:       status,
		fresh:        &fresh,
		logTelemetry: cfg.MQTTBrokerURL == "",
		logger:       logger,
	}

	// rtcSyncer, commander and reconciler are the controls seams, left as nil
	// interfaces when controls are disabled (no handler) so the scheduler's
	// nil-checks hold — a typed-nil *controls.Handler would defeat them.
	var (
		rtcSyncer  scheduler.RTCSyncer
		commander  scheduler.Commander
		reconciler scheduler.Reconciler
	)

	// MQTT is required in live mode and optional in mock. When a broker URL is
	// configured, connect and publish HA discovery + availability eagerly; the same
	// republish hook re-runs on every reconnect. When it is not, the pipeline still
	// polls and logs decoded telemetry so the mock run stays observable.
	var commandTopic string
	if cfg.MQTTBrokerURL != "" {
		commandTopic = haCfg.BaseTopic() + "/+/set"
		republish := func(ctx context.Context) {
			svc := svcPtr.Load()
			if svc == nil {
				return
			}
			if err := svc.PublishDiscovery(ctx); err != nil {
				logger.Warn("republish discovery failed", "err", err)
			}
			if err := svc.PublishDiscoveryRemovals(ctx); err != nil {
				logger.Warn("republish discovery removals failed", "err", err)
			}
			if err := svc.PublishAvailability(ctx, true); err != nil {
				logger.Warn("republish availability failed", "err", err)
			}
			// The scheduler owns the last-good cache; it may not exist yet on the
			// very first OnConnectionUp (fired during Connect, before sched is built).
			if sched != nil {
				if tel, sp, have := sched.LastState(); have {
					if _, err := svc.PublishState(ctx, tel, sp); err != nil {
						logger.Warn("republish state failed", "err", err)
					}
				}
			}
			// Re-subscribe on every reconnect (idempotent, like discovery) so the
			// command topic survives a broker restart. Gated by CONTROLS_ENABLED.
			if cfg.ControlsEnabled {
				if err := mc.Subscribe(ctx, commandTopic); err != nil {
					logger.Warn("resubscribe command topic failed", "topic", commandTopic, "err", err)
				}
			}
		}

		mc, err = mqtt.Connect(ctx, mqtt.Options{
			BrokerURL:         cfg.MQTTBrokerURL,
			Username:          cfg.MQTTUsername,
			Password:          cfg.MQTTPassword,
			ClientID:          cfg.MQTTClientID,
			AvailabilityTopic: haCfg.AvailabilityTopic(),
			Logger:            logger,
			OnConnectionUp:    republish,
		})
		if err != nil {
			return fmt.Errorf("mqtt: %w", err)
		}
		svcPtr.Store(publisher.New(mc, haCfg, publisher.WithNow(now)))
	} else {
		// No broker: publish into Discard so the poll still builds one state
		// document and the status page still gets it, with no branch in the
		// publish step.
		svcPtr.Store(publisher.New(publisher.Discard{}, haCfg, publisher.WithNow(now)))
	}

	// TOU_WINDOW is parsed once here and handed to the command handler. Config
	// validation already rejects a malformed window, so an error is impossible in
	// practice; it is surfaced rather than swallowed so a future validation gap
	// cannot silently disable Time-of-Use assertion.
	touWindow, assertToU, err := schedule.ParseToUWindow(cfg.TOUWindow)
	if err != nil {
		return fmt.Errorf("tou window: %w", err)
	}

	// Writable controls: build the command handler whenever CONTROLS_ENABLED,
	// independently of MQTT, because the handler also drives the poll-time
	// reconcile and RTC auto-sync — both of which must run in the no-broker mock
	// path. When controls are disabled the five command entities are already
	// omitted from discovery (haCfg.ControlsEnabled=false, ruling R1), so there is
	// nothing to subscribe to and no handler is built — leaving rtcSyncer,
	// commander and reconciler nil.
	if cfg.ControlsEnabled {
		// refresh re-reads telemetry+setpoints through the wrapper and fans the
		// result out after any command that touched the inverter, then updates the
		// scheduler cache so a reconnect in the window before the next poll
		// republishes post-command values rather than stale ones. It goes through
		// the same statePublisher as a poll, so / shows the effect of a command
		// without waiting for the next poll, in both modes.
		refresh := func(ctx context.Context) {
			tel, haSp, err := readState(ctx)
			if err != nil {
				logger.Warn("controls refresh: read failed", "err", err)
				return
			}
			if err := statePub.PublishState(ctx, tel, haSp); err != nil {
				logger.Warn("controls refresh: publish state failed", "err", err)
			}
			sched.RememberState(tel, haSp)
		}
		handler := controls.NewHandler(wrapper, logger, true, refresh,
			controls.WithNow(now),
			controls.WithToU(touWindow, assertToU),
		)
		rtcSyncer = handler
		commander = handler
		reconciler = fresh.gate(handler, logger)
	}

	if cfg.RTCSyncEnabled && !cfg.ControlsEnabled {
		logger.Warn("RTC_SYNC_ENABLED is true but CONTROLS_ENABLED is false; RTC auto-sync is disabled because it requires the write path")
	}
	if cfg.TOUWindow != "" && !cfg.ControlsEnabled {
		logger.Warn("ToU assertion is disabled because CONTROLS_ENABLED=false; it requires the write path", "tou_window", cfg.TOUWindow)
	}

	sched = scheduler.New(readerFunc(readState), statePub, status, rtcSyncer, commander, reconciler, scheduler.Config{
		PollInterval:      cfg.PollInterval,
		MaxRetries:        cfg.PollMaxRetries,
		RTCSyncEnabled:    cfg.RTCSyncEnabled,
		RTCDriftThreshold: cfg.RTCDriftThreshold,
		Logger:            logger,
		Now:               now,
	})

	// Command dispatch runs through the scheduler (ApplyCommand takes apiMu), so a
	// guarded write + re-read + refresh is atomic against a poll. Installed after
	// sched exists and before the command-topic subscribe, so no inbound command is
	// dropped for want of a handler.
	if mc != nil && cfg.ControlsEnabled {
		mc.SetOnMessage(sched.ApplyCommand)
	}

	// Eager publish once after connect, in case the first connection-up fired before
	// svcPtr was stored. Transient publish errors are logged, not fatal.
	if mc != nil {
		svc := svcPtr.Load()
		if err := svc.PublishDiscovery(ctx); err != nil {
			logger.Warn("publish discovery failed", "err", err)
		}
		if err := svc.PublishDiscoveryRemovals(ctx); err != nil {
			logger.Warn("publish discovery removals failed", "err", err)
		}
		if err := svc.PublishAvailability(ctx, true); err != nil {
			logger.Warn("publish availability failed", "err", err)
		}
		if cfg.ControlsEnabled {
			if err := mc.Subscribe(ctx, commandTopic); err != nil {
				logger.Warn("subscribe command topic failed", "topic", commandTopic, "err", err)
			}
		}
	}

	schedDone := make(chan struct{})
	go func() {
		defer close(schedDone)
		sched.Run(ctx)
	}()

	// shutdown is the single authoritative teardown path; both exit branches below
	// funnel through it so teardown happens exactly once, in one place. Ordering is
	// load-bearing: drain the scheduler FIRST (so no in-flight poll/command touches
	// the client concurrently with Disconnect), then publish a retained "offline",
	// then Disconnect — which itself drains any in-flight reconnect-republish
	// goroutine before the clean disconnect that suppresses the Will — and finally
	// stop the health server. It relies on ctx being cancelled so the scheduler
	// goroutine returns and schedDone closes; ctx is already cancelled, so the final
	// publishes use a fresh short-lived context.
	shutdown := func() {
		logger.Info("shutting down")
		<-schedDone
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if mc != nil {
			if svc := svcPtr.Load(); svc != nil {
				if err := svc.PublishAvailability(shutdownCtx, false); err != nil {
					logger.Warn("publish offline failed", "err", err)
				}
			}
			if err := mc.Disconnect(shutdownCtx); err != nil {
				logger.Warn("mqtt disconnect failed", "err", err)
			}
		}
		if err := shutdownServer(healthSrv); err != nil {
			logger.Warn("health server shutdown failed", "err", err)
		}
	}

	logger.Info("manager running")
	select {
	case err := <-healthErr:
		// The health server failed to listen/serve. Cancel so the scheduler drains,
		// run the one teardown path, then surface the health error as the exit error.
		cancel()
		shutdown()
		return fmt.Errorf("health server: %w", err)
	case <-ctx.Done():
		shutdown()
		return nil
	}
}

func shutdownServer(srv *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}
