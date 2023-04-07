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


def set_optimal_income(payload):
    try:
        enable = payload == "1" or payload == "true"
    except ValueError:
        return

    if enable:
        logging.getLogger("manager").info("enabling optimal income")

        Inverter(Modbus()).turn_on_optimal_income()
    else:
        logging.getLogger("manager").info("disabling optimal income")

        Inverter(Modbus()).turn_off_optimal_income()

def on_connect(client, userdata, flags, rc):
    client.subscribe(topics.set_charge_topic)
    client.subscribe(topics.set_optimal_income_topic)


def on_message(client, userdata, message):
    payload = str(message.payload, 'utf8')

    match message.topic:
        case topics.set_charge_topic:
            set_charge(payload)
        case topics.set_optimal_income_topic:
            set_optimal_income(payload)
