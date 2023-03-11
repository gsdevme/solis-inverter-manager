from fastapi import FastAPI
from environs import Env
from solis.inverter import Inverter
from solis.modbus import Modbus

app = FastAPI()
env = Env()
env.read_env()

@app.get("/api/metrics")
def read_api_metrics():
    return Inverter(Modbus()).poll()


@app.get("/api/battery")
def read_api_battery():
    battery = Inverter(Modbus()).read_battery()

    return {"battery": battery}


@app.get("/api/pv")
def read_api_battery():
    pv = Inverter(Modbus()).read_pv_yield()

    return {"pv": pv}
