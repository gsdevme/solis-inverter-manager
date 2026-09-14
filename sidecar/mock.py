"""In-memory fake register server for ``MODE=mock``.

:class:`MockTransport` implements the same interface as
:class:`sidecar.transport.LiveTransport` but with no ``pysolarmanv5`` and no
socket. It seeds an ``addr -> uint16`` dict from the ``by_addr`` maps of a Phase 0
fixture; reads return stored (or zero-filled) words, writes update the dict, and
``/health`` always reports reachable. This lets the whole dev/CI loop run with no
hardware.
"""

from __future__ import annotations

import json
import threading
from pathlib import Path


class MockTransport:
    """Fixture-seeded fake transport with the LiveTransport interface."""

    def __init__(self, fixture_path: str):
        self._lock = threading.Lock()
        self._regs: dict[int, int] = {}
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
