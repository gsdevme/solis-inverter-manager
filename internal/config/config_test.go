package config

import (
	"strings"
	"testing"
	"time"
)

// setEnv sets the minimum required vars for MODE=live plus any overrides.
func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	base := map[string]string{
		"MODE":            "live",
		"INVERTER_IP":     "192.168.1.50",
		"INVERTER_SERIAL": "1234567890",
		"MQTT_BROKER_URL": "mqtt://localhost:1883",
	}
	for k, v := range base {
		if _, ok := kv[k]; !ok {
			t.Setenv(k, v)
		}
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestLoadDefaults(t *testing.T) {
	setEnv(t, nil)
	c, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.PollInterval.Seconds() != 60 {
		t.Errorf("poll interval = %s, want 60s", c.PollInterval)
	}
	if c.FailureThreshold != 3 {
		t.Errorf("failure threshold = %d, want 3", c.FailureThreshold)
	}
	if c.PollMaxRetries != 3 {
		t.Errorf("poll max retries = %d, want 3", c.PollMaxRetries)
	}
	if c.RTCSyncEnabled {
		t.Errorf("rtc sync enabled = true, want false by default")
	}
	if c.RTCDriftThreshold != 60*time.Second {
		t.Errorf("rtc drift threshold = %s, want 60s", c.RTCDriftThreshold)
	}
	if c.InverterPort != 8899 {
		t.Errorf("inverter port = %d, want 8899", c.InverterPort)
	}
	if c.MQTTTopicPrefix != "solis" {
		t.Errorf("topic prefix = %q, want solis", c.MQTTTopicPrefix)
	}
	if c.HADiscoveryPrefix != "homeassistant" {
		t.Errorf("ha prefix = %q, want homeassistant", c.HADiscoveryPrefix)
	}
	if c.HAObjectIDPrefix != "solis_inverter" {
		t.Errorf("ha object id prefix = %q, want solis_inverter", c.HAObjectIDPrefix)
	}
	if c.Mode != "live" {
		t.Errorf("mode = %q, want live", c.Mode)
	}
}

func TestMissingRequiredLive(t *testing.T) {
	t.Setenv("MODE", "live")
	t.Setenv("INVERTER_IP", "")
	t.Setenv("INVERTER_SERIAL", "")
	t.Setenv("MQTT_BROKER_URL", "")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing required vars")
	}
	for _, want := range []string{"INVERTER_IP", "INVERTER_SERIAL", "MQTT_BROKER_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %s: %v", want, err)
		}
	}
}

func TestModeMockDropsLiveRequirements(t *testing.T) {
	// Mock mode must load cleanly with no inverter/MQTT values.
	t.Setenv("MODE", "mock")
	t.Setenv("INVERTER_IP", "")
	t.Setenv("INVERTER_SERIAL", "")
	t.Setenv("MQTT_BROKER_URL", "")
	c, err := Load()
	if err != nil {
		t.Fatalf("mock load: %v", err)
	}
	if c.Mode != "mock" {
		t.Errorf("mode = %q, want mock", c.Mode)
	}
}

func TestModeBogus(t *testing.T) {
	setEnv(t, map[string]string{"MODE": "bogus"})
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "MODE") {
		t.Fatalf("expected MODE validation error, got %v", err)
	}
}

func TestPollFloor(t *testing.T) {
	setEnv(t, map[string]string{"POLL_INTERVAL": "1s"})
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "floor") {
		t.Fatalf("expected poll floor error, got %v", err)
	}
}

func TestBadFailureThreshold(t *testing.T) {
	setEnv(t, map[string]string{"FAILURE_THRESHOLD": "0"})
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "FAILURE_THRESHOLD") {
		t.Fatalf("expected FAILURE_THRESHOLD error, got %v", err)
	}
}

func TestRTCSyncKnobs(t *testing.T) {
	setEnv(t, map[string]string{"RTC_SYNC_ENABLED": "true", "RTC_DRIFT_THRESHOLD": "30s"})
	c, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !c.RTCSyncEnabled {
		t.Errorf("rtc sync enabled = false, want true")
	}
	if c.RTCDriftThreshold != 30*time.Second {
		t.Errorf("rtc drift threshold = %s, want 30s", c.RTCDriftThreshold)
	}
}

func TestBadRTCDriftThreshold(t *testing.T) {
	setEnv(t, map[string]string{"RTC_DRIFT_THRESHOLD": "0s"})
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "RTC_DRIFT_THRESHOLD") {
		t.Fatalf("expected RTC_DRIFT_THRESHOLD error, got %v", err)
	}
}

func TestTOUWindowDefault(t *testing.T) {
	setEnv(t, nil)
	c, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.TOUWindow != "23:30-05:30" {
		t.Errorf("tou window = %q, want 23:30-05:30", c.TOUWindow)
	}
}

func TestTOUWindowEmptyDisablesAssertion(t *testing.T) {
	setEnv(t, map[string]string{"TOU_WINDOW": ""})
	c, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.TOUWindow != "" {
		t.Errorf("tou window = %q, want empty", c.TOUWindow)
	}
}

func TestTOUWindowInvalid(t *testing.T) {
	setEnv(t, map[string]string{"TOU_WINDOW": "25:00-05:30"})
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TOU_WINDOW") {
		t.Fatalf("expected TOU_WINDOW validation error, got %v", err)
	}
}

func TestHAObjectIDPrefixOverride(t *testing.T) {
	setEnv(t, map[string]string{"HA_OBJECT_ID_PREFIX": "solis_2"})
	c, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.HAObjectIDPrefix != "solis_2" {
		t.Errorf("ha object id prefix = %q, want solis_2", c.HAObjectIDPrefix)
	}
}

func TestHAObjectIDPrefixInvalid(t *testing.T) {
	for _, bad := range []string{"Solis Inverter", "solis-inverter", "solis.inverter", "sensor/solis"} {
		t.Run(bad, func(t *testing.T) {
			setEnv(t, map[string]string{"HA_OBJECT_ID_PREFIX": bad})
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), "HA_OBJECT_ID_PREFIX") {
				t.Fatalf("expected HA_OBJECT_ID_PREFIX validation error for %q, got %v", bad, err)
			}
		})
	}
}

func TestSidecarStartupTimeout(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		want    time.Duration
		wantErr bool
	}{
		{name: "default", value: "", want: 30 * time.Second},
		{name: "explicit", value: "90s", want: 90 * time.Second},
		{name: "zero disables the wait", value: "0s", want: 0},
		{name: "negative rejected", value: "-1s", wantErr: true},
		{name: "unparsable rejected", value: "soon", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, map[string]string{"SIDECAR_STARTUP_TIMEOUT": tc.value})
			c, err := Load()
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "SIDECAR_STARTUP_TIMEOUT") {
					t.Fatalf("expected SIDECAR_STARTUP_TIMEOUT validation error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if c.SidecarStartupTimeout != tc.want {
				t.Errorf("sidecar startup timeout = %s, want %s", c.SidecarStartupTimeout, tc.want)
			}
		})
	}
}

func TestRedaction(t *testing.T) {
	setEnv(t, map[string]string{
		"INVERTER_SERIAL": "SN-super-secret",
		"MQTT_PASSWORD":   "hunter2",
	})
	c, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// String() must not leak either secret.
	s := c.String()
	for _, leak := range []string{"SN-super-secret", "hunter2"} {
		if strings.Contains(s, leak) {
			t.Errorf("String() leaked %q: %s", leak, s)
		}
	}
	// LogValue() must not leak either secret either.
	lv := c.LogValue().String()
	for _, leak := range []string{"SN-super-secret", "hunter2"} {
		if strings.Contains(lv, leak) {
			t.Errorf("LogValue() leaked %q: %s", leak, lv)
		}
	}
}
