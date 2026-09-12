package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"

	"github.com/gsdevme/solis-inverter-manager/internal/config"
	"github.com/gsdevme/solis-inverter-manager/internal/homeassistant"
	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
	"github.com/gsdevme/solis-inverter-manager/internal/mqtt"
	"github.com/gsdevme/solis-inverter-manager/internal/publisher"
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

// lastState caches the most recent successful telemetry poll. It is written by
// the poll ticker goroutine and read by the MQTT reconnect hook (which runs on a
// transport goroutine), so every access is guarded by the mutex.
type lastState struct {
	mu   sync.Mutex
	tel  inverter.Telemetry
	have bool
}

func runServe(ctx context.Context) error {
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
	// Readiness starts false and is flipped ready after the first successful poll.
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
	haCfg := homeassistant.Config{
		DiscoveryPrefix: cfg.HADiscoveryPrefix,
		TopicPrefix:     cfg.MQTTTopicPrefix,
		Serial:          cfg.InverterSerial,
	}
	cache := &lastState{}

	// MQTT is required in live mode and optional in mock. When a broker URL is
	// configured, connect and publish HA discovery + availability eagerly; the
	// same republish hook re-runs on every reconnect. When it is not, the pipeline
	// still polls and logs decoded telemetry so the mock run stays observable.
	// svcPtr holds the publisher once MQTT is connected. It is stored by the main
	// goroutine after Connect and loaded by the reconnect hook and the poll ticker
	// goroutine, so the hand-off is synchronized through an atomic pointer rather
	// than a plain assignment (which would race with the transport goroutine that
	// fires OnConnectionUp as soon as the connection comes up).
	var (
		mc     *mqtt.Client
		svcPtr atomic.Pointer[publisher.Service]
	)
	if cfg.MQTTBrokerURL != "" {
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
			cache.mu.Lock()
			tel, have := cache.tel, cache.have
			cache.mu.Unlock()
			if have {
				if err := svc.PublishState(ctx, tel); err != nil {
					logger.Warn("republish state failed", "err", err)
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
		mc.SetOnConnectionUp(republish)

		// Eager publish once after connect, in case the first connection-up fired
		// before svcPtr was stored. Transient publish errors are logged, not fatal.
		svc := svcPtr.Load()
		if err := svc.PublishDiscovery(ctx); err != nil {
			logger.Warn("publish discovery failed", "err", err)
		}
		if err := svc.PublishAvailability(ctx, true); err != nil {
			logger.Warn("publish availability failed", "err", err)
		}
	}

	var readyOnce sync.Once
	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		// INTERIM poll ticker: a fixed-interval placeholder with no backoff,
		// replaced by internal/scheduler in Phase 6.
		ticker := time.NewTicker(cfg.PollInterval)
		defer ticker.Stop()

		poll := func() {
			tel, err := publisher.Collect(ctx, client)
			if err != nil {
				logger.Warn("poll failed", "err", err)
				return
			}
			cache.mu.Lock()
			cache.tel, cache.have = tel, true
			cache.mu.Unlock()

			if svc := svcPtr.Load(); svc != nil {
				if err := svc.PublishState(ctx, tel); err != nil {
					logger.Warn("publish state failed", "err", err)
				}
			} else {
				logger.Info("telemetry",
					"time", tel.Time,
					"battery_soc", tel.Battery.SOCPercent,
					"battery_power_w", tel.Battery.PowerW,
					"pv_power_w", tel.PV.TotalPowerW,
					"grid_power_w", tel.Grid.PowerW,
				)
			}
			readyOnce.Do(func() { status.SetReady(true) })
		}

		poll()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				poll()
			}
		}
	}()

	logger.Info("manager running")
	select {
	case err := <-healthErr:
		return fmt.Errorf("health server: %w", err)
	case <-ctx.Done():
	}
	logger.Info("shutting down")

	// Graceful shutdown: drain the poll goroutine FIRST so no in-flight poll can
	// use the client concurrently with Disconnect, then publish a retained
	// "offline" and disconnect the broker cleanly before stopping the health
	// server. ctx is already cancelled, so use a fresh short-lived context for
	// these final publishes.
	<-pollDone
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
	return shutdownServer(healthSrv)
}

func shutdownServer(srv *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}
