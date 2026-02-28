# =============================================================================
# wick_go — Containerized AI Agent Server (pure Go)
#
# Build (from repo root):
#   docker build -f wick_go/Dockerfile -t wick-agent .
#
# Run (local mode only):
#   docker run -p 8000:8000 wick-agent
#
# Run (with remote/docker sandbox support):
#   docker run -p 8000:8000 -v /var/run/docker.sock:/var/run/docker.sock wick-agent
# =============================================================================

# Stage 1: Build Go binaries (wick_go + wickfs + wick-daemon for Linux)
FROM golang:1.24-alpine AS go-builder
WORKDIR /src

# Copy server library first (dependency)
COPY wick_deep_agent/server/ ./wick_deep_agent/server/

# Copy wick_go application
COPY wick_go/go.mod wick_go/go.sum ./wick_go/
COPY wick_go/main.go ./wick_go/

# Build the main application binary
WORKDIR /src/wick_go
RUN go build -o /wick_go .

# Build wickfs CLI (injected into sandbox containers for file operations)
WORKDIR /src/wick_deep_agent/server
RUN CGO_ENABLED=0 GOOS=linux go build -o /wickfs ./cmd/wickfs/

# Build wick-daemon (injected into sandbox containers for fast exec)
RUN CGO_ENABLED=0 GOOS=linux go build -o /wick-daemon ./cmd/wickdaemon/

# Stage 2: Build UI static assets
FROM node:20-alpine AS ui-builder
WORKDIR /build
COPY wick_go/ui/package.json wick_go/ui/package-lock.json* ./
RUN npm ci --ignore-scripts
COPY wick_go/ui/ .
RUN npm run build -- --outDir /static

# Stage 3: Runtime
FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates curl gnupg \
        python3-minimal && \
    install -m 0755 -d /etc/apt/keyrings && \
    curl -fsSL https://download.docker.com/linux/debian/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/debian $(. /etc/os-release && echo $VERSION_CODENAME) stable" > /etc/apt/sources.list.d/docker.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends docker-ce-cli && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Go binary
COPY --from=go-builder /wick_go /app/wick_go
RUN chmod +x /app/wick_go

# wickfs + wick-daemon binaries (for injection into sandbox containers)
COPY --from=go-builder /wickfs /usr/local/bin/wickfs
COPY --from=go-builder /wick-daemon /usr/local/bin/wick-daemon
RUN chmod +x /usr/local/bin/wickfs /usr/local/bin/wick-daemon

# UI static assets
COPY --from=ui-builder /static /app/static

# Skills
COPY wick_go/skills/ /app/skills/

# Raise file descriptor limit for high concurrency
RUN echo '* soft nofile 65536' >> /etc/security/limits.conf && \
    echo '* hard nofile 65536' >> /etc/security/limits.conf

EXPOSE 8000
ENV HOST=0.0.0.0
ENV PORT=8000
ENV OLLAMA_BASE_URL=http://host.docker.internal:11434

# Set ulimit in the entrypoint (limits.conf alone doesn't work in containers)
CMD ["sh", "-c", "ulimit -n 65536 && exec /app/wick_go"]
