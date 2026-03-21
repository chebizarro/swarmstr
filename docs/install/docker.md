---
summary: "Run metiq in Docker"
read_when:
  - Running metiq in a container
  - Docker deployment
title: "Docker"
---

# Docker

metiq can run in Docker as a stateless Go binary with mounted config and state volumes.

## Quick start

Mount your `~/.metiq` directory (which contains `bootstrap.json` and `config.json`):

```bash
docker run -d \
  --name metiqd \
  --restart unless-stopped \
  -v ~/.metiq:/data/.metiq \
  ghcr.io/your-org/metiq:latest
```

If your `bootstrap.json` has `admin_listen_addr` set (e.g. `"127.0.0.1:7423"`), expose the port:

```bash
docker run -d \
  --name metiqd \
  --restart unless-stopped \
  -v ~/.metiq:/data/.metiq \
  -p 127.0.0.1:7423:7423 \
  ghcr.io/your-org/metiq:latest
```

## Docker Compose

```yaml
version: "3.8"

services:
  metiqd:
    image: ghcr.io/your-org/metiq:latest
    container_name: metiqd
    restart: unless-stopped
    volumes:
      - metiq-data:/data/.metiq
    # Expose admin API port only if admin_listen_addr is set in bootstrap.json
    # ports:
    #   - "127.0.0.1:7423:7423"

volumes:
  metiq-data:
```

Seed your config files into the volume before the first run:

```bash
docker run --rm \
  -v metiq-data:/data/.metiq \
  -v ~/.metiq:/src:ro \
  alpine sh -c "mkdir -p /data/.metiq && cp -r /src/. /data/.metiq/"
docker compose up -d
docker compose logs -f metiqd
```

## Config in the container

Mount individual config files read-only:

```bash
docker run -d \
  --name metiqd \
  -v /path/to/bootstrap.json:/data/.metiq/bootstrap.json:ro \
  -v /path/to/config.json:/data/.metiq/config.json:ro \
  -v metiq-workspace:/data/.metiq/workspace \
  ghcr.io/your-org/metiq:latest
```

The binary accepts `--bootstrap` and `--config` flags if you need non-default paths:

```bash
docker run -d \
  --name metiqd \
  -v /etc/metiq:/etc/metiq:ro \
  -v metiq-data:/data \\
  ghcr.io/your-org/metiq:latest \
  --bootstrap /etc/metiq/bootstrap.json \
  --config /etc/metiq/config.json
```

## Building from source

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o dist/metiqd ./cmd/metiqd/

FROM alpine:latest
RUN apk --no-cache add ca-certificates
COPY --from=builder /app/dist/metiqd /usr/local/bin/metiqd
ENTRYPOINT ["/usr/local/bin/metiqd"]
```

```bash
docker build -t metiqd:local .
```

## Volumes and data

metiq needs a persistent volume for:
- `bootstrap.json` — startup config (key, relays, admin addr)
- `config.json` — runtime agent config
- `workspace/` — agent workspace (AGENTS.md, SOUL.md, memory, etc.)
- `agents/*/sessions/` — session transcripts
- `cron/jobs.json` — cron job store
- `skills/` — installed skills

Mount everything under one volume at `/data/.metiq`. The image sets `HOME=/data`.

## Health check

```bash
docker exec metiqd metiq health
docker logs metiqd --tail 50
```

## Updating

```bash
docker pull ghcr.io/your-org/metiq:latest
docker compose up -d --pull always
```
