Feature: MQTT / Home Assistant discovery pipeline

  The publisher turns a decoded inverter reading into Home Assistant autodiscovery
  configs, a retained JSON state document, and a retained availability message.
  These scenarios drive the publisher against a recording MQTT client and a stub
  inverter reader — no broker and no sidecar — and assert the retained-payload
  contract Home Assistant relies on.

  Background:
    Given a configured publisher with a recording MQTT client and a stub inverter reader

  Scenario: Discovery publishes a retained config for every entity
    When discovery is published
    Then a retained discovery config is published for every entity

  Scenario: State publishes a retained JSON document with the expected sensor keys
    Given the inverter reports a battery state of charge of 42 percent
    When a poll is collected and state is published
    Then a retained state document is published at the state topic
    And the state document contains the keys "battery_soc,grid_power,rtc"
    And the state document reports battery_soc as 42

  Scenario: State carries the BMS fault, SOC-threshold and status-text diagnostics
    When a poll is collected and state is published
    Then a retained state document is published at the state topic
    And the state document contains the keys "bms_fault_1,bms_over_voltage,overdischarge_soc,force_charge_soc,status_text"

  Scenario: Availability reports online retained
    When availability is published as online
    Then a retained "online" message is published at the availability topic

  Scenario: Availability reports offline retained
    When availability is published as offline
    Then a retained "offline" message is published at the availability topic
