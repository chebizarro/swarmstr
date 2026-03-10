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

```bash
docker run -d \
  --name swarmstrd \
  --restart unless-stopped \
  -e NOSTR_PRIVATE_KEY="nsec1..." \
  -e ANTHROPIC_API_KEY="sk-ant-..." \
  -v ~/.swarmstr:/data/.swarmstr \
  -p 18789:18789 \
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
    environment:
      - NOSTR_PRIVATE_KEY=${NOSTR_PRIVATE_KEY}
      - ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}
      - SWARMSTR_STATE_DIR=/data/.swarmstr
      - SWARMSTR_CONFIG_PATH=/data/.swarmstr/config.json
    volumes:
      - swarmstr-data:/data/.swarmstr
    ports:
      - "127.0.0.1:18789:18789"

volumes:
  swarmstr-data:
```

Use an `.env` file:

```bash
NOSTR_PRIVATE_KEY=nsec1...
ANTHROPIC_API_KEY=sk-ant-...
```

```bash
docker compose up -d
docker compose logs -f swarmstrd
```

## Config in the container

Mount a config file:

```bash
docker run -d \
  --name swarmstrd \
  -e NOSTR_PRIVATE_KEY="nsec1..." \
  -e ANTHROPIC_API_KEY="sk-ant-..." \
  -v /path/to/config.json:/data/.swarmstr/config.json:ro \
  -v swarmstr-data:/data/.swarmstr \
  ghcr.io/your-org/swarmstr:latest
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
- `config.json` — daemon config
- `workspace/` — agent workspace (AGENTS.md, SOUL.md, memory, etc.)
- `agents/*/sessions/` — session transcripts
- `cron/jobs.json` — cron job store
- `skills/` — installed skills

Mount everything under one volume at `SWARMSTR_STATE_DIR` (`/data/.swarmstr`).

## Health check

```bash
docker exec swarmstrd swarmstr health
docker logs swarmstrd --tail 50 -f
```

## Updating

```bash
docker pull ghcr.io/your-org/swarmstr:latest
docker compose up -d --pull always
```
