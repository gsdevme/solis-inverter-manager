package homeassistant

import (
	"encoding/json"
	"fmt"
)

// BuildDiscovery returns one discovery config Message per entity, in the stable
// order of Entities(). Each Message is intended to be published retained (retain
// is applied by the publisher). The topic is
// <DiscoveryPrefix>/<Component>/<Serial>_<Key>/config and the payload carries the
// shared device block, the `~` base topic, the shared state/availability topics,
// the entity's classes when non-empty, and a value_template that reads
// value_json.<Key>.
func (c Config) BuildDiscovery() ([]Message, error) {
	device := map[string]any{
		"identifiers":  []string{c.Serial},
		"manufacturer": Manufacturer,
		"model":        Model,
		"name":         DeviceName,
	}
	entities := Entities()
	msgs := make([]Message, 0, len(entities))
	for _, e := range entities {
		if e.Command && !c.ControlsEnabled {
			continue
		}
		body, err := json.Marshal(c.buildEntityPayload(e, device))
		if err != nil {
			return nil, fmt.Errorf("marshal discovery for %s: %w", e.Key, err)
		}
		msgs = append(msgs, Message{Topic: c.DiscoveryTopic(e.Component, e.Key), Payload: body})
	}
	return msgs, nil
}

// DiscoveryTopic is the config topic for one component/key pair:
// <DiscoveryPrefix>/<component>/<Serial>_<key>/config.
func (c Config) DiscoveryTopic(component, key string) string {
	return fmt.Sprintf("%s/%s/%s_%s/config", c.DiscoveryPrefix, component, c.Serial, key)
}

// BuildDiscoveryRemovals returns the discovery topics of entities this version no
// longer publishes, each with an empty payload: clearing a retained discovery
// config is how Home Assistant is told to delete the entity. Publishing them is
// idempotent, so it is safe on every discovery pass, and it is not gated on
// ControlsEnabled — a stale entity must go either way.
//
// The only removal is switch.optimal_income, replaced in B2 by a select on the
// same key (docs/specs/09-schedule-controls.md, REQ-HA-15).
func (c Config) BuildDiscoveryRemovals() []Message {
	return []Message{{Topic: c.DiscoveryTopic(Switch, "optimal_income"), Payload: []byte{}}}
}

// buildEntityPayload assembles the discovery JSON for one entity. Only the `~`
// base topic is abbreviated; every other key is spelled out in full.
//
// Identity is split three ways on purpose:
//
//   - unique_id is <Serial>_<key>, so an entity keeps its registry row (and any
//     user customisation) even if the entity-id prefix is later changed.
//   - object_id and default_entity_id are <ObjectIDPrefix>_<key>, the slug Home
//     Assistant derives the entity_id from.
//
// Both id keys are emitted because current Home Assistant cores derive the
// entity_id from default_entity_id ("if we have set default_entity_id to
// sensor.test, then Home Assistant will try to assign sensor.test") and no
// longer read object_id from the payload, while older cores only understand
// object_id. Emitting both is safe: the MQTT platform discovery schemas are
// built with voluptuous REMOVE_EXTRA, so a key a core does not know is dropped
// rather than failing validation. default_entity_id carries the domain, so it
// is "<component>.<ObjectIDPrefix>_<key>" where object_id is bare.
func (c Config) buildEntityPayload(e Entity, device map[string]any) map[string]any {
	objectID := c.objectID(e.Key)
	p := map[string]any{
		"~":                     c.BaseTopic(),
		"name":                  e.Name,
		"unique_id":             c.Serial + "_" + e.Key,
		"object_id":             objectID,
		"default_entity_id":     e.Component + "." + objectID,
		"availability_topic":    "~/availability",
		"payload_available":     "online",
		"payload_not_available": "offline",
		"device":                device,
	}
	if e.DeviceClass != "" {
		p["device_class"] = e.DeviceClass
	}
	if e.StateClass != "" {
		p["state_class"] = e.StateClass
	}
	if e.Unit != "" {
		p["unit_of_measurement"] = e.Unit
	}
	if e.Category != "" {
		p["entity_category"] = e.Category
	}
	if e.Command {
		p["command_topic"] = "~/" + e.Key + "/set"
	}

	switch e.Component {
	case BinarySensor:
		p["state_topic"] = "~/state"
		p["payload_on"] = "ON"
		p["payload_off"] = "OFF"
		if e.InvertBool {
			p["value_template"] = fmt.Sprintf("{{ 'OFF' if value_json.%s else 'ON' }}", e.Key)
		} else {
			p["value_template"] = fmt.Sprintf("{{ 'ON' if value_json.%s else 'OFF' }}", e.Key)
		}
	// Unused by the current Solis catalogue, which publishes no switch entity;
	// kept for parity with the shape.
	case Switch:
		p["state_topic"] = "~/state"
		p["payload_on"] = e.PayloadOn
		p["payload_off"] = e.PayloadOff
		p["state_on"] = e.StateOn
		p["state_off"] = e.StateOff
		p["value_template"] = fmt.Sprintf("{{ value_json.%s }}", e.Key)
	case Select:
		p["state_topic"] = "~/state"
		p["options"] = e.Options
		p["value_template"] = fmt.Sprintf("{{ value_json.%s }}", e.Key)
	case Number:
		p["state_topic"] = "~/state"
		p["min"] = e.Min
		p["max"] = e.Max
		p["step"] = e.Step
		p["mode"] = e.Mode
		p["value_template"] = fmt.Sprintf("{{ value_json.%s }}", e.Key)
	case Button:
		// A button is stateless: no state_topic, no value_template.
		p["payload_press"] = e.PayloadPress
	default:
		p["state_topic"] = "~/state"
		p["value_template"] = fmt.Sprintf("{{ value_json.%s }}", e.Key)
	}
	return p
}
