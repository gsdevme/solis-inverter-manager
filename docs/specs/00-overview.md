# 00 — Overview

> **Status: skeleton.** Full content authored in a later phase. Sections below are
> outlines with TODO notes.

## Purpose

A small, stdlib-first Go **manager** that polls a Solis hybrid inverter and
republishes its state to **MQTT** with **Home Assistant (HA) MQTT autodiscovery**,
and exposes writable setpoint controls back to HA.

- **Hardware:** Solis **RHI-3.6K-48ES-5G** hybrid inverter, reached via its
  **Solarman V5 datalogger** (gen2 WiFi stick) over TCP **port 8899**.
- **Architecture:** a **two-container pod** — a Go **manager** (this repo:
  orchestration, decode, MQTT, HA discovery, scheduling, health) plus a **thin
  Python sidecar** that owns only the Modbus/Solarman-V5 transport
  (`pysolarmanv5`). The manager talks to the sidecar over **localhost HTTP**; the
  sidecar never touches MQTT or HA.

## Principles

- TODO: spec-driven; stdlib-first (latest Go); self-contained; Kube-friendly
  (probes, env config, slog, graceful shutdown).
- TODO: **read-only by default**, with a small, explicitly-guarded set of writable
  setpoints (see the write-guard in `03-mqtt-ha-discovery.md` and `05-config.md`).

## High-level flow

TODO: load/validate config -> connect sidecar + MQTT -> publish discovery ->
poll every `POLL_INTERVAL` (read via sidecar, decode, publish state, apply guarded
writes) -> health/readiness -> graceful shutdown.

## Non-goals

TODO.

## Reference

See the sibling specs:
- `01-sidecar-contract.md` — the localhost HTTP contract between manager and sidecar.
- `02-register-map.md` — the confirmed Solis register map (source: `docs/phase0/findings.md`).
- `03-mqtt-ha-discovery.md` — MQTT topics, HA discovery, writable controls, write-guard.
- `04-polling-scheduling.md` — the poll loop.
- `05-config.md` — environment variables.
- `06-lifecycle-health.md` — probes, readiness, graceful shutdown, status page.
- `07-testing.md` — mock sidecar, unit tests, godog suite.
- `08-deployment.md` — the two-container pod, image, CI/CD.
- `REQUIREMENTS.md` — traceable requirement IDs (`REQ-*`).
