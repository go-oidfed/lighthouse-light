# syntax=docker/dockerfile:1
# lighthouse-light multi-stage build.
#
# Self-contained: the build context is the lighthouse-light repo root.
# All deps resolve through the Go module proxy (go.mod pins
# github.com/go-oidfed/lighthouse and github.com/go-oidfed/lib), so no
# neighboring modules are required:
#   docker build -t oidfed/lighthouse-light .

# --- builder ---
FROM golang:1.27-bookworm AS builder
WORKDIR /build

COPY . ./
RUN go mod download

# CGO is required: importing lighthouse/storage links gorm sqlite (mattn/go-sqlite3).
RUN CGO_ENABLED=1 go build -o /lighthouse-light ./cmd/lighthouse-light

# --- runtime ---
FROM debian:stable-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /lighthouse-light /lighthouse-light
COPY docker-entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 7672
ENTRYPOINT ["/entrypoint.sh"]
