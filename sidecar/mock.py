"""In-memory fake register server for ``MODE=mock``.

:class:`MockTransport` implements the same interface as
:class:`sidecar.transport.LiveTransport` but with no ``pysolarmanv5`` and no
socket. It seeds an ``addr -> uint16`` dict from the ``by_addr`` maps of one or
more Phase 0 fixtures; reads return stored (or zero-filled) words, writes update
the dict, and ``/health`` always reports reachable. This lets the whole dev/CI
loop run with no hardware.

No single Phase 0 capture covers the whole device — the full sweep skips the
ranges the other snapshots already hold — so seeding from several complementary
fixtures is the normal case, not an edge case.
"""

from __future__ import annotations

import json
import threading
from collections.abc import Iterable
from pathlib import Path


class MockTransport:
    """Fixture-seeded fake transport with the LiveTransport interface."""

    def __init__(self, fixtures: str | Iterable[str]):
        """Seed from one fixture path or several.

        Fixtures are applied in order, so a later one wins where two define the
        same register. A bare string is treated as a single path rather than as
        an iterable of characters.
        """
        if isinstance(fixtures, str):
            fixtures = (fixtures,)
        self._lock = threading.Lock()
        self._regs: dict[int, int] = {}
        for fixture_path in fixtures:
            data = json.loads(Path(fixture_path).read_text())
            for block in data.get("blocks", []):
                for addr, value in block.get("by_addr", {}).items():
                    self._regs[int(addr)] = int(value) & 0xFFFF

    def read_input(self, addr: int, count: int) -> list[int]:
        return self._read(addr, count)

    def read_holding(self, addr: int, count: int) -> list[int]:
        return self._read(addr, count)

    def write_holding(self, addr: int, value: int) -> int:
        with self._lock:
            self._regs[addr] = value & 0xFFFF
        return value

    def health(self) -> bool:
        return True

    def _read(self, addr: int, count: int) -> list[int]:
        with self._lock:
            return [self._regs.get(addr + i, 0) for i in range(count)]
