from environs import Env
import typer

from cli.battery import read_battery
from cli.charging import read_charging

env = Env()
app = typer.Typer()
app.command()(read_battery)
app.command()(read_charging)

# MODBUS_REGISTER_OPTIMAL_INCOME = 43110
#
# MODBUS_REGISTER_OPTIMAL_INCOME_STOP = 33
# MODBUS_REGISTER_OPTIMAL_INCOME_RUN = 35

if __name__ == '__main__':
    env.read_env()

    app()

    # try:
    #     logging.info('Connecting to inverter')
    #
    #     modbus = PySolarmanV5(
    #         env.str("INVERTER_IP"), env.int("INVERTER_SERIAL"), port=env.int("INVERTER_PORT"),
    #         mb_slave_id=1, verbose=env.bool("DEBUG"), socket_timeout=env.int("INVERTER_SOCKET_TIMEOUT"))
    # except Exception as e:
    #     e.add_note('Connection to Solis modbus failed')
    #     raise e
    #
    # #modbus.write_holding_register(register_addr=MODBUS_REGISTER_OPTIMAL_INCOME, value=MODBUS_REGISTER_OPTIMAL_INCOME_STOP)
    #
    # current_income_level = modbus.read_holding_registers(register_addr=MODBUS_REGISTER_OPTIMAL_INCOME, quantity=1)[0]
    #
    # if current_income_level == MODBUS_REGISTER_OPTIMAL_INCOME_RUN:
    #     print("Battery optimal income setting is RUN")
    # else:
    #     print("Battery optimal income setting is STOP")