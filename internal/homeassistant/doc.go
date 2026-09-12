// Package homeassistant is a pure payload builder for Home Assistant MQTT
// autodiscovery.
//
// It maps a decoded inverter.Telemetry to the Solis entity catalogue (Entities)
// and builds two kinds of payload:
//
//   - Config.BuildDiscovery returns one discovery config per entity, each with a
//     shared device block identifying the inverter, the `~` base topic, and a
//     value_template that reads the shared state document.
//   - Config.BuildState marshals the flat state document whose JSON tags equal the
//     entity keys, so every entity's value_template resolves.
//
// The package performs no I/O and never reads the wall clock: clock drift is
// passed into BuildState. Callers (the publisher) apply retain/QoS when
// publishing. See docs/specs/03-mqtt-ha-discovery.md and the
// home-assistant-mqtt-discovery skill, the source of truth for these rules.
package homeassistant
