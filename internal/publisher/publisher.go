package publisher

import (
	"context"
	"fmt"
	"time"

	"github.com/gsdevme/solis-inverter-manager/internal/homeassistant"
	"github.com/gsdevme/solis-inverter-manager/internal/inverter"
)

// Publisher is the MQTT publish seam. Discovery, availability and state are all
// published retained; the transport fixes QoS at 1, so only the retain flag
// varies here. *mqtt.Client satisfies this in production; RecordingPublisher
// satisfies it in tests.
type Publisher interface {
	Publish(ctx context.Context, topic string, payload []byte, retain bool) error
}

// Service publishes Home Assistant discovery, availability and state for one
// inverter. It depends on the Publisher interface, not a concrete client, so the
// transport is swappable and the payload building is testable without a broker.
type Service struct {
	pub Publisher
	cfg homeassistant.Config
}

// New builds a Service from a Publisher and the discovery config.
func New(pub Publisher, cfg homeassistant.Config) *Service {
	return &Service{pub: pub, cfg: cfg}
}

// AvailabilityTopic exposes the topic used for the LWT and explicit availability.
func (s *Service) AvailabilityTopic() string { return s.cfg.AvailabilityTopic() }

// PublishDiscovery publishes every entity's discovery config, each retained so
// Home Assistant rebuilds its entities after a broker restart.
func (s *Service) PublishDiscovery(ctx context.Context) error {
	msgs, err := s.cfg.BuildDiscovery()
	if err != nil {
		return fmt.Errorf("build discovery: %w", err)
	}
	for _, m := range msgs {
		if err := s.pub.Publish(ctx, m.Topic, m.Payload, true); err != nil {
			return fmt.Errorf("publish discovery %s: %w", m.Topic, err)
		}
	}
	return nil
}

// PublishDiscoveryRemovals clears the retained discovery config of every entity
// this version no longer publishes, which is how Home Assistant is told to
// delete it. Each removal is an empty retained payload and is idempotent, so it
// is safe to send on every startup.
func (s *Service) PublishDiscoveryRemovals(ctx context.Context) error {
	for _, m := range s.cfg.BuildDiscoveryRemovals() {
		if err := s.pub.Publish(ctx, m.Topic, m.Payload, true); err != nil {
			return fmt.Errorf("publish discovery removal %s: %w", m.Topic, err)
		}
	}
	return nil
}

// PublishAvailability publishes "online" or "offline" retained to the
// availability topic.
func (s *Service) PublishAvailability(ctx context.Context, online bool) error {
	payload := "offline"
	if online {
		payload = "online"
	}
	if err := s.pub.Publish(ctx, s.cfg.AvailabilityTopic(), []byte(payload), true); err != nil {
		return fmt.Errorf("publish availability: %w", err)
	}
	return nil
}

// PublishState publishes the retained JSON state document for a decoded reading
// and the current writable-control setpoints. The clock drift is computed here
// against the wall clock; Phase 6 will inject a clock, but time.Now() is
// acceptable for now. A retained state doc restores the last values to Home
// Assistant on reconnect.
func (s *Service) PublishState(ctx context.Context, t inverter.Telemetry, sp homeassistant.Setpoints) error {
	drift := inverter.Drift(t.Time, time.Now())
	msg, err := s.cfg.BuildState(t, drift, sp)
	if err != nil {
		return fmt.Errorf("build state: %w", err)
	}
	if err := s.pub.Publish(ctx, msg.Topic, msg.Payload, true); err != nil {
		return fmt.Errorf("publish state: %w", err)
	}
	return nil
}
