# Dev Workflow

## Prerequisites (one-time)

```bash
# Go 1.24+
go version

# Node.js 20+ (for UI development)
node --version
```

---

## Running wick_go

```bash
cd wick_go
go run .
# or
go build -o wick_go . && ./wick_go
```

Server starts on `http://localhost:8000`. Single process, no Python needed.

---

## What to do after changes

### 1. Changed Go server library (`wick_deep_agent/server/`)

No separate build step — `wick_go/go.mod` has a `replace` directive pointing to the local source. Just rebuild wick_go:

```bash
cd wick_go && go build -o wick_go . && ./wick_go
```

### 2. Changed wick_go app code (`wick_go/main.go`)

```bash
cd wick_go && go build -o wick_go . && ./wick_go
```

### 3. Changed UI code (`wick_go/ui/`)

For development (hot reload):
```bash
cd wick_go/ui && npm run dev    # :3000, proxies to :8000
```

For production build:
```bash
cd wick_go/ui && npm run build -- --outDir ../static
```

### 4. Changed skills (`wick_go/skills/`)

No rebuild needed. Skills are loaded at runtime from the filesystem.

---

## Building the standalone server binary

The `wick_deep_agent/server` can also be built as a standalone binary for `agents.yaml`-based deployments:

```bash
cd wick_deep_agent/server
go build -o wick_server ./cmd/wick_server/
./wick_server --config agents.yaml
```

---

## Docker

```bash
# From repo root
docker build -f wick_go/Dockerfile -t wick-agent .
docker run -p 8000:8000 wick-agent
```

---

## Quick reference

| Changed | Build command | Then |
|---------|-------------|------|
| `wick_deep_agent/server/**/*.go` | `cd wick_go && go build -o wick_go .` | `./wick_go` |
| `wick_go/main.go` | `cd wick_go && go build -o wick_go .` | `./wick_go` |
| `wick_go/ui/` | `cd wick_go/ui && npm run build -- --outDir ../static` | restart `./wick_go` |
| `wick_go/skills/` | none | restart `./wick_go` (or live) |
| Everything | `cd wick_go && go build -o wick_go .` | `./wick_go` |

---

## Verifying builds

```bash
# Library + all sub-packages
cd wick_deep_agent/server && go build ./...

# Standalone binary
cd wick_deep_agent/server && go build -o wick_server ./cmd/wick_server/

# wickfs CLI (injected into Docker containers for file ops)
cd wick_deep_agent/server && go build -o wickfs ./cmd/wickfs/

# wick-daemon (injected into Docker containers for fast command execution)
cd wick_deep_agent/server && go build -o wick-daemon ./cmd/wickdaemon/

# wick_go application
cd wick_go && go build -o wick_go .

# Tests
cd wick_deep_agent/server && go test ./...
```
