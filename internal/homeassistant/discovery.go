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
		body, err := json.Marshal(c.buildEntityPayload(e, device))
		if err != nil {
			return nil, fmt.Errorf("marshal discovery for %s: %w", e.Key, err)
		}
		topic := fmt.Sprintf("%s/%s/%s_%s/config", c.DiscoveryPrefix, e.Component, c.Serial, e.Key)
		msgs = append(msgs, Message{Topic: topic, Payload: body})
	}
	return msgs, nil
}

// buildEntityPayload assembles the discovery JSON for one entity. Only the `~`
// base topic is abbreviated; every other key is spelled out in full.
func (c Config) buildEntityPayload(e Entity, device map[string]any) map[string]any {
	uniq := c.Serial + "_" + e.Key
	p := map[string]any{
		"~":                     c.BaseTopic(),
		"name":                  e.Name,
		"unique_id":             uniq,
		"object_id":             uniq,
		"state_topic":           "~/state",
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

	if e.Component == BinarySensor {
		p["payload_on"] = "ON"
		p["payload_off"] = "OFF"
		if e.InvertBool {
			p["value_template"] = fmt.Sprintf("{{ 'OFF' if value_json.%s else 'ON' }}", e.Key)
		} else {
			p["value_template"] = fmt.Sprintf("{{ 'ON' if value_json.%s else 'OFF' }}", e.Key)
		}
	} else {
		p["value_template"] = fmt.Sprintf("{{ value_json.%s }}", e.Key)
	}
	return p
}
