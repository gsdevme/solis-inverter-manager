package mqtt

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sync"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
)

// keepAliveSeconds is the MQTT keepalive interval. The Will delay interval is
// derived from it (2×) to mirror the autopaho defaults.
const keepAliveSeconds = 20

// Options configure the MQTT connection.
type Options struct {
	BrokerURL         string
	Username          string
	Password          string
	ClientID          string
	AvailabilityTopic string // LWT topic; retained "offline" on unexpected loss
	Logger            *slog.Logger

	// OnConnectionUp is invoked (in a goroutine) each time the connection is
	// (re)established, so callers can (re)publish availability + discovery. It
	// may block without stalling the connection manager.
	OnConnectionUp func(ctx context.Context)
}

// Client wraps an autopaho ConnectionManager and provides QoS-1 publish plus a
// retained "offline" Last Will on the availability topic. It is the MQTT5
// transport backing publisher.Publisher.
type Client struct {
	cm     *autopaho.ConnectionManager
	logger *slog.Logger

	mu   sync.Mutex
	onUp func(ctx context.Context)
}

// Connect parses the broker URL, builds the autopaho configuration (with LWT)
// and blocks until the initial connection is established.
func Connect(ctx context.Context, opts Options) (*Client, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	u, err := url.Parse(opts.BrokerURL)
	if err != nil {
		return nil, fmt.Errorf("parse MQTT broker URL: %w", err)
	}

	c := &Client{logger: opts.Logger, onUp: opts.OnConnectionUp}
	cfg := c.buildClientConfig(u, opts)

	cm, err := autopaho.NewConnection(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("mqtt new connection: %w", err)
	}
	c.cm = cm
	if err := cm.AwaitConnection(ctx); err != nil {
		return nil, fmt.Errorf("mqtt await connection: %w", err)
	}
	return c, nil
}

// buildClientConfig assembles the autopaho ClientConfig for the parsed broker
// URL. It performs no I/O so the LWT and connection settings are unit-testable.
// The OnConnectionUp callback fires the stored hook in a goroutine so a slow
// republish never blocks the connection manager.
func (c *Client) buildClientConfig(u *url.URL, opts Options) autopaho.ClientConfig {
	cfg := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{u},
		KeepAlive:                     keepAliveSeconds,
		CleanStartOnInitialConnection: false,
		ConnectUsername:               opts.Username,
		ConnectPassword:               []byte(opts.Password),
		OnConnectError: func(err error) {
			opts.Logger.Warn("mqtt connection attempt failed", "err", err)
		},
		OnConnectionUp: func(_ *autopaho.ConnectionManager, _ *paho.Connack) {
			opts.Logger.Info("mqtt connected")
			c.fireOnUp(context.Background())
		},
		ClientConfig: paho.ClientConfig{ClientID: opts.ClientID},
	}

	if opts.AvailabilityTopic != "" {
		willDelayInterval := uint32(2 * keepAliveSeconds)
		cfg.WillMessage = &paho.WillMessage{
			Topic:   opts.AvailabilityTopic,
			Payload: []byte("offline"),
			QoS:     1,
			Retain:  true,
		}
		cfg.WillProperties = &paho.WillProperties{WillDelayInterval: &willDelayInterval}
	}

	return cfg
}

// SetOnConnectionUp registers or replaces the callback invoked (in a goroutine)
// each time the connection is (re)established — used to republish availability +
// discovery after a broker restart. Safe to call after Connect.
func (c *Client) SetOnConnectionUp(fn func(ctx context.Context)) {
	c.mu.Lock()
	c.onUp = fn
	c.mu.Unlock()
}

// fireOnUp invokes the current OnConnectionUp hook in a goroutine, so it never
// blocks the paho callback.
func (c *Client) fireOnUp(ctx context.Context) {
	c.mu.Lock()
	fn := c.onUp
	c.mu.Unlock()
	if fn != nil {
		go fn(ctx)
	}
}

// Publish sends a message at QoS 1 with the given retain flag.
func (c *Client) Publish(ctx context.Context, topic string, payload []byte, retain bool) error {
	_, err := c.cm.Publish(ctx, &paho.Publish{
		QoS:     1,
		Topic:   topic,
		Payload: payload,
		Retain:  retain,
	})
	if err != nil {
		return fmt.Errorf("mqtt publish %s: %w", topic, err)
	}
	return nil
}

// Disconnect closes the connection cleanly, which suppresses the Last Will.
func (c *Client) Disconnect(ctx context.Context) error {
	return c.cm.Disconnect(ctx)
}
