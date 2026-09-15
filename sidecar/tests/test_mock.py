"""Seeding behaviour of the fixture-backed MockTransport.

The point of these tests is that a mock run must look like a live inverter, not
a dead one: no single Phase 0 capture covers the whole device, so the default
seed merges two complementary snapshots.
"""

from __future__ import annotations

from sidecar.config import _DEFAULT_FIXTURES
from sidecar.mock import MockTransport

# Battery block registers that the full sweep alone does not carry.
_BATTERY_SOC = 33139
_BMS_CHARGE_LIMIT = 33143
_BMS_DISCHARGE_LIMIT = 33144


def _defaults() -> MockTransport:
    return MockTransport([str(p) for p in _DEFAULT_FIXTURES])


def test_default_seed_fills_the_battery_block():
    # Seeded from the full sweep alone this block reads all zeros, which makes a
    # mock run look like a dead inverter.
    regs = _defaults().read_input(_BATTERY_SOC, 6)
    assert regs[0] == 99, "battery SOC must come from the comprehensive snapshot"
    assert regs[_BMS_CHARGE_LIMIT - _BATTERY_SOC] == 150
    assert regs[_BMS_DISCHARGE_LIMIT - _BATTERY_SOC] == 1125


def test_full_sweep_alone_leaves_the_battery_block_empty():
    # Pins the reason the default seeds two fixtures rather than one.
    sweep = next(p for p in _DEFAULT_FIXTURES if "full-sweep" in p.name)
    assert MockTransport(str(sweep)).read_input(_BATTERY_SOC, 6) == [0] * 6


def test_default_seed_keeps_the_ranges_only_the_full_sweep_holds():
    # 43117/43118: the inverter's configured current ceiling, 100.0 A.
    assert _defaults().read_holding(43117, 2) == [1000, 1000]


def test_a_bare_string_is_one_path_not_an_iterable_of_characters():
    single = str(next(p for p in _DEFAULT_FIXTURES if "comprehensive" in p.name))
    assert MockTransport(single).read_input(_BATTERY_SOC, 1) == [99]


def test_later_fixtures_win_on_conflict(tmp_path):
    first = tmp_path / "first.json"
    second = tmp_path / "second.json"
    first.write_text('{"blocks": [{"by_addr": {"33139": 11}}]}')
    second.write_text('{"blocks": [{"by_addr": {"33139": 22}}]}')
    assert MockTransport([str(first), str(second)]).read_input(_BATTERY_SOC, 1) == [22]


def test_writes_still_land_over_a_merged_seed():
    t = _defaults()
    t.write_holding(43141, 340)
    assert t.read_holding(43141, 1) == [340]
