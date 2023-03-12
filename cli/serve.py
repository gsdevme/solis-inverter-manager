import uvicorn


def serve():
    uvicorn.run("api:app", host="0.0.0.0", port=8000, reload=False, log_level="debug", workers=1, limit_concurrency=3)
