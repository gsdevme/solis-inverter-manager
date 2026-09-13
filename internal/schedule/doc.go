// Package schedule turns the inverter's three timed slots into the logical
// schedule Home Assistant presents.
//
// The inverter cannot express a window that crosses midnight, so the owner's
// Time-of-Use tariff is physically two slots joined at 00:00 (slot 1 ending at
// end-of-day, slot 2 starting at midnight); ToU rejoins them into the single
// window a human recognises. Slot 3 is the only slot left for an ad-hoc boost, so
// BoostOf reads the boost mode and window from it alone.
//
// The package is pure: no I/O, no configuration and no clock of its own — the one
// time-dependent result, Boost.EndsAt, derives its date from the now passed in.
// See docs/specs/REQUIREMENTS.md and docs/phase0/findings.md for the register
// ground truth behind the slot layout.
package schedule
