Feature: MQTT writable controls (guarded, flash-sparing)

  Inbound Home Assistant command messages route through a read-before-write guard:
  a fc06 write is issued only when the holding register differs from the commanded
  value, and every write is confirmed by a re-read. Needless writes are skipped
  because the holding bank is flash-backed. These scenarios drive the controls
  handler against a fake inverter holding-register bank — no sidecar, no broker —
  and assert the guard and state-mirror contract Home Assistant relies on.

  Background:
    Given a configured publisher with a recording MQTT client and a stub inverter reader
    And a controls handler over a fake inverter holding-register bank

  Scenario: In-range charge-current command is guarded, written and confirmed
    Given holding register 43141 currently reads 0
    When a "set_charge_current" command arrives with payload "30"
    Then holding register 43141 is written once with 300
    And the write is confirmed by a re-read

  Scenario: A command equal to the current value is skipped (no flash write)
    Given holding register 43141 currently reads 300
    When a "set_charge_current" command arrives with payload "30"
    Then no holding register is written

  Scenario: An unparseable charge-current command is rejected
    Given holding register 43141 currently reads 0
    When a "set_charge_current" command arrives with payload "abc"
    Then no holding register is written

  Scenario: An out-of-range charge-current command is clamped, not rejected
    Given holding register 43141 currently reads 0
    When a "set_charge_current" command arrives with payload "200"
    Then holding register 43141 is written once with 600

  Scenario: Optimal income Run preserves unrelated work-mode bits
    Given holding register 43110 currently reads 289
    When an "optimal_income" command arrives with payload "Run"
    Then holding register 43110 is written once with 291

  Scenario: State document mirrors the writable-control setpoints
    Given the writable controls read charge 12.5 A, discharge 7 A, optimal income Run
    When a poll is collected and state is published with those setpoints
    Then the state document contains the keys "set_charge_current,set_discharge_current,optimal_income"
    And the state document reports set_charge_current as 12.5
    And the state document reports set_discharge_current as 7
    And the state document reports optimal_income as "Run"
