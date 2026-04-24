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

### With environment variables (easiest)

Provide your Nostr key and relays via environment variables - the container will auto-generate `bootstrap.json`:

```bash
docker run -d \
  --name metiqd \
  --restart unless-stopped \
  -e METIQ_NOSTR_KEY="nsec1..." \
  -e METIQ_NOSTR_RELAYS="wss://relay.damus.io,wss://nos.lol" \
  -v metiq-data:/data \
  ghcr.io/your-org/metiq:latest
```

### With existing config files

Mount your `~/.metiq` directory (which contains `bootstrap.json` and `config.json`):

```bash
docker run -d \
  --name metiqd \
  --restart unless-stopped \
  -v ~/.metiq:/data/.metiq \
  ghcr.io/your-org/metiq:latest
```

**Note**: If you see permission errors like `permission denied: /data/.metiq/sessions.json`, ensure your volume has correct ownership. The container's entrypoint automatically fixes permissions on startup when running as root (default behavior).

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

## Relay hairpin NAT workaround

If a relay lives on the same machine and its public `wss://` hostname hairpins back to that host, `metiqd` running on the host network may not be able to reach it even though other Docker containers can. This usually shows up as startup `WARN relay healthcheck ... unreachable` logs.

In that case, attach `metiqd` to the same Docker network as the relay and use the relay URL that is reachable from that network.

```yaml
version: "3.8"

services:
  relay:
    image: ghcr.io/your-org/relay:latest
    networks: [nostr]

  metiqd:
    image: ghcr.io/your-org/metiq:latest
    volumes:
      - metiq-data:/data/.metiq
    networks: [nostr]

networks:
  nostr:

volumes:
  metiq-data:
```

If you must keep `metiqd` on the host network, use a relay address that is routable from the host instead of the public hostname that loops back through NAT.

When using shared relays, set `storage.encrypt: true` in `~/.metiq/config.json` so relay-persisted config, transcript, and memory documents are self-encrypted before publication.

## Bootstrap Configuration

The `bootstrap.json` file contains startup configuration that metiqd reads before connecting to Nostr:

```json
{
  "private_key": "${NOSTR_NSEC}",
  "relays": [
    "wss://relay.damus.io",
    "wss://nos.lol"
  ],
  "admin_listen_addr": "127.0.0.1:7423",
  "model_context_overrides": {
    "lemmy-local/": 8192,
    "ollama/": 8192,
    "google_gemma": 8192
  }
}
```

### Model Context Window Overrides

If you're using local models (GGUF files, Ollama, etc.), add `model_context_overrides` to avoid context window warnings:

```json
{
  "model_context_overrides": {
    "lemmy-local/": 8192,           // All models from lemmy-local provider
    "ollama/": 8192,                // All ollama models
    "google_gemma": 8192,           // Gemma family
    "my-custom-model-v1": 16384    // Specific model
  }
}
```

Patterns are matched as **case-insensitive prefixes**. Without this, unknown models default to 200k tokens, which can cause issues with smaller models.

### Environment Variable Substitution

Use `${VAR_NAME}` in `bootstrap.json` to pull values from environment:

```json
{
  "private_key": "${NOSTR_NSEC}",
  "admin_token": "${METIQ_ADMIN_TOKEN}"
}
```

Then pass them to Docker:

```bash
docker run -d \
  -e NOSTR_NSEC="nsec1..." \
  -e METIQ_ADMIN_TOKEN="secret" \
  -v metiq-data:/data \
  ghcr.io/your-org/metiq:latest
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
- `bootstrap.json` — startup config (key, relays, admin addr, model overrides)
- `config.json` — runtime agent config
- `workspace/` — agent workspace (AGENTS.md, SOUL.md, memory, etc.)
- `sessions.json` — session metadata and settings
- `memory-index.json` — memory index (if using JSON FTS backend)
- `agents/*/sessions/` — session transcripts
- `cron/jobs.json` — cron job store
- `skills/` — installed skills

Mount everything under one volume at `/data/.metiq`. The image sets `HOME=/data`.

### Volume Permissions

The Docker image runs as the `metiq` user (UID 1000) for security. The entrypoint script automatically:

1. Starts as root
2. Creates `/data/.metiq` directory structure
3. Fixes ownership to `metiq:metiq`
4. Drops to the `metiq` user
5. Runs `metiqd`

This ensures no permission errors when using Docker volumes or bind mounts.

### Troubleshooting Permissions

If you see:
```
permission denied: /data/.metiq/sessions.json
```

Either:
- **Rebuild** the image (recent versions auto-fix this)
- **Manually fix** ownership:
  ```bash
  docker exec -u root metiqd chown -R metiq:metiq /data
  docker restart metiqd
  ```

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
