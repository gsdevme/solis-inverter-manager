Feature: Manager health endpoints

  The manager exposes a liveness probe that is always up while the process runs and
  a readiness probe that only reports ready once the readiness flag is set (the
  scheduler flips it after the first successful poll in a later phase).

  Scenario: Liveness is up and readiness gates on the ready flag
    Given the manager status server is running
    Then the liveness endpoint returns 200
    And the readiness endpoint returns 503
    When the manager is marked ready
    Then the readiness endpoint returns 200

  Scenario: The status page shows the last inverter values
    Given the manager status server is running
    And a configured publisher with a recording MQTT client and a stub inverter reader
    And the inverter reports a battery state of charge of 57 percent
    When a poll is collected and state is published
    And the published state document is recorded for the status page
    Then the status page shows "battery_soc" as "57"
