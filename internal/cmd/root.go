// Package cmd wires the CLI: `serve` runs the poll->sidecar->MQTT manager for the
// Solis inverter. It is the composition root and owns process lifecycle (signal
// handling, graceful shutdown). See docs/specs/06-lifecycle-health.md.
package cmd

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "solis-inverter-manager",
	Short: "Poll a Solis inverter (via a localhost sidecar) and publish to MQTT with Home Assistant autodiscovery",
	// Handle error reporting and exit codes in main() instead: don't let cobra
	// print the error or dump usage on runtime failures.
	SilenceErrors: true,
	SilenceUsage:  true,
	PersistentPreRun: func(_ *cobra.Command, _ []string) {
		// Load a local .env if present (no-op in production).
		_ = godotenv.Load()
	},
}

// Execute runs the root command, returning any error for main to report and to
// map to a non-zero exit code. Signal handling is installed once here so every
// subcommand receives a context cancelled on SIGTERM/SIGINT via cmd.Context().
func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	return rootCmd.ExecuteContext(ctx)
}

func init() {
	rootCmd.AddCommand(serveCmd)
}

// newLogger builds a slog logger from level/format strings.
func newLogger(level, format string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(h)
}
