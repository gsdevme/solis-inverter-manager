from pysolarmanv5.pysolarmanv5 import PySolarmanV5, NoSocketAvailableError, V5FrameError
from retry import retry

__BATTERY_REGISTER__ = 33126
__OPTIMAL_INCOME_REGISTER__ = 43110
__CHARGE_AMPS__ = 43141

__OPTIMAL_INCOME_STOP_REGISTER_VALUE__ = 33
__OPTIMAL_INCOME_RUN_REGISTER_VALUE__ = 35

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

    @retry(V5FrameError, tries=3, delay=5)
    def read_battery(self):
        if not self._connected:
            self.connect()

        register = self._solarman.read_input_registers(register_addr=__BATTERY_REGISTER__, quantity=25)
        charging_amps = self._solarman.read_holding_registers(register_addr=__CHARGE_AMPS__, quantity=1)

        return {
            "percentage": register[13],
            "health": register[14],
            "charging": (True, False)[register[9]],
            "charging_amps": charging_amps[0] / 10
        }

    @retry(V5FrameError, tries=3, delay=5)
    def turn_on_time_of_use(self):
        if not self._connected:
            self.connect()

        self._solarman.write_holding_register(
            register_addr=__OPTIMAL_INCOME_REGISTER__,
            value=__OPTIMAL_INCOME_RUN_REGISTER_VALUE__
        )

    @retry(V5FrameError, tries=3, delay=5)
    def turn_off_time_of_use(self):
        if not self._connected:
            self.connect()

        self._solarman.write_holding_register(
            register_addr=__OPTIMAL_INCOME_REGISTER__,
            value=__OPTIMAL_INCOME_STOP_REGISTER_VALUE__
        )

    @retry(V5FrameError, tries=3, delay=5)
    def set_charging_amps(self, amps):
        """
        :type amps float
        """
        if not self._connected:
            self.connect()

        self._solarman.write_holding_register(
            register_addr=__CHARGE_AMPS__,
            value=int(amps * 10)
        )

    def connect(self):
        if self._connected:
            return

        try:
            self._solarman = PySolarmanV5(address=self._ip, serial=self._serial, port=self._port, mb_slave_id=1,
                                          verbose=False, socket_timeout=10)

            self._connected = True
        except V5FrameError as e:
            # todo re-raise this as a local exception
            raise e
