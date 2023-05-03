import time
import logging

import manager.callbacks
from solis.inverter import Inverter
from solis.modbus import Modbus
from environs import Env
from ha_mqtt_discoverable import Settings, DeviceInfo
from ha_mqtt_discoverable.sensors import BinarySensor, BinarySensorInfo, Sensor, SensorInfo, Subscriber
from paho.mqtt.client import Client, MQTTMessage


def publish_mqtt():
    env = Env()

    serial = env.int("INVERTER_SERIAL")

    prefix_name = f"Solis Inverter"
    prefix_id = f"solis_inverter_{serial}"

    mqtt_settings = Settings.MQTT(host=env.str("MQTT_HOST"))
    client = Client("solar-inverter-manager")
    client.on_connect = manager.callbacks.on_connect
    client.on_message = manager.callbacks.on_message

    client.connect(host=env.str("MQTT_HOST"))

    client.loop_start()

    device = DeviceInfo(
        name=prefix_name,
        identifiers=prefix_id,
        manufacturer="Solis")

    poller_sensor = BinarySensor(Settings(mqtt=mqtt_settings,
                                          entity=BinarySensorInfo(
                                              name=f"{prefix_name} Poller",
                                              device_class="running",
                                              expire_after=240,
                                              unique_id=f"{prefix_id}_running",
                                              device=device)))

    from_battery = Sensor(Settings(mqtt=mqtt_settings,
                                   entity=SensorInfo(
                                       name=f"{prefix_name} Power From Battery",
                                       device_class="power",
                                       unit_of_measurement="W",
                                       expire_after=240,
                                       unique_id=f"{prefix_id}_power_from_battery",
                                       device=device)))

    to_battery = Sensor(Settings(mqtt=mqtt_settings,
                                 entity=SensorInfo(
                                     name=f"{prefix_name} Power To Battery",
                                     device_class="power",
                                     unit_of_measurement="W",
                                     expire_after=240,
                                     unique_id=f"{prefix_id}_power_to_battery",
                                     device=device)))

    battery = Sensor(Settings(mqtt=mqtt_settings,
                              entity=SensorInfo(
                                  name=f"{prefix_name} Battery",
                                  device_class="battery",
                                  unit_of_measurement="%",
                                  expire_after=240,
                                  unique_id=f"{prefix_id}_battery",
                                  device=device)))

    grid_charge_amps = Sensor(Settings(mqtt=mqtt_settings,
                                       entity=SensorInfo(
                                           name=f"{prefix_name} Charge Amps",
                                           device_class="current",
                                           unit_of_measurement="A",
                                           expire_after=240,
                                           unique_id=f"{prefix_id}_charge_amps",
                                           device=device)))

    grid_discharge_amps = Sensor(Settings(mqtt=mqtt_settings,
                                       entity=SensorInfo(
                                           name=f"{prefix_name} Discharge Amps",
                                           device_class="current",
                                           unit_of_measurement="A",
                                           expire_after=240,
                                           unique_id=f"{prefix_id}_discharge_amps",
                                           device=device)))

    optimal_income = BinarySensor(Settings(mqtt=mqtt_settings,
                                           entity=BinarySensorInfo(
                                               name=f"{prefix_name} optimal_income",
                                               expire_after=240,
                                               unique_id=f"{prefix_id}_optimal_income",
                                               device=device)))

    pv = Sensor(Settings(mqtt=mqtt_settings,
                         entity=SensorInfo(
                             name=f"{prefix_name} PV",
                             device_class="power",
                             unit_of_measurement="W",
                             expire_after=240,
                             unique_id=f"{prefix_id}_pv",
                             device=device)))

    logger = logging.getLogger("poller")

    while True:
        poller_sensor.on()

        try:
            metrics = Inverter(Modbus()).poll()
        except Exception as e:
            logger.error("Error communicating with modbus: " + str(e))

            time.sleep(60)

            continue

        poller_sensor.set_attributes({
            "time": metrics["pv"]["datetime"].strftime("%m/%d/%Y, %H:%M:%S"),
        })

        battery.set_state(metrics["meter"]["battery"]["percentage"])
        battery.set_attributes(metrics["meter"]["battery"])

        from_battery.set_state(metrics["meter"]["battery"]["power_from_the_battery"])
        to_battery.set_state(metrics["meter"]["battery"]["power_to_the_battery"])

        grid_charge_amps.set_state(metrics["grid_charge"]["grid_charging_amps"])
        grid_discharge_amps.set_state(metrics["grid_charge"]["grid_discharging_amps"])
        grid_charge_amps.set_attributes(metrics["grid_charge"])

        if metrics['grid_charge']['grid_charge_optimal_income']:
            optimal_income.on()
        else:
            optimal_income.off()

        pv.set_state(metrics["pv"]["pv_now"])
        pv.set_attributes({
            "pv_yield_today": metrics["pv"]['pv_yield_today'],
            "pv_yield_yesterday": metrics["pv"]['pv_yield_yesterday'],
            "pv_yield_this_month": metrics["pv"]['pv_yield_this_month'],
            "pv_yield_last_month": metrics["pv"]['pv_yield_last_month'],
        })

        logger.info("Published")

        time.sleep(40)
