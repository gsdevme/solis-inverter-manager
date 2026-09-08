"""Thin Solarman V5 / Modbus transport sidecar for the Go manager.

The package exposes only generic, dumb register RPCs over localhost HTTP: reads
return raw uint16 words and writes are unconditional. All register semantics
(decode, scale, sign, MQTT, Home Assistant, the read-before-write guard) live in
the Go manager — see ``docs/specs/01-sidecar-contract.md``.
"""
