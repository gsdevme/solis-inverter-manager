from environs import Env
from solis.inverter import Inverter
from rich import print

def set_charge(amps: float):
    env = Env()

    inverter = Inverter(
        env.str("INVERTER_IP"),
        env.int("INVERTER_SERIAL"),
        env.int("INVERTER_PORT")
    )

    inverter.set_charging_amps(amps)

