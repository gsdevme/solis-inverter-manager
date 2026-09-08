// Package homeassistant is a pure payload builder for Home Assistant MQTT
// autodiscovery.
//
// It will build the retained discovery configs and state documents for the Solis
// entities (battery SOC/SOH, power flows, energy totals, work-mode, and the
// writable setpoint controls) with a shared device block identifying the inverter.
// See docs/specs/03-mqtt-ha-discovery.md.
//
// TODO(phase-4): implement Config, topic helpers, BuildDiscovery/BuildState and the
// entity catalogue (sensors + writable number/select controls).
package homeassistant
