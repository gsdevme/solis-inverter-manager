# Solis Inverter Manager

## Running

```bash
# Quickly running read-all will confirm modbus connections are working
docker run --rm \
-e INVERTER_SERIAL=1111 \
-e INVERTER_IP=127.0.0.1 \
gsdevme/solis-inverter-manager:latest read-all 

# Running the poller/mqtt publisher
docker run --rm \
-e INVERTER_SERIAL=1111 \
-e INVERTER_IP=127.0.0.1 \
-e MQTT_HOST=127.0.0.1 \
gsdevme/solis-inverter-manager:latest
```

<img width="510" alt="Screenshot 2023-04-09 at 20 21 31" src="https://user-images.githubusercontent.com/319498/230792453-fc59532c-34b2-4f45-b341-40e12b425764.png">
<img width="499" alt="Screenshot 2023-04-09 at 20 21 25" src="https://user-images.githubusercontent.com/319498/230792454-825c0761-5ec5-4405-a7a3-a9d1a2e693e3.png">

```yaml
action:
  - service: mqtt.publish
    data:
      qos: 0
      topic: solar_inverter_manager/set_charge
      payload_template: "25"
```


# Metrics endpoint

```json
{
  "meter": {
    "battery": {
      "percentage": 98,
      "health": 100,
      "voltage": 50.1,
      "bms_voltage": 49.7,
      "battery_power": 235,
      "power_from_the_battery": 235,
      "power_to_the_battery": 0,
      "battery_power_amps": 4.7,
      "charging": false
    },
    "grid_export": 0,
    "grid_return": 0,
    "grid_import": 0
  },
  "grid_charge": {
    "grid_charging": 45.0,
    "grid_charge_start": "2023-03-11T00:29:00",
    "grid_charge_end": "2023-03-11T04:31:00",
    "grid_discharging_amps": 0.0
  },
  "pv": {
    "pv_now": 117,
    "panels": {
      "voltage": {
        "pv1": 195.5,
        "pv2": 195.1
      },
      "current": {
        "pv1": 0.3,
        "pv2": 0.3
      }
    },
    "pv_yield_today": 9.8,
    "pv_yield_this_month": 125,
    "pv_yield_yesterday": 23.7,
    "pv_yield_last_month": 163
  },
  "summary": {
    "pv_self_consumption": 117,
    "inverter_generation": 352
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
