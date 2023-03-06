from environs import Env
import typer

from cli.battery import read_battery
from cli.time_of_use import toggle_time_of_use
from cli.set_charge import set_charge

env = Env()
app = typer.Typer()
app.command()(read_battery)
app.command()(toggle_time_of_use)
app.command()(set_charge)

if __name__ == '__main__':
    env.read_env()

    app()