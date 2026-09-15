# Changelog

## [2.4.0](https://github.com/gsdevme/solis-inverter-manager/compare/v2.3.1...v2.4.0) (2026-09-15)


### Features

* **inverter:** publish BMS and inverter current limits ([7203edb](https://github.com/gsdevme/solis-inverter-manager/commit/7203edba9d8473c558252cc23f4c754506be2b3d))


### Bug Fixes

* **sidecar:** seed the mock from complementary fixtures ([ae11256](https://github.com/gsdevme/solis-inverter-manager/commit/ae1125638e5e68737de75cd5db4fcc2d96499c8b))

## [2.3.1](https://github.com/gsdevme/solis-inverter-manager/compare/v2.3.0...v2.3.1) (2026-09-15)


### Bug Fixes

* **cmd:** correct the TOU warning, health-error and sidecar-wait paths ([070844e](https://github.com/gsdevme/solis-inverter-manager/commit/070844e340511d11ac41bb402bceea50c366dc02))
* **config:** reject malformed integer environment variables ([4e79f2c](https://github.com/gsdevme/solis-inverter-manager/commit/4e79f2c5656bd51dac4d2f50f9e09e121bccd190))
* **homeassistant:** rename the boost select to "Boost control" ([208ba4e](https://github.com/gsdevme/solis-inverter-manager/commit/208ba4ec77aade0dba11394ec088bfed343c15e8))

## [2.3.0](https://github.com/gsdevme/solis-inverter-manager/compare/v2.2.0...v2.3.0) (2026-09-14)


### Features

* **server:** show the last inverter values on the status page ([5b0cff1](https://github.com/gsdevme/solis-inverter-manager/commit/5b0cff10c70b427926ac17a1982cf0b775564fd8))

## [2.2.0](https://github.com/gsdevme/solis-inverter-manager/compare/v2.1.0...v2.2.0) (2026-09-14)


### Features

* **homeassistant:** publish suggested_display_precision per register scale ([2696e6d](https://github.com/gsdevme/solis-inverter-manager/commit/2696e6d390a756a8cb537fc544acb836a87a80b3))

## [2.1.0](https://github.com/gsdevme/solis-inverter-manager/compare/v2.0.1...v2.1.0) (2026-09-14)


### Features

* **cmd:** wait for the sidecar before announcing and polling ([#32](https://github.com/gsdevme/solis-inverter-manager/issues/32)) ([179ee27](https://github.com/gsdevme/solis-inverter-manager/commit/179ee27bd8a28b91ca851bfa76bd3c6980e4bc8a))

## [2.0.1](https://github.com/gsdevme/solis-inverter-manager/compare/v2.0.0...v2.0.1) (2026-09-14)


### Bug Fixes

* **inverter:** derive battery direction from the 33135 flag ([bdf23db](https://github.com/gsdevme/solis-inverter-manager/commit/bdf23dba8e04fd34b2300cddd6c0b7d39faaf9eb))

## [2.0.0](https://github.com/gsdevme/solis-inverter-manager/compare/v1.6.1...v2.0.0) (2026-09-14)


### ⚠ BREAKING CHANGES

* Home Assistant entity ids, domains and command topics change; see docs/specs/08-deployment.md "Migrating from the legacy Python publisher".

### Features

* **cmd:** wire sidecar→decode→MQTT/HA publish with an interim poll ticker ([6fd2e72](https://github.com/gsdevme/solis-inverter-manager/commit/6fd2e72133911596fd37247429785a137bc56718))
* **cmd:** wire writable controls, guarded command handler, CONTROLS_ENABLED ([55be885](https://github.com/gsdevme/solis-inverter-manager/commit/55be8855a5e2d9f6782fc715f2dd775fbdfd5857))
* **controls:** program the boost slot and assert the ToU window (B2, [#28](https://github.com/gsdevme/solis-inverter-manager/issues/28)) ([d7ff67b](https://github.com/gsdevme/solis-inverter-manager/commit/d7ff67ba035c0b6cbfb308015fd4a1e042291f5d))
* **controls:** read-before-write guard, command routing, setpoint read ([a534276](https://github.com/gsdevme/solis-inverter-manager/commit/a534276bf16c2e8fd201e1ccf60514e88fda1ceb))
* **homeassistant:** add unsigned battery charge/discharge power sensors ([02d38ef](https://github.com/gsdevme/solis-inverter-manager/commit/02d38efd1adac3cfb76ce82d35d898e08e297ee3))
* **homeassistant:** add writable control entities and setpoint state ([f7004de](https://github.com/gsdevme/solis-inverter-manager/commit/f7004de32f37d206be0777b636e3eec57370df22))
* **homeassistant:** derive entity ids from a configurable prefix ([9956de7](https://github.com/gsdevme/solis-inverter-manager/commit/9956de76f0c39df3dc6dc2b3a8ed2f66c8464684))
* **homeassistant:** expose the timed schedule as derived sensors (B1, [#28](https://github.com/gsdevme/solis-inverter-manager/issues/28)) ([14394df](https://github.com/gsdevme/solis-inverter-manager/commit/14394df555f6afc9bc7717cc090e35879f05236b))
* **homeassistant:** pure HA autodiscovery payload builder ([9cbd9a6](https://github.com/gsdevme/solis-inverter-manager/commit/9cbd9a627aeee5c2cd832cd80cd8442de4ffb55c))
* **inverter:** add RTCWriteRegisters holding-block write helper ([f995dfb](https://github.com/gsdevme/solis-inverter-manager/commit/f995dfb37b0b50d5b374a0a15084567faf6d6533))
* **inverter:** confirm timed slots 2/3 at stride 10 (Stage A, [#27](https://github.com/gsdevme/solis-inverter-manager/issues/27)) ([7be262d](https://github.com/gsdevme/solis-inverter-manager/commit/7be262d0c23fb6895015bc5902b290d8c3ed9271))
* **inverter:** decode the confirmed register map into physical values ([bd0ebd3](https://github.com/gsdevme/solis-inverter-manager/commit/bd0ebd3008df46fe0a96628464e478ea6e06e0c3))
* **lifecycle:** unify teardown and drain the reconnect hook on shutdown ([d9fdf52](https://github.com/gsdevme/solis-inverter-manager/commit/d9fdf5295ac929dbc1f9b153247716d56531e751))
* **mqtt:** add inbound message handler and Subscribe seam ([dff66d2](https://github.com/gsdevme/solis-inverter-manager/commit/dff66d21f1e7a58c3aab9e29f39fa0c37edf34cd))
* **mqtt:** autopaho MQTT5 transport with retained LWT ([59f0a9a](https://github.com/gsdevme/solis-inverter-manager/commit/59f0a9a0fdb4566bc47e859855f078c1e8a2ceae))
* **publisher:** glue HA payloads to MQTT plus reusable poll unit ([352f177](https://github.com/gsdevme/solis-inverter-manager/commit/352f17769c64b0133873c4f6c361ef2a048a4443))
* replace the Python monolith with the Go manager and sidecar ([#26](https://github.com/gsdevme/solis-inverter-manager/issues/26)) ([cb6fd20](https://github.com/gsdevme/solis-inverter-manager/commit/cb6fd20435aaf45fef503279d3a340ed66da1a97))
* scaffold spec-driven Go manager (Phase 1) ([fb95c1e](https://github.com/gsdevme/solis-inverter-manager/commit/fb95c1edf4511d48856499d6a487cbea54607c1d))
* **scheduler:** resilient poll loop with backoff, readiness, opt-in RTC auto-sync ([2aecb25](https://github.com/gsdevme/solis-inverter-manager/commit/2aecb2529f0ddd5bff9c599df1bad258a902eea9))
* **sidecar:** add thin Solarman V5 transport sidecar ([7303ec5](https://github.com/gsdevme/solis-inverter-manager/commit/7303ec569c20c83cbf2f5e1b1c2fb1c82d22332c))
* **sidecarclient:** typed Go client for the sidecar RPCs ([f7d4a40](https://github.com/gsdevme/solis-inverter-manager/commit/f7d4a40b4927f3b1de2157136924c0af56c81fc3))


### Bug Fixes

* **cmd:** preserve last-known setpoints when the holding read fails ([0dfae71](https://github.com/gsdevme/solis-inverter-manager/commit/0dfae71b89ae07ab0c56e9e776da06ea2ee8818c))
* **controls:** abort a register sequence on the first failed write ([#28](https://github.com/gsdevme/solis-inverter-manager/issues/28)) ([9918e19](https://github.com/gsdevme/solis-inverter-manager/commit/9918e19615728ce309c194a4b53973b5b47f9b04))
* **publisher:** read input bank as two ≤100-reg blocks ([9c5b239](https://github.com/gsdevme/solis-inverter-manager/commit/9c5b239b20c006a37a7be1f08949165c133d94e2))
* **schedule:** clear slot-3 windows a boost could never have written ([#28](https://github.com/gsdevme/solis-inverter-manager/issues/28)) ([b5a44f7](https://github.com/gsdevme/solis-inverter-manager/commit/b5a44f72fa67c4656252fa33c297e845f20c7926))
* **sidecar:** reset the session when a request gets no reply ([2c8a648](https://github.com/gsdevme/solis-inverter-manager/commit/2c8a64822cfb4565af17953c426d507ead9249d6))
