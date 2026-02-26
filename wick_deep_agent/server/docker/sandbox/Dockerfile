# wick-sandbox: pre-baked sandbox image with wickfs binary.
#
# Build:
#   docker build -t wick-sandbox -f docker/sandbox/Dockerfile .
#
# The build context must be the server/ directory so it can access
# Go source for wickfs compilation.
#
# Usage in agents.yaml:
#   backend:
#     type: docker
#     image: wick-sandbox      # uses this pre-baked image
#
# For custom base images, override the BASE_IMAGE build arg:
#   docker build --build-arg BASE_IMAGE=node:20-slim -t wick-sandbox-node .

ARG BASE_IMAGE=python:3.11-slim

# ── Stage 1: build wickfs ────────────────────────────────────────────────────
FROM golang:1.24-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /wickfs ./cmd/wickfs/

# ── Stage 2: sandbox runtime ─────────────────────────────────────────────────
FROM ${BASE_IMAGE}

# Install common utilities useful for sandboxed code execution
RUN apt-get update && apt-get install -y --no-install-recommends \
        git \
        curl \
        jq \
    && rm -rf /var/lib/apt/lists/*

# Copy wickfs binary from builder
COPY --from=builder /wickfs /usr/local/bin/wickfs
RUN chmod +x /usr/local/bin/wickfs

# Default workspace
WORKDIR /workspace

# Container stays alive for docker exec
CMD ["sleep", "infinity"]
