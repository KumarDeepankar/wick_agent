# Stage 1: Build
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go admin.html ./
RUN go build -ldflags="-s -w" -o /wick_gateway .

# Stage 2: Run
FROM alpine:3.20
COPY --from=build /wick_gateway /wick_gateway
EXPOSE 8080
ENTRYPOINT ["/wick_gateway", "-config", "/etc/wick_gateway/config.yaml"]
