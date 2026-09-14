Feature: Writable timed schedule (boost and Time-of-Use tariff)

  The manager owns the three timed slots: slots 1 and 2 hold the configured
  Time-of-Use tariff and slot 3 holds an ad-hoc boost. A boost is programmed one
  register at a time — start hour, start minute, end hour, end minute — so the
  one-second transient can never charge or discharge in the wrong direction, and
  every register still passes the read-before-write guard that spares the
  flash-backed holding bank. A reconcile after each poll restores tariff drift and
  clears an expired boost, and costs nothing when the inverter already agrees.
  These scenarios drive the controls handler against a fake holding-register bank
  with a fixed clock — no sidecar, no broker.

  Background:
    Given a configured publisher with a recording MQTT client and a stub inverter reader
    And a controls handler over a fake inverter holding-register bank with the clock at 2026-09-12T14:07:00Z and TOU window "23:30-05:30"
    And holding register 43110 currently reads 35

  Scenario: A boost is written one register at a time and mirrored into the state document
    When a "boost_select" command arrives with payload "Charge 30 min"
    Then holding registers 43163 to 43166 are written in order with "14,7,14,30"
    And every write is confirmed by a re-read
    When the setpoints are read from the holding bank at 2026-09-12T14:07:00Z
    And a poll is collected and state is published with those setpoints
    Then the state document reports boost_select as "Charge 30 min"
    And the state document reports boost as "Charge until 14:30"
    And the state document reports boost_ends_at as "2026-09-12T14:30:00Z"

  Scenario: Selecting Off clears the boost slot in the same order
    Given holding register 43163 currently reads 14
    And holding register 43164 currently reads 7
    And holding register 43165 currently reads 14
    And holding register 43166 currently reads 30
    When a "boost_select" command arrives with payload "Off"
    Then holding registers 43163 to 43166 are written in order with "0,0,0,0"

  Scenario: A boost is refused while Optimal Income is Stop
    Given holding register 43110 currently reads 33
    When a "boost_select" command arrives with payload "Charge 30 min"
    Then no holding register is written

  Scenario: A boost is refused when it would overlap the Time-of-Use window
    Given a controls handler over a fake inverter holding-register bank with the clock at 2026-09-12T23:20:00Z and TOU window "23:30-05:30"
    And holding register 43110 currently reads 35
    When a "boost_select" command arrives with payload "Charge 30 min"
    Then no holding register is written

  Scenario: An expired boost is cleared by the reconcile
    Given holding register 43143 currently reads 23
    And holding register 43144 currently reads 30
    And holding register 43155 currently reads 5
    And holding register 43156 currently reads 30
    And holding register 43163 currently reads 14
    And holding register 43164 currently reads 2
    And holding register 43165 currently reads 14
    And holding register 43166 currently reads 56
    When the schedule is reconciled at 2026-09-12T19:00:00Z
    Then holding registers 43163 to 43166 are written in order with "0,0,0,0"

  # A command interrupted mid-sequence can leave slot 3 holding the tail of the
  # window it was clearing (here a charge end minute of 56, the rest already zero)
  # next to the discharge boost it went on to program. Liveness is judged per
  # direction, so the reconcile clears the remnant — 00:00-00:56 is not running at
  # 08:59 — and leaves the running boost alone. The unseeded slot-3 registers read
  # 0 from the empty bank.
  Scenario: A partially cleared boost is healed, not wiped, on reconcile
    Given holding register 43143 currently reads 23
    And holding register 43144 currently reads 30
    And holding register 43155 currently reads 5
    And holding register 43156 currently reads 30
    And holding register 43166 currently reads 56
    And holding register 43167 currently reads 8
    And holding register 43168 currently reads 58
    And holding register 43169 currently reads 9
    When the schedule is reconciled at 2026-09-14T08:59:00Z
    Then holding register 43166 is written once with 0

  # The manager only ever writes a slot-3 window that starts now and ends before
  # midnight, so a window ending at 00:00 is a remnant of a boost whose end
  # registers never landed, however recently it started.
  Scenario: A remnant ending at midnight is cleared on reconcile
    Given holding register 43143 currently reads 23
    And holding register 43144 currently reads 30
    And holding register 43155 currently reads 5
    And holding register 43156 currently reads 30
    And holding register 43163 currently reads 14
    And holding register 43164 currently reads 0
    And holding register 43165 currently reads 0
    And holding register 43166 currently reads 0
    When the schedule is reconciled at 2026-09-14T15:00:00Z
    Then holding register 43163 is written once with 0

  # A boost that ran last night is no longer live once the clock has passed
  # midnight, so the reconcile clears it before it can fire again tonight.
  Scenario: Yesterday's boost is cleared after midnight
    Given holding register 43143 currently reads 23
    And holding register 43144 currently reads 30
    And holding register 43155 currently reads 5
    And holding register 43156 currently reads 30
    And holding register 43163 currently reads 23
    And holding register 43164 currently reads 40
    And holding register 43165 currently reads 23
    And holding register 43166 currently reads 45
    When the schedule is reconciled at 2026-09-14T00:05:00Z
    Then holding registers 43163 to 43166 are written in order with "0,0,0,0"

  Scenario: Time-of-Use drift is re-asserted across the midnight split
    Given holding register 43143 currently reads 23
    And holding register 43144 currently reads 31
    And holding register 43155 currently reads 5
    And holding register 43156 currently reads 29
    When the schedule is reconciled at 2026-09-12T14:07:00Z
    Then the holding registers written in order are "43144=30,43156=30"

  Scenario: A Time-of-Use window the inverter already holds costs no write
    Given holding register 43143 currently reads 23
    And holding register 43144 currently reads 30
    And holding register 43155 currently reads 5
    And holding register 43156 currently reads 30
    When the schedule is reconciled at 2026-09-12T14:07:00Z
    Then no holding register is written

  Scenario: Optimal income Run flips only work-mode bit 1
    Given holding register 43110 currently reads 33
    When an "optimal_income" command arrives with payload "Run"
    Then holding register 43110 is written once with 35

  Scenario: Optimal income Stop clears only work-mode bit 1
    Given holding register 43110 currently reads 35
    When an "optimal_income" command arrives with payload "Stop"
    Then holding register 43110 is written once with 33

  Scenario: Optimal income Stop preserves unrelated work-mode bits
    Given holding register 43110 currently reads 291
    When an "optimal_income" command arrives with payload "Stop"
    Then holding register 43110 is written once with 289
