// Package config binds and validates the service configuration from environment
// variables. It fails fast (aggregating problems with errors.Join) and redacts
// secrets from every log surface. See docs/specs/05-config.md.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gsdevme/solis-inverter-manager/internal/schedule"
)

// MinPollInterval is the floor for POLL_INTERVAL, protecting the inverter's
// datalogger from excessive polling.
const MinPollInterval = 5 * time.Second

// defaultToUWindow is the default TOU_WINDOW: 23:30-05:30 local time.
const defaultToUWindow = "23:30-05:30"

// redacted is the placeholder printed in place of any secret value.
const redacted = "REDACTED"

// Config is the fully-parsed, validated service configuration.
//
// Secrets (InverterSerial, MQTTPassword) are never rendered in String() or
// LogValue(); pass a *Config straight to slog and the redaction is applied
// automatically.
type Config struct {
	// Mode selects the data source: "live" talks to the real inverter via the
	// sidecar; "mock" runs the pipeline against canned data (no inverter/MQTT
	// values required).
	Mode string

	// Inverter / sidecar
	InverterIP            string
	InverterSerial        string // secret: datalogger serial, never logged
	InverterPort          int
	InverterSocketTimeout time.Duration
	SidecarURL            string // localhost HTTP endpoint of the Python sidecar

	// Polling & scheduling
	PollInterval     time.Duration
	PollMaxRetries   int
	FailureThreshold int

	// RTC auto-sync. Opt-in (default off) periodic clock correction folded into the
	// poll: when enabled and the inverter clock drifts by more than
	// RTCDriftThreshold, the scheduler runs the guarded 43000–43005 write. Off by
	// default so the flash-wear guardrail is never touched without an explicit opt-in.
	RTCSyncEnabled    bool
	RTCDriftThreshold time.Duration

	// MQTT / HA
	MQTTBrokerURL     string
	MQTTUsername      string
	MQTTPassword      string // secret: never logged
	MQTTClientID      string
	MQTTTopicPrefix   string
	HADiscoveryPrefix string

	// Controls
	// ControlsEnabled gates the writable HA command entities and the command
	// subscription. Defaults to true (absent/empty CONTROLS_ENABLED enables
	// controls); set CONTROLS_ENABLED=false to run read-only.
	ControlsEnabled bool

	// TOUWindow is the raw TOU_WINDOW setting, "HH:MM-HH:MM" local time,
	// asserted into timed slots 1-2 by the schedule reconciler. Left unset
	// it defaults to "23:30-05:30"; set to the empty string it disables ToU
	// assertion entirely (slots 1-2 are left untouched). See
	// schedule.ParseToUWindow for parsing.
	TOUWindow string

	// Health & logging
	HealthAddr string
	LogLevel   string
	LogFormat  string
}

// Load reads configuration from the environment, applies defaults and validates.
func Load() (*Config, error) {
	c := &Config{
		InverterIP:        os.Getenv("INVERTER_IP"),
		InverterSerial:    os.Getenv("INVERTER_SERIAL"),
		SidecarURL:        getEnv("SIDECAR_URL", "http://127.0.0.1:8081"),
		MQTTBrokerURL:     os.Getenv("MQTT_BROKER_URL"),
		MQTTUsername:      os.Getenv("MQTT_USERNAME"),
		MQTTPassword:      os.Getenv("MQTT_PASSWORD"),
		MQTTClientID:      getEnv("MQTT_CLIENT_ID", "solis-inverter-manager"),
		MQTTTopicPrefix:   getEnv("MQTT_TOPIC_PREFIX", "solis"),
		HADiscoveryPrefix: getEnv("HA_DISCOVERY_PREFIX", "homeassistant"),
		HealthAddr:        getEnv("HEALTH_ADDR", ":8080"),
		LogLevel:          strings.ToLower(getEnv("LOG_LEVEL", "info")),
		LogFormat:         strings.ToLower(getEnv("LOG_FORMAT", "json")),
	}

	var errs []error

	c.Mode = strings.ToLower(getEnv("MODE", "live"))
	switch c.Mode {
	case "live":
		// Live requires the inverter identity and an MQTT broker; the sidecar URL
		// has a localhost default. Mock mode drops all of these.
		if c.InverterIP == "" {
			errs = append(errs, errors.New("INVERTER_IP is required when MODE=live"))
		}
		if c.InverterSerial == "" {
			errs = append(errs, errors.New("INVERTER_SERIAL is required when MODE=live"))
		}
		if c.MQTTBrokerURL == "" {
			errs = append(errs, errors.New("MQTT_BROKER_URL is required when MODE=live"))
		}
	case "mock":
		// No live/MQTT values required in mock mode.
	default:
		errs = append(errs, fmt.Errorf("MODE must be live or mock, got %q", c.Mode))
	}

	if c.MQTTBrokerURL != "" {
		if _, err := url.Parse(c.MQTTBrokerURL); err != nil {
			errs = append(errs, fmt.Errorf("MQTT_BROKER_URL is invalid: %w", err))
		}
	}
	if _, err := url.Parse(c.SidecarURL); err != nil {
		errs = append(errs, fmt.Errorf("SIDECAR_URL is invalid: %w", err))
	}

	c.InverterPort = getInt("INVERTER_PORT", 8899)
	if c.InverterPort < 1 || c.InverterPort > 65535 {
		errs = append(errs, fmt.Errorf("INVERTER_PORT %d is out of range 1-65535", c.InverterPort))
	}

	socketTimeout, err := parseDurationOrSeconds("INVERTER_SOCKET_TIMEOUT", 10*time.Second)
	if err != nil {
		errs = append(errs, err)
	} else if socketTimeout <= 0 {
		errs = append(errs, errors.New("INVERTER_SOCKET_TIMEOUT must be > 0"))
	}
	c.InverterSocketTimeout = socketTimeout

	interval, err := parseDuration("POLL_INTERVAL", 60*time.Second)
	if err != nil {
		errs = append(errs, err)
	} else if interval < MinPollInterval {
		errs = append(errs, fmt.Errorf("POLL_INTERVAL %s is below the %s floor", interval, MinPollInterval))
	}
	c.PollInterval = interval

	c.PollMaxRetries = getInt("POLL_MAX_RETRIES", 3)
	if c.PollMaxRetries < 0 {
		errs = append(errs, errors.New("POLL_MAX_RETRIES must be >= 0"))
	}
	c.FailureThreshold = getInt("FAILURE_THRESHOLD", 3)
	if c.FailureThreshold < 1 {
		errs = append(errs, errors.New("FAILURE_THRESHOLD must be >= 1"))
	}

	controlsEnabled, err := parseBool("CONTROLS_ENABLED", true)
	if err != nil {
		errs = append(errs, err)
	}
	c.ControlsEnabled = controlsEnabled

	// TOU_WINDOW must distinguish "unset" (apply the default) from "set to the
	// empty string" (disable ToU assertion), so it cannot use getEnv, which
	// treats both the same way.
	c.TOUWindow = defaultToUWindow
	if v, ok := os.LookupEnv("TOU_WINDOW"); ok {
		c.TOUWindow = v
	}
	if _, _, err := schedule.ParseToUWindow(c.TOUWindow); err != nil {
		errs = append(errs, fmt.Errorf("TOU_WINDOW: %w", err))
	}

	rtcSyncEnabled, err := parseBool("RTC_SYNC_ENABLED", false)
	if err != nil {
		errs = append(errs, err)
	}
	c.RTCSyncEnabled = rtcSyncEnabled

	rtcDrift, err := parseDuration("RTC_DRIFT_THRESHOLD", 60*time.Second)
	if err != nil {
		errs = append(errs, err)
	} else if rtcDrift <= 0 {
		errs = append(errs, errors.New("RTC_DRIFT_THRESHOLD must be > 0"))
	}
	c.RTCDriftThreshold = rtcDrift

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return c, nil
}

// String renders the config with secrets redacted (safe to log). The inverter
// serial and MQTT password are replaced with a placeholder — they are never
// printed.
func (c *Config) String() string {
	serial := ""
	if c.InverterSerial != "" {
		serial = redacted
	}
	password := ""
	if c.MQTTPassword != "" {
		password = redacted
	}
	return fmt.Sprintf("Config{mode=%s inverterIP=%s inverterSerial=%s inverterPort=%d "+
		"socketTimeout=%s sidecar=%s poll=%s maxRetries=%d failThreshold=%d "+
		"rtcSyncEnabled=%t rtcDriftThreshold=%s controlsEnabled=%t touWindow=%s "+
		"broker=%s user=%s password=%s clientID=%s topicPrefix=%s haPrefix=%s health=%s log=%s/%s}",
		c.Mode, c.InverterIP, serial, c.InverterPort, c.InverterSocketTimeout, c.SidecarURL,
		c.PollInterval, c.PollMaxRetries, c.FailureThreshold,
		c.RTCSyncEnabled, c.RTCDriftThreshold, c.ControlsEnabled, c.TOUWindow, c.MQTTBrokerURL,
		c.MQTTUsername, password, c.MQTTClientID, c.MQTTTopicPrefix, c.HADiscoveryPrefix,
		c.HealthAddr, c.LogLevel, c.LogFormat)
}

// LogValue implements slog.LogValuer so `logger.Info("...", "config", cfg)` emits
// a structured, secret-free representation. Secrets are replaced with a redaction
// placeholder (present-but-hidden) rather than omitted, so their presence is still
// visible in logs.
func (c *Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("mode", c.Mode),
		slog.String("inverter_ip", c.InverterIP),
		slog.String("inverter_serial", redactSecret(c.InverterSerial)),
		slog.Int("inverter_port", c.InverterPort),
		slog.Duration("socket_timeout", c.InverterSocketTimeout),
		slog.String("sidecar_url", c.SidecarURL),
		slog.Duration("poll_interval", c.PollInterval),
		slog.Int("poll_max_retries", c.PollMaxRetries),
		slog.Int("failure_threshold", c.FailureThreshold),
		slog.Bool("rtc_sync_enabled", c.RTCSyncEnabled),
		slog.Duration("rtc_drift_threshold", c.RTCDriftThreshold),
		slog.Bool("controls_enabled", c.ControlsEnabled),
		slog.String("tou_window", c.TOUWindow),
		slog.String("mqtt_broker_url", c.MQTTBrokerURL),
		slog.String("mqtt_username", c.MQTTUsername),
		slog.String("mqtt_password", redactSecret(c.MQTTPassword)),
		slog.String("mqtt_client_id", c.MQTTClientID),
		slog.String("mqtt_topic_prefix", c.MQTTTopicPrefix),
		slog.String("ha_discovery_prefix", c.HADiscoveryPrefix),
		slog.String("health_addr", c.HealthAddr),
		slog.String("log_level", c.LogLevel),
		slog.String("log_format", c.LogFormat),
	)
}

// redactSecret returns the redaction placeholder when a secret is set, or the
// empty string when it is unset, so logs distinguish "set but hidden" from "unset"
// without ever revealing the value.
func redactSecret(v string) string {
	if v == "" {
		return ""
	}
	return redacted
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// parseBool parses a boolean env var (via strconv.ParseBool, so 1/t/true/0/f/
// false are all accepted, case-insensitively). An empty/unset value returns the
// default; an unparseable value is a validation error.
func parseBool(key string, def bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return def, fmt.Errorf("%s is not a valid boolean: %w", key, err)
	}
	return b, nil
}

func parseDuration(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s is not a valid duration: %w", key, err)
	}
	return d, nil
}

// parseDurationOrSeconds parses a Go duration (e.g. "10s") and, for backwards
// compatibility with the sidecar/legacy config, also accepts a bare integer as a
// number of seconds (e.g. "10").
func parseDurationOrSeconds(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s is not a valid duration or integer seconds: %w", key, err)
	}
	return d, nil
}
