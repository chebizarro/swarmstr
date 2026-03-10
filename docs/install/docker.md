---
summary: "Run swarmstr in Docker"
read_when:
  - Running swarmstr in a container
  - Docker deployment
title: "Docker"
---

# Docker

swarmstr can run in Docker as a stateless Go binary with mounted config and state volumes.

## Quick start

Mount your `~/.swarmstr` directory (which contains `bootstrap.json` and `config.json`):

```bash
docker run -d \
  --name swarmstrd \
  --restart unless-stopped \
  -v ~/.swarmstr:/root/.swarmstr \
  ghcr.io/your-org/swarmstr:latest
```

If your `bootstrap.json` has `admin_listen_addr` set (e.g. `"127.0.0.1:7423"`), expose the port:

```bash
docker run -d \
  --name swarmstrd \
  --restart unless-stopped \
  -v ~/.swarmstr:/root/.swarmstr \
  -p 127.0.0.1:7423:7423 \
  ghcr.io/your-org/swarmstr:latest
```

## Docker Compose

```yaml
version: "3.8"

services:
  swarmstrd:
    image: ghcr.io/your-org/swarmstr:latest
    container_name: swarmstrd
    restart: unless-stopped
    volumes:
      - swarmstr-data:/root/.swarmstr
    # Expose admin API port only if admin_listen_addr is set in bootstrap.json
    # ports:
    #   - "127.0.0.1:7423:7423"

volumes:
  swarmstr-data:
```

Seed your config files into the volume before the first run:

```bash
docker run --rm \
  -v swarmstr-data:/root/.swarmstr \
  -v ~/.swarmstr:/src:ro \
  alpine sh -c "cp -r /src/. /root/.swarmstr/"
docker compose up -d
docker compose logs -f swarmstrd
```

## Config in the container

Mount individual config files read-only:

```bash
docker run -d \
  --name swarmstrd \
  -v /path/to/bootstrap.json:/root/.swarmstr/bootstrap.json:ro \
  -v /path/to/config.json:/root/.swarmstr/config.json:ro \
  -v swarmstr-workspace:/root/.swarmstr/workspace \
  ghcr.io/your-org/swarmstr:latest
```

The binary accepts `--bootstrap` and `--config` flags if you need non-default paths:

```bash
docker run -d \
  --name swarmstrd \
  -v /etc/swarmstr:/etc/swarmstr:ro \
  -v swarmstr-data:/data \
  ghcr.io/your-org/swarmstr:latest \
  --bootstrap /etc/swarmstr/bootstrap.json \
  --config /etc/swarmstr/config.json
```

## Building from source

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o dist/swarmstrd ./cmd/swarmstrd/

FROM alpine:latest
RUN apk --no-cache add ca-certificates
COPY --from=builder /app/dist/swarmstrd /usr/local/bin/swarmstrd
ENTRYPOINT ["/usr/local/bin/swarmstrd"]
```

```bash
docker build -t swarmstrd:local .
```

## Volumes and data

swarmstr needs a persistent volume for:
- `bootstrap.json` — startup config (key, relays, admin addr)
- `config.json` — runtime agent config
- `workspace/` — agent workspace (AGENTS.md, SOUL.md, memory, etc.)
- `agents/*/sessions/` — session transcripts
- `cron/jobs.json` — cron job store
- `skills/` — installed skills

Mount everything under one volume at `/root/.swarmstr`.

## Health check

```bash
docker exec swarmstrd swarmstr health
docker logs swarmstrd --tail 50
```

## Updating

```bash
docker pull ghcr.io/your-org/swarmstr:latest
docker compose up -d --pull always
```
