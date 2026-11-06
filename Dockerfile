# syntax=docker/dockerfile:1
# lighthouse-light multi-stage build.
#
# The build context MUST be the go-oidfed/ parent directory:
#   docker build -f lighthouse-light/Dockerfile -t oidfed/lighthouse-light ..
# (the lighthouse module is a local replace target; lib too, see go.mod).

# --- builder ---
FROM golang:1.27-bookworm AS builder
WORKDIR /build

# Copy the three modules of the workspace. The context root is go-oidfed/.
COPY lighthouse-light ./lighthouse-light
COPY lighthouse ./lighthouse
COPY lib ./lib

WORKDIR /build/lighthouse-light

# CGO is required: importing lighthouse/storage links gorm sqlite (mattn/go-sqlite3).
RUN CGO_ENABLED=1 GOFLAGS=-mod=mod go build -o /lighthouse-light ./cmd/lighthouse-light

# --- runtime ---
FROM debian:stable-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /lighthouse-light /lighthouse-light
COPY lighthouse-light/docker-entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 7672
ENTRYPOINT ["/entrypoint.sh"]
