// Package mqtt provides the autopaho-backed MQTT5 client that publishes to the
// broker and backs the publisher.Publisher interface.
//
// The client configures a Last Will (retained "offline" on the service
// availability topic), auto-reconnects, and exposes an on-connection-up hook so
// callers can republish availability + discovery + last state on every
// (re)connection. Publishes are QoS 1; a clean Disconnect suppresses the Will.
// See docs/specs/03-mqtt-ha-discovery.md.
package mqtt
