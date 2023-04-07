from solis.inverter import Inverter
from solis.modbus import Modbus
from rich import print


def read_all():
    print(Inverter(Modbus()).poll())
