import solis.modbus as Modbus
import datetime as date

# Registers
BATTERY_REGISTER = [33126, 25]
OPTIMAL_INCOME_REGISTER = 43110
GRID_CHARGE = [43141, 10]
SYSTEM_REGISTER = [33022, 19]
DC_INPUT = [33049, 10]

# Data Points
SYSTEM_REGISTER_PV_TODAY = 13
SYSTEM_REGISTER_PV_YESTERDAY = 14

SYSTEM_REGISTER_PV_THIS_MONTH_1 = 9
SYSTEM_REGISTER_PV_THIS_MONTH_2 = 10

SYSTEM_REGISTER_PV_LAST_MONTH_1 = 11
SYSTEM_REGISTER_PV_LAST_MONTH_2 = 12

BATTERY_REGISTER_CHARGE_PERCENTAGE = 13
BATTERY_REGISTER_HEALTH = 14
BATTERY_REGISTER_VOLTAGE = 7
BATTERY_REGISTER_BMS_VOLTAGE = 15

BATTERY_REGISTER_BATTERY_POWER_1 = 23
BATTERY_REGISTER_BATTERY_POWER_2 = 24

BATTERY_REGISTER_BATTERY_POWER_AMPS = 8
BATTERY_REGISTER_STATE = 9

# WRITABLE REGISTER VALUES
OPTIMAL_INCOME_STOP_REGISTER_VALUE = 33
OPTIMAL_INCOME_RUN_REGISTER_VALUE = 35


class Inverter:

    def __init__(self, modbus: Modbus):
        self.__modbus = modbus

        pass

    def poll(self):
        return {
            "battery": self.read_battery(),
            "grid_charge": self.read_grid_charge(),
            "pv": self.read_pv_yield(),
        }

    def read_pv_yield(self):
        system = self.__modbus.read_input_registers(SYSTEM_REGISTER[0], SYSTEM_REGISTER[1])
        dc = self.__modbus.read_input_registers(DC_INPUT[0], DC_INPUT[1])

        return {
            "pv_watts_now": int(dc[9]) + int(dc[8]),
            "pv_yield_today": int(system[SYSTEM_REGISTER_PV_TODAY]) / 10,
            "pv_yield_this_month": int(system[SYSTEM_REGISTER_PV_THIS_MONTH_1]) + int(system[SYSTEM_REGISTER_PV_THIS_MONTH_2]),
            "pv_yield_yesterday": int(system[SYSTEM_REGISTER_PV_YESTERDAY]) / 10,
            "pv_yield_last_month": int(system[SYSTEM_REGISTER_PV_LAST_MONTH_1]) + int(system[SYSTEM_REGISTER_PV_LAST_MONTH_2]),
        }

    def read_grid_charge(self):
        grid_charge = self.__modbus.read_holding_registers(GRID_CHARGE[0], GRID_CHARGE[1])

        now = date.datetime.now()
        charge_end = date.datetime(now.year, now.month, now.day, int(grid_charge[4]), int(grid_charge[3]))
        charge_start = date.datetime(now.year, now.month, now.day, int(grid_charge[6]), int(grid_charge[5]))

        return {
            "grid_charging_amps": int(grid_charge[0]) / 10,
            "grid_charge_start": charge_start.isoformat(),
            "grid_charge_end": charge_end.isoformat(),
            "grid_discharging_amps": int(grid_charge[1]) / 10,
        }

    def read_battery(self):
        battery = self.__modbus.read_input_registers(BATTERY_REGISTER[0], BATTERY_REGISTER[1])

        return {
            "percentage": battery[BATTERY_REGISTER_CHARGE_PERCENTAGE],
            "health": battery[BATTERY_REGISTER_HEALTH],
            "voltage": int(battery[BATTERY_REGISTER_VOLTAGE]) / 10,
            "bms_voltage": int(battery[BATTERY_REGISTER_BMS_VOLTAGE]) / 100,
            "battery_power_in_watts": int(battery[BATTERY_REGISTER_BATTERY_POWER_1]) + int(battery[BATTERY_REGISTER_BATTERY_POWER_2]),
            "battery_power_in_amps": int(battery[BATTERY_REGISTER_BATTERY_POWER_AMPS]) / 10,
            "charging": (True, False)[battery[BATTERY_REGISTER_STATE]],
        }

    # def turn_on_time_of_use(self):
    #     self.connect_if_required()
    #
    #     self._solarman.write_holding_register(
    #         register_addr=OPTIMAL_INCOME_REGISTER,
    #         value=OPTIMAL_INCOME_RUN_REGISTER_VALUE
    #     )
    #
    # def turn_off_time_of_use(self):
    #     self.connect_if_required()
    #
    #     self._solarman.write_holding_register(
    #         register_addr=OPTIMAL_INCOME_REGISTER,
    #         value=OPTIMAL_INCOME_STOP_REGISTER_VALUE
    #     )
    #

    def set_grid_charging_amps(self, amps):
        """
        :type amps float
        """
        self.__modbus.write_holding_register(GRID_CHARGE[0], int(amps * 10))
