// Package schedule turns the inverter's three timed slots into the logical
// schedule Home Assistant presents.
//
// The inverter cannot express a window that crosses midnight, so the owner's
// Time-of-Use tariff is physically two slots joined at 00:00 (slot 1 ending at
// end-of-day, slot 2 starting at midnight); ToU rejoins them into the single
// window a human recognises. Slot 3 is the only slot left for an ad-hoc boost, so
// BoostOf reads the boost mode and window from it alone.
//
// The write side is declarative. Desired builds the schedule the inverter should
// hold — the configured Time-of-Use window (ParseToUWindow, ToUSlots) in slots 1
// and 2, and the boost slot keeping every window still running while clearing
// each one that has ended — which a reconciler elsewhere makes true one guarded
// register write at a time. Expiry is judged per direction, so a boost written
// only in part (a register lost to a transport failure) is healed by the next
// reconcile rather than escalated into a wiped slot. PlanBoost turns a select
// command into the slot to hold, snapping the end to the quarter-hour grid and
// refusing a boost that would cross midnight or run inside the tariff window;
// BoostSelectState maps the registers back to the select's state.
//
// The package is pure: no I/O, no configuration and no clock of its own — the
// time-dependent results, Boost.EndsAt, Desired and PlanBoost, derive their
// dates from the now passed in.
// See docs/specs/REQUIREMENTS.md and docs/phase0/findings.md for the register
// ground truth behind the slot layout.
package schedule
