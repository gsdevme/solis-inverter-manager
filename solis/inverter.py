from pysolarmanv5.pysolarmanv5 import PySolarmanV5, NoSocketAvailableError

__BATTERY_REGISTER__ = 33126


class Inverter:
    _ip: str
    _serial: int
    _solarman: PySolarmanV5
    _connected: bool

    def __init__(self, ip: str, serial: int, port: int):
        self._ip = ip
        self._serial = serial
        self._port = port
        self._connected = False

        pass

    def read_battery(self):
        if not self._connected:
            self.connect()

        register = self._solarman.read_input_registers(register_addr=__BATTERY_REGISTER__, quantity=25)

        return {
            "percentage": register[13],
            "health": register[14],
            "charging": (True, False)[register[9]]
        }

    def connect(self):
        if self._connected:
            return

        try:
            self._solarman = PySolarmanV5(address=self._ip, serial=self._serial, port=self._port, mb_slave_id=1,
                                          verbose=False, socket_timeout=10)

            self._connected = True
        except NoSocketAvailableError as e:
            # todo re-raise this as a local exception
            raise e
