// Package publisher glues the homeassistant payload builder to the mqtt transport
// and provides the reusable read→decode poll unit.
//
// A Service publishes the retained Home Assistant discovery configs (once, on
// startup, alongside the empty configs that remove retired entities), the
// retained availability payload (online/offline), and the retained per-poll JSON
// state document. It depends on the Publisher seam rather than a concrete MQTT
// client so tests can substitute a RecordingPublisher.
//
// Collect is the read→decode unit the Phase 6 scheduler calls unchanged: it reads
// the two input register blocks through a RegisterReader and returns decoded
// inverter.Telemetry. See docs/specs/03-mqtt-ha-discovery.md.
package publisher
