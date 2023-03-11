import typer
from environs import Env

from cli.meter import read_meter
from cli.serve import serve
from cli.set_charge import set_charge
from solis.modbus import Modbus

env = Env()
app = typer.Typer()
app.command()(read_meter)
app.command()(set_charge)
app.command()(serve)

if __name__ == '__main__':
    env.read_env()

    Modbus().set_connection_settings(
        env.str("INVERTER_IP"),
        env.int("INVERTER_SERIAL"),
        env.int("INVERTER_PORT")
    )

    app()
