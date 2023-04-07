import manager.callbacks
from environs import Env
from paho.mqtt.client import Client

def handle_events():
    env = Env()

    client = Client("solar-inverter-manager")
    client.on_connect = manager.callbacks.on_connect
    client.on_message = manager.callbacks.on_message

    client.connect(host=env.str("MQTT_HOST"))

    client.loop_forever()
