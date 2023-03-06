from fastapi import FastAPI
from environs import Env
from solis.inverter import Inverter

app = FastAPI()
env = Env()
env.read_env()

@app.get("/api/battery")
def read_api_battery():
    battery = Inverter(
        env.str("INVERTER_IP"),
        env.int("INVERTER_SERIAL"),
        env.int("INVERTER_PORT")
    ).read_battery()

    return {"battery": battery}