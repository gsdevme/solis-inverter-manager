# Thin Solarman V5 transport sidecar. Build from the repo root so the Phase 0
# fixtures can be copied alongside the package for MODE=mock:
#   docker build -f sidecar/Dockerfile -t solis-sidecar .
FROM python:3.12-slim

WORKDIR /app

# Install runtime deps first for layer caching (only pysolarmanv5).
COPY sidecar/requirements.txt ./sidecar/requirements.txt
RUN pip install --no-cache-dir -r sidecar/requirements.txt

# The package and the fixtures the MODE=mock default seed resolves to; the layout
# under /app mirrors the repo so the relative MOCK_FIXTURE path resolves.
COPY sidecar/ ./sidecar/
COPY docs/phase0/fixtures/ ./docs/phase0/fixtures/

EXPOSE 8081

ENTRYPOINT ["python", "-m", "sidecar"]
