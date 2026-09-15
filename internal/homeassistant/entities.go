package homeassistant

import "github.com/gsdevme/solis-inverter-manager/internal/schedule"

// Component types this package emits.
const (
	Sensor       = "sensor"
	BinarySensor = "binary_sensor"
	Number       = "number"
	Switch       = "switch"
	Select       = "select"
	Button       = "button"
)

// Fixed device identity for the Solis inverter. Every entity carries the same
// device block so Home Assistant groups them under one device.
const (
	Manufacturer = "Solis"
	Model        = "RHI-3.6K-48ES-5G"
	DeviceName   = "Solis Inverter"
)

// DefaultObjectIDPrefix is the entity-id prefix used when a Config leaves
// ObjectIDPrefix empty. It is also the default of HA_OBJECT_ID_PREFIX, so
// entity ids read as sensor.solis_inverter_<key> out of the box.
const DefaultObjectIDPrefix = "solis_inverter"

// Config identifies the topics and device metadata for one inverter's discovery.
// Serial is the datalogger (Solarman) serial that identifies the device in HA.
type Config struct {
	DiscoveryPrefix string // e.g. "homeassistant"
	TopicPrefix     string // e.g. "solis"
	Serial          string // datalogger serial (device identifier)
	// ObjectIDPrefix is the slug Home Assistant derives entity ids from:
	// <component>.<ObjectIDPrefix>_<key>. It is deliberately independent of
	// Serial, which stays the unique_id and device scope. Empty falls back to
	// DefaultObjectIDPrefix, so a Config can never emit a bare "_<key>".
	ObjectIDPrefix string
	// ControlsEnabled gates the writable command entities. When false (the
	// default), BuildDiscovery emits only the read-only sensors; when true it
	// also emits the Number/Select/Button controls.
	ControlsEnabled bool
}

// objectID is the entity-id slug for one key, <ObjectIDPrefix>_<key>.
func (c Config) objectID(key string) string {
	prefix := c.ObjectIDPrefix
	if prefix == "" {
		prefix = DefaultObjectIDPrefix
	}
	return prefix + "_" + key
}

// BaseTopic is the per-inverter base topic used as the `~` abbreviation.
func (c Config) BaseTopic() string { return c.TopicPrefix + "/" + c.Serial }

// StateTopic is the single retained JSON state document topic.
func (c Config) StateTopic() string { return c.BaseTopic() + "/state" }

// AvailabilityTopic is the LWT/availability topic.
func (c Config) AvailabilityTopic() string { return c.BaseTopic() + "/availability" }

// Message is a single MQTT publish (topic + payload). Discovery messages are
// intended to be published retained at QoS 1; retain is applied by the publisher.
type Message struct {
	Topic   string
	Payload []byte
}

// Entity describes a single HA entity derived from the shared JSON state topic.
// Key is both the discovery object-id suffix and the JSON field the entity reads
// (value_json.<Key>), so every entity Key must match a state-DTO JSON tag.
type Entity struct {
	Component   string // "sensor" | "binary_sensor" | "number" | "switch" | "select" | "button"
	Key         string
	Name        string
	DeviceClass string // "" if none
	StateClass  string // "" if none
	Unit        string // "" if none
	Category    string // "diagnostic" or empty
	// Precision is the suggested_display_precision HA renders the value with. It
	// mirrors the register scale (÷10 → 1, ÷100 → 2) so a whole-number reading
	// keeps its trailing zero (49.0 V, not 49 V). 0 omits the key.
	Precision int

	// Command marks a writable control (Number/Select/Button). When set,
	// BuildDiscovery emits a command_topic and the component-specific keys
	// below, and gates the entity behind Config.ControlsEnabled.
	Command bool

	// Number bounds and step, plus the HA input Mode ("box" | "slider" | "auto").
	Min, Max, Step float64
	Mode           string

	// Options lists a Select's choices, in display order. A Select's state
	// document value must always be one of them (or null for "unknown").
	Options []string

	// PayloadPress is the payload a Button publishes when pressed.
	PayloadPress string
}

// Entities returns the full, stably ordered catalogue published for the inverter.
// The order is preserved by BuildDiscovery, so discovery output is deterministic.
// Every Telemetry field maps to exactly one entry, and every Key is a JSON tag on
// the state DTO (see state.go).
func Entities() []Entity {
	return []Entity{
		// Battery.
		{Component: Sensor, Key: "battery_voltage", Name: "Battery voltage", DeviceClass: "voltage", StateClass: "measurement", Unit: "V", Precision: 1},
		{Component: Sensor, Key: "battery_current", Name: "Battery current", DeviceClass: "current", StateClass: "measurement", Unit: "A", Precision: 1},
		{Component: Sensor, Key: "battery_power", Name: "Battery power", DeviceClass: "power", StateClass: "measurement", Unit: "W"},
		// Unsigned halves of the signed battery_power above, derived in
		// BuildState so Home Assistant's Riemann-sum integrations can meter the
		// two directions separately. Not an independent decode.
		{Component: Sensor, Key: "battery_charge_power", Name: "Battery charge power", DeviceClass: "power", StateClass: "measurement", Unit: "W"},
		{Component: Sensor, Key: "battery_discharge_power", Name: "Battery discharge power", DeviceClass: "power", StateClass: "measurement", Unit: "W"},
		{Component: BinarySensor, Key: "battery_charging", Name: "Battery charging", DeviceClass: "battery_charging"},
		{Component: Sensor, Key: "battery_soc", Name: "Battery SOC", DeviceClass: "battery", StateClass: "measurement", Unit: "%"},
		{Component: Sensor, Key: "battery_soh", Name: "Battery SOH", DeviceClass: "battery", StateClass: "measurement", Unit: "%", Category: "diagnostic"},
		{Component: Sensor, Key: "bms_voltage", Name: "BMS voltage", DeviceClass: "voltage", StateClass: "measurement", Unit: "V", Category: "diagnostic", Precision: 2},
		{Component: Sensor, Key: "bms_current", Name: "BMS current", DeviceClass: "current", StateClass: "measurement", Unit: "A", Category: "diagnostic", Precision: 1},
		// Ceilings the BMS advertises, not readings: the charge limit tapers to 0
		// as the pack fills, which is what explains a slow or stalled charge.
		{Component: Sensor, Key: "bms_charge_current_limit", Name: "BMS charge current limit", DeviceClass: "current", StateClass: "measurement", Unit: "A", Category: "diagnostic", Precision: 1},
		{Component: Sensor, Key: "bms_discharge_current_limit", Name: "BMS discharge current limit", DeviceClass: "current", StateClass: "measurement", Unit: "A", Category: "diagnostic", Precision: 1},
		// The two raw BMS fault words, published alongside the per-bit sensors
		// below so a fault can be reconciled against the vendor table even if a
		// bit is mislabelled (see inverter/bmsfault.go).
		{Component: Sensor, Key: "bms_fault_1", Name: "BMS fault word 1", Category: "diagnostic"},
		{Component: Sensor, Key: "bms_fault_2", Name: "BMS fault word 2", Category: "diagnostic"},
		// One problem-class binary sensor per decoded fault bit, so a protection
		// event is a state in Home Assistant's history rather than a number a
		// template has to pick apart.
		{Component: BinarySensor, Key: "bms_over_voltage", Name: "BMS over voltage", DeviceClass: "problem", Category: "diagnostic"},
		{Component: BinarySensor, Key: "bms_under_voltage", Name: "BMS under voltage", DeviceClass: "problem", Category: "diagnostic"},
		{Component: BinarySensor, Key: "bms_over_temp", Name: "BMS over temperature", DeviceClass: "problem", Category: "diagnostic"},
		{Component: BinarySensor, Key: "bms_under_temp", Name: "BMS under temperature", DeviceClass: "problem", Category: "diagnostic"},
		{Component: BinarySensor, Key: "bms_charge_over_temp", Name: "BMS charge over temperature", DeviceClass: "problem", Category: "diagnostic"},
		{Component: BinarySensor, Key: "bms_charge_under_temp", Name: "BMS charge under temperature", DeviceClass: "problem", Category: "diagnostic"},
		{Component: BinarySensor, Key: "bms_discharge_over_current", Name: "BMS discharge over current", DeviceClass: "problem", Category: "diagnostic"},
		{Component: BinarySensor, Key: "bms_charge_over_current", Name: "BMS charge over current", DeviceClass: "problem", Category: "diagnostic"},
		{Component: BinarySensor, Key: "bms_internal_protection", Name: "BMS internal protection", DeviceClass: "problem", Category: "diagnostic"},
		{Component: BinarySensor, Key: "bms_module_unbalanced", Name: "BMS module unbalanced", DeviceClass: "problem", Category: "diagnostic"},
		// The inverter's own SOC settings, mirrored read-only on the input bank.
		// Deliberately carry no StateClass: near-static configuration, so
		// long-term statistics for them would be noise. No DeviceClass either:
		// "battery" would make Home Assistant read a static threshold as the
		// device's remaining charge, i.e. a permanent low-battery alert.
		{Component: Sensor, Key: "overdischarge_soc", Name: "Over-discharge SOC", Unit: "%", Category: "diagnostic"},
		{Component: Sensor, Key: "force_charge_soc", Name: "Force-charge SOC", Unit: "%", Category: "diagnostic"},

		// PV.
		{Component: Sensor, Key: "pv1_voltage", Name: "PV1 voltage", DeviceClass: "voltage", StateClass: "measurement", Unit: "V", Precision: 1},
		{Component: Sensor, Key: "pv1_current", Name: "PV1 current", DeviceClass: "current", StateClass: "measurement", Unit: "A", Precision: 1},
		{Component: Sensor, Key: "pv2_voltage", Name: "PV2 voltage", DeviceClass: "voltage", StateClass: "measurement", Unit: "V", Precision: 1},
		{Component: Sensor, Key: "pv2_current", Name: "PV2 current", DeviceClass: "current", StateClass: "measurement", Unit: "A", Precision: 1},
		{Component: Sensor, Key: "pv_total_power", Name: "PV total power", DeviceClass: "power", StateClass: "measurement", Unit: "W"},

		// Grid.
		{Component: Sensor, Key: "grid_power", Name: "Grid power", DeviceClass: "power", StateClass: "measurement", Unit: "W"},
		{Component: Sensor, Key: "grid_total_import", Name: "Grid total import", DeviceClass: "energy", StateClass: "total_increasing", Unit: "kWh"},
		{Component: Sensor, Key: "grid_import_today", Name: "Grid import today", DeviceClass: "energy", StateClass: "total", Unit: "kWh", Precision: 1},
		{Component: Sensor, Key: "grid_total_export", Name: "Grid total export", DeviceClass: "energy", StateClass: "total_increasing", Unit: "kWh"},
		{Component: Sensor, Key: "grid_export_today", Name: "Grid export today", DeviceClass: "energy", StateClass: "total", Unit: "kWh", Precision: 1},

		// AC.
		{Component: Sensor, Key: "ac_active_power", Name: "AC active power", DeviceClass: "power", StateClass: "measurement", Unit: "W"},
		{Component: Sensor, Key: "inverter_temperature", Name: "Inverter temperature", DeviceClass: "temperature", StateClass: "measurement", Unit: "°C", Precision: 1},
		{Component: Sensor, Key: "grid_frequency", Name: "Grid frequency", DeviceClass: "frequency", StateClass: "measurement", Unit: "Hz", Precision: 2},
		{Component: Sensor, Key: "house_load", Name: "House load", DeviceClass: "power", StateClass: "measurement", Unit: "W"},

		// Energy.
		{Component: Sensor, Key: "generation_today", Name: "Generation today", DeviceClass: "energy", StateClass: "total", Unit: "kWh", Precision: 1},
		{Component: Sensor, Key: "generation_yesterday", Name: "Generation yesterday", DeviceClass: "energy", Unit: "kWh", Category: "diagnostic", Precision: 1},
		{Component: Sensor, Key: "battery_total_charge", Name: "Battery total charge", DeviceClass: "energy", StateClass: "total_increasing", Unit: "kWh"},
		{Component: Sensor, Key: "battery_charge_today", Name: "Battery charge today", DeviceClass: "energy", StateClass: "total", Unit: "kWh", Precision: 1},
		{Component: Sensor, Key: "battery_total_discharge", Name: "Battery total discharge", DeviceClass: "energy", StateClass: "total_increasing", Unit: "kWh"},
		{Component: Sensor, Key: "battery_discharge_today", Name: "Battery discharge today", DeviceClass: "energy", StateClass: "total", Unit: "kWh", Precision: 1},

		// System.
		{Component: Sensor, Key: "status", Name: "Status", Category: "diagnostic"},
		// The same enum as status, rendered through the vendor's display table so
		// a fault is legible in history without a lookup.
		{Component: Sensor, Key: "status_text", Name: "Status text", Category: "diagnostic"},
		{Component: Sensor, Key: "operating_status", Name: "Operating status", Category: "diagnostic"},
		{Component: Sensor, Key: "work_mode", Name: "Energy storage mode", Category: "diagnostic"},
		{Component: Sensor, Key: "rtc", Name: "RTC", DeviceClass: "timestamp", Category: "diagnostic"},
		{Component: Sensor, Key: "rtc_drift", Name: "RTC drift", DeviceClass: "duration", Unit: "s", Category: "diagnostic"},

		// Derived schedule. Read-only renderings of the timed slots; the slots
		// themselves are written through the boost_select control below.
		{Component: Sensor, Key: "tou_window", Name: "Time-of-use window"},
		{Component: Sensor, Key: "boost", Name: "Boost"},
		{Component: Sensor, Key: "boost_ends_at", Name: "Boost ends at", DeviceClass: "timestamp"},

		// The inverter's own configured current ceiling (holding 43117/43118),
		// read-only. Deliberately carries no StateClass: it is near-static
		// configuration, so recording long-term statistics for it would be noise.
		{Component: Sensor, Key: "inverter_max_charge_current", Name: "Inverter max charge current", DeviceClass: "current", Unit: "A", Category: "diagnostic", Precision: 1},
		{Component: Sensor, Key: "inverter_max_discharge_current", Name: "Inverter max discharge current", DeviceClass: "current", Unit: "A", Category: "diagnostic", Precision: 1},

		// Writable controls, five in all. Gated behind Config.ControlsEnabled in
		// BuildDiscovery; always present in this catalogue so the state round-trip
		// stays exhaustive.
		{Component: Number, Key: "set_charge_current", Name: "Timed charge current", Command: true, Min: 0, Max: 60, Step: 0.1, Mode: "box", Unit: "A"},
		{Component: Number, Key: "set_discharge_current", Name: "Timed discharge current", Command: true, Min: 0, Max: 60, Step: 0.1, Mode: "box", Unit: "A"},
		{Component: Select, Key: "optimal_income", Name: "Optimal income", Command: true, Options: []string{"Run", "Stop"}},
		{Component: Select, Key: "boost_select", Name: "Boost control", Command: true, Options: schedule.BoostOptions()},
		{Component: Button, Key: "rtc_sync", Name: "Sync RTC now", Command: true, PayloadPress: "PRESS", Category: "diagnostic"},
	}
}
