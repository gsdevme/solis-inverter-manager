Feature: Resilient polling and readiness

  The scheduler polls the inverter on a serialised loop, retries transient read
  failures with backoff, keeps the last successfully-read state cached, and drives
  readiness: ready after the first successful poll, not-ready after FAILURE_THRESHOLD
  consecutive failures. These scenarios drive the real scheduler and status server
  against a stub inverter with an immediate (fake) backoff clock — no sidecar, no
  broker, no wall-clock waits.

  Scenario: Readiness gates on the first successful poll
    Given a resilient scheduler whose inverter fails the first 0 reads and a failure threshold of 3
    Then the readiness endpoint returns 503
    When the scheduler completes 1 poll
    Then the readiness endpoint returns 200
    And the last-good state reports battery SOC 47

  Scenario: A transient read failure is absorbed by retry and never flips not-ready
    Given a resilient scheduler whose inverter fails the first 2 reads and a failure threshold of 3
    When the scheduler completes 1 poll
    Then the readiness endpoint returns 200
    And the last-good state reports battery SOC 47

  Scenario: Persistent failures flip not-ready after the threshold, retaining the last-good state
    Given a resilient scheduler whose inverter fails the first 0 reads and a failure threshold of 2
    When the scheduler completes 1 poll
    Then the readiness endpoint returns 200
    And the last-good state reports battery SOC 47
    When the inverter starts failing every read
    And the scheduler completes 2 polls
    Then the readiness endpoint returns 503
    And the last-good state reports battery SOC 47
