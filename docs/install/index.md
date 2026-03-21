---
summary: "metiq installation methods overview"
read_when:
  - Installing metiq for the first time
  - Choosing the right installation method
title: "Install"
---

# Install

metiq distributes as a single statically-linked Go binary — no runtime dependencies,
no npm, no node_modules.

## Installation methods

### Binary download (recommended)

Download the pre-built binary from GitHub Releases:

```bash
# Linux x86_64
curl -fsSL https://github.com/your-org/metiq/releases/latest/download/metiqd-linux-amd64 \
  -o /usr/local/bin/metiqd && chmod +x /usr/local/bin/metiqd

# Linux ARM64 (Raspberry Pi, AWS Graviton)
curl -fsSL https://github.com/your-org/metiq/releases/latest/download/metiqd-linux-arm64 \
  -o /usr/local/bin/metiqd && chmod +x /usr/local/bin/metiqd

# macOS ARM64 (Apple Silicon)
curl -fsSL https://github.com/your-org/metiq/releases/latest/download/metiqd-darwin-arm64 \
  -o /usr/local/bin/metiqd && chmod +x /usr/local/bin/metiqd
```

### Install script

```bash
curl -fsSL https://metiq.dev/install.sh | bash
```

### From source

Requirements: Go 1.22+

```bash
git clone https://github.com/your-org/metiq.git
cd metiq
go build -o dist/metiqd ./cmd/metiqd/
go build -o dist/metiq ./cmd/metiq/
sudo cp dist/metiqd /usr/local/bin/metiqd
```

### Docker

```bash
docker run -d \
  --name metiqd \
  -e NOSTR_PRIVATE_KEY="nsec1..." \
  -e ANTHROPIC_API_KEY="sk-ant-..." \
  -v ~/.metiq:/data/.metiq \
  ghcr.io/your-org/metiq:latest
```

See [Docker](/install/docker) for full setup.

## Platform-specific guides

- [Linux](/platforms/linux) — systemd service, package managers
- [Raspberry Pi](/platforms/raspberry-pi) — ARM64, low-power tips
- [Docker](/install/docker) — Container deployment
- [VPS (DigitalOcean, Hetzner, Oracle)](/platforms/digitalocean) — Cloud deployment

## After installing

1. Create `~/.metiq/bootstrap.json` with your private key and relays — see [Setup](/start/setup).
2. Create `~/.metiq/config.json` — see [Configuration](/gateway/configuration).
3. Start the daemon: `metiqd` or `systemctl start metiqd`. 
4. Verify: `metiq status`. 

## Verify installation

```bash
metiqd --version
metiq health
```

## CLI vs daemon

- **`metiqd`** — the daemon binary (long-running server process).
- **`metiq`** — the CLI client binary (communicates with running daemon).

Both are built from the same source. The CLI is used for management and automation.
