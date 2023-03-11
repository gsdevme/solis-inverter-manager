from solis.inverter import Inverter
from solis.modbus import Modbus
from rich import print

def read_battery():
    print(Inverter(Modbus()).read_battery())

