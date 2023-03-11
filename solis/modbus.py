from pysolarmanv5.pysolarmanv5 import PySolarmanV5, NoSocketAvailableError, V5FrameError
from retry import retry
import struct


class Modbus:
    __instance = None
    __ip: str
    __serial: int
    __port: int
    __solarman: PySolarmanV5
    __has_connected: bool

    def __new__(cls, *args, **kwargs):
        if not Modbus.__instance:
            Modbus.__instance = object.__new__(cls)
        return Modbus.__instance

    def __init__(self):
        self.__has_connected = False

    def set_connection_settings(self, ip: str, serial: int, port: int):
        self.__ip = ip
        self.__port = port
        self.__serial = serial

    @retry(V5FrameError, tries=3, delay=10)
    @retry(struct.error, tries=3, delay=10)
    def read_input_registers(self, addr: int, quantity: int):
        self.connect_if_required()

        try:
            return self.__solarman.read_input_registers(addr, quantity)
        except Exception as e:
            self.disconnect()

            raise e

    @retry(V5FrameError, tries=3, delay=10)
    @retry(struct.error, tries=3, delay=10)
    def read_holding_registers(self, addr: int, quantity: int):
        self.connect_if_required()

        try:
            return self.__solarman.read_holding_registers(addr, quantity)
        except Exception as e:
            self.disconnect()

            raise e

    @retry(V5FrameError, tries=3, delay=10)
    @retry(struct.error, tries=3, delay=10)
    def write_holding_register(self, addr: int, quantity: int):
        self.connect_if_required()

        try:
            return self.__solarman.write_holding_register(addr, quantity)
        except Exception as e:
            self.disconnect()

            raise e

    def connect_if_required(self):
        if self.__has_connected:
            return

        try:
            self.__solarman = PySolarmanV5(address=self.__ip, serial=self.__serial, port=self.__port, mb_slave_id=1,
                                           verbose=False, socket_timeout=10)

            self.__has_connected = True
        except NoSocketAvailableError as e:
            if not self.__has_connected:
                raise e

    def disconnect(self):
        self.__has_connected = False
