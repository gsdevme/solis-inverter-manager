// Package mqtt provides the MQTT5 client that publishes to the broker and backs the
// publisher.Publisher interface.
//
// It will configure the Last Will (retained "offline" on the service availability
// topic), auto-reconnect, and republish availability + discovery + last state on
// every (re)connection. See docs/specs/03-mqtt-ha-discovery.md.
//
// TODO(phase-4): implement Connect, Publish (retained/QoS1), Disconnect and the
// on-connection-up hook. The MQTT dependency is added to go.mod in that phase.
package mqtt
