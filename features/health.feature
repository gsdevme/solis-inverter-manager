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
