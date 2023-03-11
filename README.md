# Solis Inverter Manager

# Metrics endpoint

```json
{
  "battery": {
    "percentage": 89,
    "health": 100,
    "voltage": 51.2,
    "bms_voltage": 50.51,
    "battery_power_in_watts": 742,
    "battery_power_in_amps": 14.5,
    "charging": true
  },
  "grid_charge": {
    "grid_charging_amps": 45.0,
    "grid_charge_start": "2023-03-11T00:29:00",
    "grid_charge_end": "2023-03-11T04:31:00",
    "grid_discharging_amps": 0.0
  },
  "pv": {
    "pv_watts_now": 1275,
    "pv_yield_today": 2.2,
    "pv_yield_this_month": 117,
    "pv_yield_yesterday": 23.7,
    "pv_yield_last_month": 163
  }
}
```

# Usage

```bash
/app # python main.py serve
INFO:     Started server process [197]
INFO:     Waiting for application startup.
INFO:     Application startup complete.
INFO:     Uvicorn running on http://0.0.0.0:8000 (Press CTRL+C to quit)
INFO:     172.25.0.1:48920 - "GET /api/metrics HTTP/1.1" 200 OK
INFO:     172.25.0.1:40948 - "GET /api/battery HTTP/1.1" 200 OK
INFO:     172.25.0.1:40948 - "GET /api/pv HTTP/1.1" 200 OK

/app # python main.py read-battery
{'percentage': 87, 'health': 100, 'voltage': 51.5, 'bms_voltage': 50.66, 'battery_power_in_watts': 1220, 'battery_power_in_amps': 23.7, 'charging': True}
```

# Screenshots
![Screenshot 2023-03-11 at 10 15 03](https://user-images.githubusercontent.com/319498/224479150-0726bca7-7c46-450d-b5c3-60a8ed2e34d5.png)
![Screenshot 2023-03-11 at 10 14 56](https://user-images.githubusercontent.com/319498/224479152-5b5e8af3-256b-4f75-9dac-c40552e66027.png)
![Screenshot 2023-03-11 at 10 14 48](https://user-images.githubusercontent.com/319498/224479153-b08c7036-d5af-46a3-9098-1839060977b5.png)
![Screenshot 2023-03-11 at 10 14 43](https://user-images.githubusercontent.com/319498/224479154-34827b1d-8f93-43b5-827a-f6b5f704a082.png)

![Screenshot 2023-03-11 at 10 26 48](https://user-images.githubusercontent.com/319498/224479166-3f659c57-8f44-4225-a238-8efe0f166de8.png)



# Development

```
# start the dev environment
make start

# enter the shell
make shell

# install the deps
make install
```
