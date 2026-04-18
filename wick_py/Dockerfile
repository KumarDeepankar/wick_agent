# ── Stage 1: Build Go binary ──────────────────────────────────────────
FROM golang:1.24-alpine AS go-builder

WORKDIR /build
COPY wick_deep_agent/server/ ./server/

WORKDIR /build/server
RUN go build -o /wick_server ./cmd/wick_server/

# ── Stage 2: Build UI ────────────────────────────────────────────────
FROM node:20-alpine AS ui-builder

WORKDIR /build
COPY wick_py/ui/ ./ui/

WORKDIR /build/ui
RUN npm ci && npx tsc -b && npx vite build
# Output: /build/static/

# ── Stage 3: Runtime ─────────────────────────────────────────────────
FROM python:3.11-slim

# Install system deps: curl + Docker CLI (for Remote Docker sandbox mode)
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl ca-certificates gnupg \
    && install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/debian/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/debian bookworm stable" > /etc/apt/sources.list.d/docker.list \
    && apt-get update && apt-get install -y --no-install-recommends docker-ce-cli \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy Go binary
COPY --from=go-builder /wick_server /app/bin/wick_server

# Copy built UI static files
COPY --from=ui-builder /build/static/ /app/static/

# Copy Python SDK
COPY wick_py/wick/ /app/wick/
COPY wick_py/pyproject.toml /app/

# Copy examples and skills
COPY wick_py/examples/ /app/examples/
COPY wick_py/skills/ /app/skills/

# Copy OpenSearch CLI
COPY opensearch_cli/ /app/opensearch_cli/

# Install Python deps + OpenSearch CLI
COPY wick_py/requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r /app/requirements.txt \
    && pip install --no-cache-dir /app/opensearch_cli/

# Create workspace directory
RUN mkdir -p /workspace

# Env vars
ENV PYTHONPATH=/app
ENV WICK_SERVER_BINARY=/app/bin/wick_server
ENV WICK_SKILLS_DIR=/app/skills
ENV WICK_GO_HOST=0.0.0.0
ENV OLLAMA_HOST=http://host.docker.internal:11434
ENV OPENSEARCH_URL=http://host.docker.internal:9200
ENV OPENSEARCH_USER=
ENV OPENSEARCH_PASSWORD=
ENV PYTHONUNBUFFERED=1

EXPOSE 8000 9100

# Health check
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s \
    CMD curl -f http://localhost:8000/health || exit 1

CMD ["python", "/app/examples/server.py"]
