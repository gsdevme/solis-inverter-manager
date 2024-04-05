import typer
import logging
from environs import Env

from cli.meter import ReadMeter
from cli.set_charge import SetCharge
from cli.set_discharge import SetDischarge
from cli.publish import PublishMqtt
from cli.events import HandleEvents
from cli.all import ReadAll
from solis.modbus import Modbus


if __name__ == '__main__':
    env = Env()
    env.read_env()

    logging.basicConfig(level="INFO")

    modbus = Modbus(
        env.str("INVERTER_IP"),
        env.int("INVERTER_SERIAL"),
        env.int("INVERTER_PORT")
    )

    app = typer.Typer()
    app.command(name="read_meter")(ReadMeter(modbus))
    app.command(name="set_charge")(SetCharge(modbus))
    app.command(name="set_discharge")(SetDischarge(modbus))
    app.command(name="publish_mqtt")(PublishMqtt(modbus, env.int("INVERTER_SERIAL"), env.str("MQTT_HOST")))
    app.command(name="handle_events")(HandleEvents(modbus, env.str("MQTT_HOST")))
    app.command(name="read_all")(ReadAll(modbus))

    app()
