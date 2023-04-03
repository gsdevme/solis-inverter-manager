import typer
import logging
from environs import Env

from cli.meter import read_meter
from cli.serve import serve
from cli.set_charge import set_charge
from cli.publish import publish_mqtt
from solis.modbus import Modbus

env = Env()
app = typer.Typer()
app.command()(read_meter)
app.command()(set_charge)
app.command()(publish_mqtt)
app.command()(serve)

if __name__ == '__main__':
    env.read_env()

    logging.basicConfig(level="INFO")

    Modbus().set_connection_settings(
        env.str("INVERTER_IP"),
        env.int("INVERTER_SERIAL"),
        env.int("INVERTER_PORT")
    )

    app()
