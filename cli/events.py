import manager.callbacks
from paho.mqtt.client import Client
from solis.modbus import Modbus

class HandleEvents:
    __mqtt_host: str
    __serial: str
    __modbus: Modbus

    def __init__(self, modbus: Modbus, mqtt_host: str):
        self.__mqtt_host = mqtt_host
        self.__modbus = modbus

    def __call__(self):
        client = Client("solar-inverter-manager")
        client.on_connect = manager.callbacks.on_connect
        client.on_message = manager.callbacks.on_message

        client.connect(host=self.__mqtt_host)

        client.loop_forever()

