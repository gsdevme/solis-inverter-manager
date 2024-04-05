from solis.inverter import Inverter
from solis.modbus import Modbus
from rich import print


class ReadMeter:
    __modbus: Modbus

    def __init__(self, modbus: Modbus):
        self.__modbus = modbus

    def __call__(self):
        print(Inverter(self.__modbus).read_meter())
