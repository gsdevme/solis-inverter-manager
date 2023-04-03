from manager import topics
from solis.inverter import Inverter
from solis.modbus import Modbus
import logging

def set_charge(payload):
    try:
        amps = float(payload)
    except ValueError:
        return

    if amps > 60 or amps < 0:
        return

    logging.getLogger("manager").info("setting charge amps to " + str(amps))

    Inverter(Modbus()).set_grid_charging_amps(amps)

def on_connect(client, userdata, flags, rc):
    client.subscribe(topics.set_charge_topic)

def on_message(client, userdata, message):
    payload = str(message.payload, 'utf8')

    match message.topic:
        case topics.set_charge_topic:
            set_charge(payload)
