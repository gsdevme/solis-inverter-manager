from solis.inverter import Inverter
from solis.modbus import Modbus


def set_discharge(amps: float):
    inverter = Inverter(Modbus())
    inverter.set_grid_discharging_amps(amps)
