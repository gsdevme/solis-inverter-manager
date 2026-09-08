// Package publisher glues the homeassistant payload builder to the mqtt transport.
//
// It will publish discovery configs, availability and per-reading state documents,
// and expose a recording fake so tests run without a broker. See
// docs/specs/03-mqtt-ha-discovery.md.
//
// TODO(phase-4): implement the Publisher interface, the production Service, and the
// recording fake used by unit and godog tests.
package publisher
