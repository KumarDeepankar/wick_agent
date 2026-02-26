# wick_go server container
#
# Builds both wick_go (server) and wickfs (sandbox filesystem tool).
# wickfs lives alongside wick_go so it can be injected into sandbox
# containers via `docker cp` at launch time.
#
# Build (from server/ directory):
#   docker build -t wick-server .
#
# Run with Docker socket mounted:
#   docker run -v /var/run/docker.sock:/var/run/docker.sock \
#              -p 8000:8000 wick-server
#
# With config (mount agents.yaml and skills):
#   docker run -v /var/run/docker.sock:/var/run/docker.sock \
#              -v ./agents.yaml:/app/agents.yaml \
#              -v ./skills:/app/skills \
#              -p 8000:8000 wick-server -config /app/agents.yaml

# ── Stage 1: build Go binaries ───────────────────────────────────────────────
FROM golang:1.24-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /wick_go .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /wickfs ./cmd/wickfs/

# ── Stage 2: runtime ─────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache ca-certificates docker-cli

# Server binary
COPY --from=builder /wick_go /usr/local/bin/wick_go

# wickfs binary — used for injection into sandbox containers.
COPY --from=builder /wickfs /usr/local/bin/wickfs

WORKDIR /app

# Copy static UI assets (built separately, placed in server/static/)
COPY --from=builder /src/static/ /app/static/

EXPOSE 8000

ENTRYPOINT ["wick_go"]
