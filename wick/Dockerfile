# ── Stage 1: Build UI ────────────────────────────────────────────────────────
FROM node:20-alpine AS ui-build

WORKDIR /ui
COPY ui/package.json ui/package-lock.json* ./
RUN npm ci
COPY ui/ ./
RUN npm run build


# ── Stage 2: Python app ─────────────────────────────────────────────────────
FROM python:3.11-slim

WORKDIR /app

# Docker CLI (only the binary — no daemon, no apt repo needed)
COPY --from=docker:27-cli /usr/local/bin/docker /usr/local/bin/docker

# System deps
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Python deps
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# App code
COPY app/ app/
COPY skills/ skills/
COPY agents.yaml .
COPY run.py .
COPY .env* ./

# Built UI (vite.config.ts outputs to ../static relative to /ui)
COPY --from=ui-build /static/ static/

EXPOSE 8000

HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
    CMD curl -f http://localhost:8000/health || exit 1

CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000"]
