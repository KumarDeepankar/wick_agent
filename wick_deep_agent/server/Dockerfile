# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /wick_go .

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates docker-cli

COPY --from=builder /wick_go /usr/local/bin/wick_go

# Copy static assets if present
COPY --from=builder /src/static/ /app/static/ 2>/dev/null || true

WORKDIR /app

# Default port matches Python agent
EXPOSE 8000

ENTRYPOINT ["wick_go"]
CMD ["-config", "/app/agents.yaml"]
