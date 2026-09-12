package mqtt

import (
	"context"
	"net/url"
	"testing"

	"github.com/eclipse/paho.golang/paho"
)

func TestBuildClientConfigSetsLastWill(t *testing.T) {
	u, err := url.Parse("mqtt://broker.example:1883")
	if err != nil {
		t.Fatalf("parse test URL: %v", err)
	}

	c := &Client{}
	cfg := c.buildClientConfig(u, Options{AvailabilityTopic: "solis/availability"})

	if cfg.WillMessage == nil {
		t.Fatal("WillMessage is nil, want configured LWT")
	}
	if got := cfg.WillMessage.Topic; got != "solis/availability" {
		t.Errorf("Will topic = %q, want %q", got, "solis/availability")
	}
	if got := string(cfg.WillMessage.Payload); got != "offline" {
		t.Errorf("Will payload = %q, want %q", got, "offline")
	}
	if got := cfg.WillMessage.QoS; got != 1 {
		t.Errorf("Will QoS = %d, want 1", got)
	}
	if !cfg.WillMessage.Retain {
		t.Error("Will Retain = false, want true")
	}
	if cfg.WillProperties == nil || cfg.WillProperties.WillDelayInterval == nil {
		t.Fatal("WillProperties.WillDelayInterval not set")
	}
	if got := *cfg.WillProperties.WillDelayInterval; got != 2*keepAliveSeconds {
		t.Errorf("WillDelayInterval = %d, want %d", got, 2*keepAliveSeconds)
	}
}

func TestBuildClientConfigNoWillWhenTopicEmpty(t *testing.T) {
	u, _ := url.Parse("mqtt://broker.example:1883")
	cfg := (&Client{}).buildClientConfig(u, Options{})

	if cfg.WillMessage != nil {
		t.Errorf("WillMessage = %+v, want nil when AvailabilityTopic empty", cfg.WillMessage)
	}
	if cfg.WillProperties != nil {
		t.Errorf("WillProperties = %+v, want nil when AvailabilityTopic empty", cfg.WillProperties)
	}
}

func TestBuildClientConfigSetsServerURLAndCredentials(t *testing.T) {
	u, err := url.Parse("tls://broker.example:8883")
	if err != nil {
		t.Fatalf("parse test URL: %v", err)
	}

	cfg := (&Client{}).buildClientConfig(u, Options{
		Username: "user",
		Password: "secret",
		ClientID: "solis-1",
	})

	if len(cfg.ServerUrls) != 1 {
		t.Fatalf("ServerUrls length = %d, want 1", len(cfg.ServerUrls))
	}
	if got := cfg.ServerUrls[0].Scheme; got != "tls" {
		t.Errorf("ServerUrls[0].Scheme = %q, want %q", got, "tls")
	}
	if got := cfg.ServerUrls[0].Host; got != "broker.example:8883" {
		t.Errorf("ServerUrls[0].Host = %q, want %q", got, "broker.example:8883")
	}
	if cfg.KeepAlive != keepAliveSeconds {
		t.Errorf("KeepAlive = %d, want %d", cfg.KeepAlive, keepAliveSeconds)
	}
	if cfg.CleanStartOnInitialConnection {
		t.Error("CleanStartOnInitialConnection = true, want false")
	}
	if cfg.ConnectUsername != "user" {
		t.Errorf("ConnectUsername = %q, want %q", cfg.ConnectUsername, "user")
	}
	if string(cfg.ConnectPassword) != "secret" {
		t.Errorf("ConnectPassword = %q, want %q", string(cfg.ConnectPassword), "secret")
	}
	if cfg.ClientID != "solis-1" {
		t.Errorf("ClientID = %q, want %q", cfg.ClientID, "solis-1")
	}
}

func TestBuildClientConfigDeliversInboundPublishToHandler(t *testing.T) {
	u, err := url.Parse("mqtt://broker.example:1883")
	if err != nil {
		t.Fatalf("parse test URL: %v", err)
	}

	c := &Client{}
	cfg := c.buildClientConfig(u, Options{})

	if len(cfg.OnPublishReceived) != 1 {
		t.Fatalf("OnPublishReceived length = %d, want 1", len(cfg.OnPublishReceived))
	}

	var gotTopic string
	var gotPayload []byte
	c.SetOnMessage(func(_ context.Context, topic string, payload []byte) {
		gotTopic = topic
		gotPayload = payload
	})

	handled, err := cfg.OnPublishReceived[0](paho.PublishReceived{
		Packet: &paho.Publish{Topic: "solis/cmd/charge", Payload: []byte("10")},
	})
	if err != nil {
		t.Fatalf("callback returned err = %v, want nil", err)
	}
	if !handled {
		t.Error("callback returned handled = false, want true")
	}
	if gotTopic != "solis/cmd/charge" {
		t.Errorf("handler topic = %q, want %q", gotTopic, "solis/cmd/charge")
	}
	if string(gotPayload) != "10" {
		t.Errorf("handler payload = %q, want %q", string(gotPayload), "10")
	}
}

func TestOnPublishReceivedWithNilHandlerDoesNotPanic(t *testing.T) {
	u, _ := url.Parse("mqtt://broker.example:1883")
	cfg := (&Client{}).buildClientConfig(u, Options{})

	handled, err := cfg.OnPublishReceived[0](paho.PublishReceived{
		Packet: &paho.Publish{Topic: "solis/cmd/charge", Payload: []byte("10")},
	})
	if err != nil {
		t.Fatalf("callback returned err = %v, want nil", err)
	}
	if !handled {
		t.Error("callback returned handled = false, want true")
	}
}

// Subscribe requires a live autopaho ConnectionManager (c.cm), which the
// I/O-free harness deliberately never constructs; standing up a fake broker
// just to reach it would add flakiness for no coverage of the seam's logic.
// A compile-time assertion that the method exists with the exact signature
// Task 5 calls is sufficient here.
var _ func(context.Context, string) error = (*Client)(nil).Subscribe

func TestConnectRejectsMalformedBrokerURL(t *testing.T) {
	_, err := Connect(context.Background(), Options{BrokerURL: "://no-scheme"})
	if err == nil {
		t.Fatal("Connect(malformed URL) = nil error, want error")
	}
}
