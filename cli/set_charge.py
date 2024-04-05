from solis.inverter import Inverter
from solis.modbus import Modbus


class SetCharge:
    __modbus: Modbus

    def __init__(self, modbus: Modbus):
        self.__modbus = modbus

    def __call__(self, amps: float):
        inverter = Inverter(self.__modbus)
        inverter.set_grid_charging_amps(amps)
