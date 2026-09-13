Feature: Readable timed charge schedule

  The inverter's three timed slots are published as derived, intention-named
  sensors rather than raw register pairs: the Time-of-Use charge window rejoined
  across the midnight split in slots 1 and 2, and slot 3 as an ad-hoc boost with
  the instant it ends. Nothing here writes to the inverter — the schedule is
  read-only. These scenarios read the setpoint block from a fake inverter holding
  bank and assert the state document Home Assistant subscribes to.

  Background:
    Given a configured publisher with a recording MQTT client and a stub inverter reader
    And a controls handler over a fake inverter holding-register bank

  Scenario: The Time-of-Use window and the boost are published
    Given holding register 43143 currently reads 23
    And holding register 43144 currently reads 31
    And holding register 43155 currently reads 5
    And holding register 43156 currently reads 29
    And holding register 43163 currently reads 14
    And holding register 43164 currently reads 2
    And holding register 43165 currently reads 14
    And holding register 43166 currently reads 56
    When the setpoints are read from the holding bank at 2026-09-12T12:00:00Z
    And a poll is collected and state is published with those setpoints
    Then the state document contains the keys "tou_window,boost,boost_ends_at"
    And the state document reports tou_window as "23:31–05:29"
    And the state document reports boost as "Charge until 14:56"
    And the state document reports boost_ends_at as "2026-09-12T14:56:00Z"
    And no holding register is written

  Scenario: A schedule with no boost slot publishes the boost as off
    Given holding register 43143 currently reads 23
    And holding register 43144 currently reads 31
    And holding register 43155 currently reads 5
    And holding register 43156 currently reads 29
    When the setpoints are read from the holding bank at 2026-09-12T12:00:00Z
    And a poll is collected and state is published with those setpoints
    Then the state document reports tou_window as "23:31–05:29"
    And the state document reports boost as "Off"
    And the state document reports boost_ends_at as null
