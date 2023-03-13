import time
from solis.inverter import Inverter
from solis.modbus import Modbus
from environs import Env
from ha_mqtt_discoverable import Settings, DeviceInfo
from ha_mqtt_discoverable.sensors import BinarySensor, BinarySensorInfo, Sensor, SensorInfo


def publish_mqtt():
    env = Env()

    serial = env.int("INVERTER_SERIAL")

    prefix_name = f"Solis Inverter"
    prefix_id = f"solis_inverter_{serial}"

    mqtt_settings = Settings.MQTT(host="mqtt.home.gsdev.me")

    device = DeviceInfo(
        name=prefix_name,
        identifiers=prefix_id,
        manufacturer="Solis")

    poller_sensor = BinarySensor(Settings(mqtt=mqtt_settings,
                                          entity=BinarySensorInfo(
                                              name=f"{prefix_name} Poller",
                                              device_class="running",
                                              expire_after=180,
                                              unique_id=f"{prefix_id}_running",
                                              device=device)))
    battery = Sensor(Settings(mqtt=mqtt_settings,
                              entity=SensorInfo(
                                  name=f"{prefix_name} Battery",
                                  device_class="battery",
                                  unit_of_measurement="%",
                                  expire_after=180,
                                  unique_id=f"{prefix_id}_battery",
                                  device=device)))

    grid_charge_amps = Sensor(Settings(mqtt=mqtt_settings,
                              entity=SensorInfo(
                                  name=f"{prefix_name} Charge Amps",
                                  device_class="current",
                                  unit_of_measurement="A",
                                  expire_after=180,
                                  unique_id=f"{prefix_id}_charge_amps",
                                  device=device)))

    pv = Sensor(Settings(mqtt=mqtt_settings,
                              entity=SensorInfo(
                                  name=f"{prefix_name} PV",
                                  device_class="power",
                                  unit_of_measurement="W",
                                  expire_after=180,
                                  unique_id=f"{prefix_id}_pv",
                                  device=device)))


    while True:
        poller_sensor.on()

        metrics = Inverter(Modbus()).poll()

        battery.set_state(metrics["meter"]["battery"]["percentage"])
        battery.set_attributes(metrics["meter"]["battery"])

        grid_charge_amps.set_state(metrics["grid_charge"]["grid_charging_amps"])
        grid_charge_amps.set_attributes(metrics["grid_charge"])

        pv.set_state(metrics["pv"]["pv_now"])

        # print(metrics["meter"]["battery"]["percentage"])

        print("Published")

        time.sleep(60)

    #
    # client = mqtt.Client("solis-inverter-manager")  # create new instance
    # client.connect("mqtt.home.gsdev.me")  # connect to broker
    # client.publish("solis-inverter-manage/test", {"test": 1, "foo": 4})  # publish
