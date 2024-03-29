import solis.modbus as Modbus
import datetime as date

# Registers
METER_REGISTER = [33126, 25]
OPTIMAL_INCOME_REGISTER = 43110
GRID_CHARGE = [43141, 10]
DISCHARGE_GRID_CHARGE = 43142
SYSTEM_REGISTER = [33022, 19]
DC_INPUT = [33049, 10]

# Data Points
SYSTEM_REGISTER_PV_TODAY = 13
SYSTEM_REGISTER_PV_YESTERDAY = 14

SYSTEM_REGISTER_PV_THIS_MONTH_1 = 9
SYSTEM_REGISTER_PV_THIS_MONTH_2 = 10

SYSTEM_REGISTER_PV_LAST_MONTH_1 = 11
SYSTEM_REGISTER_PV_LAST_MONTH_2 = 12

METER_REGISTER_CHARGE_PERCENTAGE = 13
METER_REGISTER_HEALTH = 14
METER_REGISTER_VOLTAGE = 7
METER_REGISTER_BMS_VOLTAGE = 15

METER_REGISTER_BATTERY_POWER_1 = 23
METER_REGISTER_BATTERY_POWER_2 = 24

METER_REGISTER_BATTERY_POWER_AMPS = 8
METER_REGISTER_STATE = 9

# WRITABLE REGISTER VALUES
OPTIMAL_INCOME_STOP_REGISTER_VALUE = 33
OPTIMAL_INCOME_RUN_REGISTER_VALUE = 35


class Inverter:

    def __init__(self, modbus: Modbus):
        self.__modbus = modbus

        pass

    def poll(self):
        meter = self.read_meter()
        pv = self.read_pv_yield()

        to_battery = int(meter['battery']['power_to_the_battery'])

        if to_battery <= 0:
            to_battery = abs(to_battery)
        else:
            to_battery = 0

        return {
            "meter": meter,
            "grid_charge": self.read_grid_charge(),
            "pv": pv,
            "summary": {
                "pv_self_consumption": pv['pv_now'] - (meter['grid_export'] + to_battery),
                "inverter_generation": pv['pv_now'] - meter['grid_export'],
            },
        }

    def read_pv_yield(self):
        system = self.__modbus.read_input_registers(SYSTEM_REGISTER[0], SYSTEM_REGISTER[1])

        datetime = date.datetime(2000 + int(system[0]), int(system[1]), int(system[2]), int(system[3]), int(system[4]),
                                 int(system[5]))
        dc = self.__modbus.read_input_registers(DC_INPUT[0], DC_INPUT[1])

        return {
            "datetime": datetime,
            "pv_now": int(dc[9]) + int(dc[8]),
            "panels": {
                "voltage": {
                    "pv1": int(dc[0]) / 10,
                    "pv2": int(dc[2]) / 10,
                },
                "current": {
                    "pv1": int(dc[1]) / 10,
                    "pv2": int(dc[3]) / 10,
                },
            },
            "pv_yield_today": int(system[SYSTEM_REGISTER_PV_TODAY]) / 10,
            "pv_yield_this_month": int(system[SYSTEM_REGISTER_PV_THIS_MONTH_1]) + int(
                system[SYSTEM_REGISTER_PV_THIS_MONTH_2]),
            "pv_yield_yesterday": int(system[SYSTEM_REGISTER_PV_YESTERDAY]) / 10,
            "pv_yield_last_month": int(system[SYSTEM_REGISTER_PV_LAST_MONTH_1]) + int(
                system[SYSTEM_REGISTER_PV_LAST_MONTH_2]),
        }

    def read_grid_charge(self):
        grid_charge = self.__modbus.read_holding_registers(GRID_CHARGE[0], GRID_CHARGE[1])
        optimal_income = self.__modbus.read_holding_registers(OPTIMAL_INCOME_REGISTER, 1)

        now = date.datetime.now()
        charge_end = date.datetime(now.year, now.month, now.day, int(grid_charge[4]), int(grid_charge[5]))
        charge_start = date.datetime(now.year, now.month, now.day, int(grid_charge[2]), int(grid_charge[3]))

        discharge_end = date.datetime(now.year, now.month, now.day, int(grid_charge[8]), int(grid_charge[9]))
        discharge_start = date.datetime(now.year, now.month, now.day, int(grid_charge[6]), int(grid_charge[7]))

        return {
            "grid_charge_optimal_income": optimal_income[0] == OPTIMAL_INCOME_RUN_REGISTER_VALUE,
            "grid_charging_amps": int(grid_charge[0]) / 10,
            "grid_discharging_amps": int(grid_charge[1]) / 10,
            "grid_charge_start": charge_start.isoformat(),
            "grid_charge_end": charge_end.isoformat(),
            "grid_discharge_start": discharge_start.isoformat(),
            "grid_discharge_end": discharge_end.isoformat(),
        }

    def read_meter(self):
        meter = self.__modbus.read_input_registers(METER_REGISTER[0], METER_REGISTER[1])
        charging = (True, False)[meter[METER_REGISTER_STATE]]
        power_watts = int(meter[METER_REGISTER_BATTERY_POWER_1]) + int(meter[METER_REGISTER_BATTERY_POWER_2])
        power_amps = int(meter[METER_REGISTER_BATTERY_POWER_AMPS])

        if charging:
            to_battery = power_watts

            power_watts = -abs(power_watts)
            power_amps = -abs(power_amps)

            from_battery = 0
        else:
            from_battery = power_watts
            to_battery = 0

        grid_export = 0
        grid_import = 0

        # oddly the modbus sometimes seems to be confused with zero
        # safe guard to ensure its below 100 amps (uk mainhead)
        if int(meter[4]) <= 24000:
            grid_export = int(meter[5])
            grid_import = int(meter[4])

        return {
            "battery": {
                "percentage": meter[METER_REGISTER_CHARGE_PERCENTAGE],
                "health": meter[METER_REGISTER_HEALTH],
                "energy_storage_mode": meter[6],
                "voltage": int(meter[METER_REGISTER_VOLTAGE]) / 10,
                "bms_voltage": int(meter[METER_REGISTER_BMS_VOLTAGE]) / 100,
                "battery_power": power_watts,
                "power_from_the_battery": from_battery,
                "power_to_the_battery": to_battery,
                "battery_power_amps": power_amps / 10,
                "charging": charging,
            },
            "grid_export": grid_export,
            "grid_return": -abs(grid_export),
            "grid_import": grid_import,
        }

    def turn_on_optimal_income(self):
        self.__modbus.write_holding_register(
            OPTIMAL_INCOME_REGISTER,
            OPTIMAL_INCOME_RUN_REGISTER_VALUE
        )

    def turn_off_optimal_income(self):
        self.__modbus.write_holding_register(
            OPTIMAL_INCOME_REGISTER,
            OPTIMAL_INCOME_STOP_REGISTER_VALUE
        )

    def set_grid_charging_amps(self, amps):
        """
        :type amps float
        """
        self.__modbus.write_holding_register(GRID_CHARGE[0], int(round(amps, 1) * 10))

    def set_grid_discharging_amps(self, amps):
        """
        :type amps float
        """
        self.__modbus.write_holding_register(DISCHARGE_GRID_CHARGE, int(round(amps, 1) * 10))
