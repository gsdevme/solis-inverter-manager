// Package controls owns the inverter write path: the read-before-write guard,
// MQTT command routing, and the setpoint read that mirrors control state back to
// Home Assistant.
//
// Guard is the core primitive — it reads a holding register, writes (fc06) only
// when the value differs, then re-reads to confirm — so needless writes never
// wear the flash-backed holding bank (REQ-HA-10). Handler.OnMessage parses a
// command key from a `.../<key>/set` topic, validates the payload, and maps it
// to a guarded write (charge/discharge amps, the "optimal income" work-mode bit,
// a boost programmed into the last timed slot, or an RTC sync), refusing to
// panic on bad input. Handler.Reconcile is the declarative counterpart, driven
// by the poll rather than by a command: it asserts the schedule
// internal/schedule says the inverter should hold, writing only the registers
// that differ. Locking serializes the poll goroutine's input reads and the
// handler's holding read/writes on one mutex so they can share a single sidecar
// client.
//
// All register semantics live in internal/inverter; this package only sequences
// reads and writes. See docs/specs/03-mqtt-ha-discovery.md and
// docs/specs/09-schedule-controls.md.
package controls
