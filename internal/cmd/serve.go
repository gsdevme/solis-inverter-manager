package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/gsdevme/solis-inverter-manager/internal/config"
	"github.com/gsdevme/solis-inverter-manager/internal/server"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the poll -> sidecar -> MQTT manager",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runServe(cmd.Context())
	},
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
	// Readiness starts false and is flipped ready by the scheduler after the first
	// successful poll (see internal/scheduler; wired in a later phase).
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

	// TODO(phase-2+): construct the sidecar client (internal/sidecarclient) against
	// cfg.SidecarURL, the inverter adapter (internal/inverter), the MQTT client
	// (internal/mqtt) + HA discovery (internal/homeassistant) + publisher
	// (internal/publisher), then start the scheduler (internal/scheduler) which polls
	// every cfg.PollInterval, applies the READ-BEFORE-WRITE write-guard on setpoints,
	// publishes state, and calls status.SetReady(true) after the first success.

	logger.Info("manager running (scaffold: no polling yet)")
	select {
	case err := <-healthErr:
		return fmt.Errorf("health server: %w", err)
	case <-ctx.Done():
	}
	logger.Info("shutting down")

	// TODO(phase-2+): on shutdown, publish retained MQTT "offline" availability and
	// Disconnect() the broker cleanly before stopping the health server.
	return shutdownServer(healthSrv)
}

func shutdownServer(srv *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}
