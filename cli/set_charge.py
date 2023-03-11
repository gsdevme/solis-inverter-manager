from solis.inverter import Inverter
from solis.modbus import Modbus

def set_charge(amps: float):
    inverter = Inverter(Modbus())
    inverter.set_grid_charging_amps(amps)

