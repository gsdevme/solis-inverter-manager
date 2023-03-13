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

