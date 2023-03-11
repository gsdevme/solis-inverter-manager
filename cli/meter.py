from solis.inverter import Inverter
from solis.modbus import Modbus
from rich import print


def read_meter():
    print(Inverter(Modbus()).read_meter())
