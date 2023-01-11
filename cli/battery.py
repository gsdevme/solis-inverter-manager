from environs import Env
from solis.inverter import Inverter
from rich import print

def read_battery():
    env = Env()

    print("[green]Attempting to read batter information from the inverter[/green]")

    percentage = Inverter(
        env.str("INVERTER_IP"),
        env.int("INVERTER_SERIAL"),
        env.int("INVERTER_PORT")
    ).read_battery()

    print(percentage)

