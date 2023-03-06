from environs import Env
from solis.inverter import Inverter
from rich import print

def toggle_time_of_use(enable: bool = False):
    env = Env()

    inverter = Inverter(
        env.str("INVERTER_IP"),
        env.int("INVERTER_SERIAL"),
        env.int("INVERTER_PORT")
    )

    if enable:
        print("[blue]Enabling[/blue][green] time of use schedule[/green]")

        inverter.turn_on_time_of_use()
    else:
        print("[red]Disabling[/red][green] time of use schedule[/green]")

        inverter.turn_off_time_of_use()

