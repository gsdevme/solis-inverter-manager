# syntax=docker/dockerfile:1

# Go manager image. Two-stage: build a static binary against the toolchain that
# matches go.mod (go 1.27.0), then ship it on distroless/static:nonroot so the
# runtime carries no shell, package manager, or libc — only the binary and CA
# certs, run as an unprivileged user. Build the sidecar image separately from
# sidecar/Dockerfile.

# --- build stage ---
FROM golang:1.27 AS build
WORKDIR /src

# Cache dependencies in their own layer so source edits don't re-download modules.
COPY go.mod go.sum ./
RUN go mod download

# Static build: CGO off so the binary needs no libc at runtime; -trimpath and
# -s -w strip local paths and debug/symbol info for a smaller, reproducible image.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags "-s -w" \
    -o /out/solis-inverter-manager ./cmd

# --- runtime stage ---
FROM gcr.io/distroless/static:nonroot
WORKDIR /

COPY --from=build /out/solis-inverter-manager /solis-inverter-manager

# Prod default targets the real inverter via the sidecar (SIDECAR_URL). 8080 is
# the health/probe port (HEALTH_ADDR default :8080).
ENV MODE=live
EXPOSE 8080

USER nonroot:nonroot
ENTRYPOINT ["/solis-inverter-manager"]
CMD ["serve"]
