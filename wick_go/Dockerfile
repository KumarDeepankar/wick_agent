# =============================================================================
# wick_go — Containerized AI Agent Server
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

# Stage 1: Build Go server binary
FROM golang:1.24-alpine AS go-builder
WORKDIR /src
COPY wick_deep_agent/server/ .
RUN go build -o wick_go .

# Stage 2: Build UI static assets
FROM node:20-alpine AS ui-builder
WORKDIR /build
COPY wick_go/ui/package.json wick_go/ui/package-lock.json* ./
RUN npm ci --ignore-scripts
COPY wick_go/ui/ .
RUN npm run build -- --outDir /static

# Stage 3: Runtime
FROM python:3.11-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates curl gnupg && \
    install -m 0755 -d /etc/apt/keyrings && \
    curl -fsSL https://download.docker.com/linux/debian/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/debian $(. /etc/os-release && echo $VERSION_CODENAME) stable" > /etc/apt/sources.list.d/docker.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends docker-ce-cli && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Install wick_deep_agent Python package
COPY wick_deep_agent/pyproject.toml wick_deep_agent/README.md /tmp/wda/
COPY wick_deep_agent/wick_deep_agent/ /tmp/wda/wick_deep_agent/
RUN pip install --no-cache-dir /tmp/wda && rm -rf /tmp/wda

# Go binary
COPY --from=go-builder /src/wick_go /app/bin/wick_go
RUN chmod +x /app/bin/wick_go && \
    mkdir -p $(python -c "import wick_deep_agent; print(wick_deep_agent.__path__[0])")/bin && \
    ln -sf /app/bin/wick_go $(python -c "import wick_deep_agent; print(wick_deep_agent.__path__[0])")/bin/wick_go

# UI static assets
COPY --from=ui-builder /static /app/static

# App files
COPY wick_go/app.py wick_go/models.py /app/
COPY wick_go/skills/ /app/skills/

EXPOSE 8000
ENV HOST=0.0.0.0
ENV PORT=8000
ENV OLLAMA_BASE_URL=http://host.docker.internal:11434

CMD ["python", "app.py"]
