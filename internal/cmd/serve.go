package cmd

import (
	"context"
	"errors"
	"fmt"
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

// publisherFunc adapts a closure to scheduler.StatePublisher.
type publisherFunc func(context.Context, inverter.Telemetry, homeassistant.Setpoints) error

func (f publisherFunc) PublishState(ctx context.Context, tel inverter.Telemetry, sp homeassistant.Setpoints) error {
	return f(ctx, tel, sp)
}

// toHASetpoints maps the controls-package setpoints (read from the holding bank)
// onto the homeassistant DTO folded into the shared state document. The fields
// are identical; this keeps the two packages decoupled at the serve seam.
func toHASetpoints(sp controls.Setpoints) homeassistant.Setpoints {
	return homeassistant.Setpoints{
		SetChargeCurrent:    sp.SetChargeCurrent,
		SetDischargeCurrent: sp.SetDischargeCurrent,
		OptimalIncome:       sp.OptimalIncome,
	}
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
	// wrapper serializes every register call (input reads via publisher.Collect,
	// holding read/writes via the command handler) on one mutex, because the
	// sidecar's single socket cannot service concurrent Modbus transactions. The
	// scheduler's apiMu serializes whole poll/command cycles on top of it.
	wrapper := controls.NewLocking(client)
	haCfg := homeassistant.Config{
		DiscoveryPrefix: cfg.HADiscoveryPrefix,
		TopicPrefix:     cfg.MQTTTopicPrefix,
		Serial:          cfg.InverterSerial,
		ControlsEnabled: cfg.ControlsEnabled,
	}

	// now is the single clock shared by the scheduler and the controls handler
	// (RTC sync), so tests can inject one fake clock across both.
	now := time.Now

	// svcPtr holds the publisher once MQTT is connected. It is stored by the main
	// goroutine after Connect and loaded by the reconnect hook, the scheduler's
	// StatePublisher adapter and the command refresh, so the hand-off is
	// synchronized through an atomic pointer (the transport goroutine may fire
	// OnConnectionUp as soon as the connection comes up).
	var (
		mc     *mqtt.Client
		svcPtr atomic.Pointer[publisher.Service]
		sched  *scheduler.Scheduler
	)

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
		} else {
			haSp = toHASetpoints(sp)
		}
		return tel, haSp, nil
	}

	// publishState is the scheduler's StatePublisher: it publishes the shared state
	// document once MQTT is connected, and otherwise (mock, no broker) logs decoded
	// telemetry so the pipeline stays observable without a broker.
	publishState := func(ctx context.Context, tel inverter.Telemetry, haSp homeassistant.Setpoints) error {
		svc := svcPtr.Load()
		if svc == nil {
			logger.Info("telemetry",
				"time", tel.Time,
				"battery_soc", tel.Battery.SOCPercent,
				"battery_power_w", tel.Battery.PowerW,
				"pv_power_w", tel.PV.TotalPowerW,
				"grid_power_w", tel.Grid.PowerW,
				"set_charge_current", haSp.SetChargeCurrent,
				"set_discharge_current", haSp.SetDischargeCurrent,
				"optimal_income", haSp.OptimalIncome,
			)
			return nil
		}
		return svc.PublishState(ctx, tel, haSp)
	}

	// rtcSyncer and commander are the controls seams, left as nil interfaces when
	// controls are disabled (no handler) so the scheduler's nil-checks hold — a
	// typed-nil *controls.Handler would defeat them.
	var (
		rtcSyncer scheduler.RTCSyncer
		commander scheduler.Commander
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
			if err := svc.PublishAvailability(ctx, true); err != nil {
				logger.Warn("republish availability failed", "err", err)
			}
			// The scheduler owns the last-good cache; it may not exist yet on the
			// very first OnConnectionUp (fired during Connect, before sched is built).
			if sched != nil {
				if tel, sp, have := sched.LastState(); have {
					if err := svc.PublishState(ctx, tel, sp); err != nil {
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
		svcPtr.Store(publisher.New(mc, haCfg))

		// Writable controls: build the command handler only when CONTROLS_ENABLED.
		// When disabled, the 4 command entities are already omitted from discovery
		// (haCfg.ControlsEnabled=false, ruling R1), so there is nothing to subscribe
		// to and no handler is set — leaving rtcSyncer/commander nil.
		if cfg.ControlsEnabled {
			// refresh re-reads telemetry+setpoints through the wrapper and
			// republishes state after any command that touched the inverter, then
			// updates the scheduler cache so a reconnect in the window before the
			// next poll republishes post-command values rather than stale ones.
			refresh := func(ctx context.Context) {
				tel, haSp, err := readState(ctx)
				if err != nil {
					logger.Warn("controls refresh: read failed", "err", err)
					return
				}
				if svc := svcPtr.Load(); svc != nil {
					if err := svc.PublishState(ctx, tel, haSp); err != nil {
						logger.Warn("controls refresh: publish state failed", "err", err)
					}
				}
				sched.RememberState(tel, haSp)
			}
			handler := controls.NewHandler(wrapper, logger, true, refresh, controls.WithNow(now))
			rtcSyncer = handler
			commander = handler
		}
	}

	if cfg.RTCSyncEnabled && !cfg.ControlsEnabled {
		logger.Warn("RTC_SYNC_ENABLED is true but CONTROLS_ENABLED is false; RTC auto-sync is disabled because it requires the write path")
	}

	sched = scheduler.New(readerFunc(readState), publisherFunc(publishState), status, rtcSyncer, commander, scheduler.Config{
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
